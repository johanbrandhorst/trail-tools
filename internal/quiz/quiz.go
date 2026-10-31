// Package quiz is a temporary Lord of the Rings quiz for a party, served at
// /quiz. It is self-contained so it is easy to remove afterwards: it has its
// own players, signed in with passkeys, its own tables in the quiz schema,
// and its own page, embedded from static/. See README.md for how to remove
// it.
package quiz

import (
	"cmp"
	"context"
	"crypto/rand"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-passkeys/go-passkeys/webauthn"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"gitlab.com/greyxor/slogor"
)

const (
	// Path is where the quiz is served. The server routes it and everything
	// under it to the quiz's handler.
	Path = "/quiz"
	// cookieName is the quiz's session cookie, separate from the app's.
	cookieName = "__Host-trail-tools-quiz"
	// sessionTTL is how long players stay signed in: the length of a party,
	// and then some.
	sessionTTL = 7 * 24 * time.Hour
	// challengeTTL is how long a browser has to complete a passkey ceremony.
	challengeTTL = 5 * time.Minute
	// maxBodyBytes bounds request bodies, which are small JSON documents.
	maxBodyBytes = 64 << 10
	// maxNameLength bounds player names, so they fit on the leaderboard.
	maxNameLength = 40
	// maxPromptLength and maxChoiceLength bound questions and their choices.
	maxPromptLength = 500
	maxChoiceLength = 200
	// contentSecurityPolicy only allows the quiz's own scripts, styles and
	// fonts, so text a player enters can't run scripts even through a bug
	// in rendering it.
	contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; font-src 'self'; " +
		"img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
)

//go:embed static
var staticFS embed.FS

// Server serves the quiz's page and its JSON API.
type Server struct {
	log    *slog.Logger
	report *errreport.Reporter
	repo   *repository
	rp     *webauthn.RelyingParty
	mux    *http.ServeMux
	// publicURL is the quiz's link, which guests open to join.
	publicURL string
}

// New creates the quiz's tables if needed, and returns its server. baseURL is
// the public URL of the app, which determines the passkeys' relying party.
// It's the same as the app's, so browsers may offer the app's passkeys too;
// those aren't players' and are refused.
func New(ctx context.Context, log *slog.Logger, report *errreport.Reporter, db *store.DB, baseURL string) (*Server, error) {
	return newServer(ctx, log, report, db, baseURL)
}

func newServer(ctx context.Context, log *slog.Logger, report *errreport.Reporter, db pgxBeginner, baseURL string) (*Server, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid base URL %q", baseURL)
	}
	if err := migrate(ctx, db); err != nil {
		return nil, fmt.Errorf("failed to create the quiz schema: %w", err)
	}
	s := &Server{
		log:    log,
		report: report,
		repo:   &repository{db: db},
		rp:     &webauthn.RelyingParty{ID: u.Hostname(), Origin: u.Scheme + "://" + u.Host},
		mux:    http.NewServeMux(),
		// The relying party's origin is the base URL without a trailing
		// path, which is where the app is served.
		publicURL: u.Scheme + "://" + u.Host + Path,
	}
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	files := http.StripPrefix(Path+"/static/", http.FileServerFS(static))
	s.mux.Handle("GET "+Path+"/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The quiz is changed live during the party, so browsers must check
		// for new versions; embedded files have no modification time to
		// validate against, but they're small.
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(w, r)
	}))
	index := func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, static, "index.html")
	}
	s.mux.HandleFunc("GET "+Path, index)
	s.mux.HandleFunc("GET "+Path+"/{$}", index)
	qrHandler, err := s.qrHandler()
	if err != nil {
		return nil, err
	}
	s.mux.Handle("GET "+Path+"/qr.svg", qrHandler)

	api := func(pattern string, h func(w http.ResponseWriter, r *http.Request) error) {
		method, path, _ := strings.Cut(pattern, " ")
		s.mux.HandleFunc(method+" "+Path+"/api"+path, s.apiHandler(h))
	}
	api("GET /me", s.me)
	api("POST /register/begin", s.beginRegistration)
	api("POST /register/finish", s.finishRegistration)
	api("POST /login/begin", s.beginLogin)
	api("POST /login/finish", s.finishLogin)
	api("POST /logout", s.logout)
	api("GET /quizzes", s.listQuizzes)
	api("GET /quizzes/{tier}", s.getQuiz)
	api("POST /quizzes/{tier}/submit", s.submit)
	api("GET /leaderboard", s.leaderboard)
	api("GET /admin/questions", s.adminOnly(s.listQuestions))
	api("POST /admin/questions", s.adminOnly(s.createQuestion))
	api("PUT /admin/questions/{id}", s.adminOnly(s.updateQuestion))
	api("DELETE /admin/questions/{id}", s.adminOnly(s.deleteQuestion))
	api("GET /admin/users", s.adminOnly(s.listUsers))
	api("PUT /admin/users/{id}", s.adminOnly(s.updateUser))
	api("DELETE /admin/users/{id}", s.adminOnly(s.deleteUser))
	api("DELETE /admin/users/{id}/submissions/{tier}", s.adminOnly(s.resetSubmission))
	api("PUT /admin/settings", s.adminOnly(s.updateSettings))
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// httpError is an error with the status code and message to respond with.
type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

func errorf(status int, format string, args ...any) error {
	return &httpError{status: status, msg: fmt.Sprintf(format, args...)}
}

var errSignIn = errorf(http.StatusUnauthorized, "sign in with your passkey first")

type ctxKey struct{}

// apiHandler adapts a JSON handler: it checks that requests that change
// anything come from the quiz's page, identifies the player, and writes
// errors as JSON.
func (s *Server) apiHandler(h func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		err := func() error {
			if r.Method != http.MethodGet {
				// Other sites can't send JSON without a CORS preflight, which
				// the server never allows, nor a matching Origin, so this
				// stops cross-site request forgery along with the cookie's
				// SameSite.
				if mt, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";"); strings.TrimSpace(mt) != "application/json" {
					return errorf(http.StatusUnsupportedMediaType, "requests must be JSON")
				}
				if o := r.Header.Get("Origin"); o != "" && o != s.rp.Origin {
					return errorf(http.StatusForbidden, "requests must come from the quiz's page")
				}
			}
			if c, err := r.Cookie(cookieName); err == nil {
				u, err := s.repo.sessionUser(r.Context(), c.Value)
				switch {
				case err == nil:
					r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, u))
				case !errors.Is(err, store.ErrNotFound):
					return fmt.Errorf("failed to get session: %w", err)
				}
			}
			return h(w, r)
		}()
		if err == nil {
			return
		}
		if he := (*httpError)(nil); errors.As(err, &he) {
			writeJSON(w, he.status, map[string]string{"error": he.msg})
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found; reload the page"})
			return
		}
		s.report.Report(r.Context(), "Failed to handle a quiz request", err, slog.String("path", r.URL.Path))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "something went wrong; try again"})
	}
}

// adminOnly only lets the quiz master through.
func (s *Server) adminOnly(h func(w http.ResponseWriter, r *http.Request) error) func(w http.ResponseWriter, r *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		u, ok := currentUser(r)
		if !ok {
			return errSignIn
		}
		if !u.IsAdmin {
			return errorf(http.StatusForbidden, "only the quiz master may do that")
		}
		return h(w, r)
	}
}

func currentUser(r *http.Request) (User, bool) {
	u, ok := r.Context().Value(ctxKey{}).(User)
	return u, ok
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(v); err != nil {
		return errorf(http.StatusBadRequest, "invalid request: %v", err)
	}
	return nil
}

// pathID returns a path parameter that must be an ID.
func pathID(r *http.Request, name string) (string, error) {
	id := r.PathValue(name)
	if _, err := store.StringToUUID(id); err != nil {
		return "", store.ErrNotFound
	}
	return id, nil
}

func pathTier(r *http.Request) (Tier, error) {
	t := Tier(r.PathValue("tier"))
	if !validTier(t) {
		return "", errorf(http.StatusNotFound, "there's no %q quiz", t)
	}
	return t, nil
}

// cleanName trims a player's name and checks its length.
func cleanName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "", errorf(http.StatusBadRequest, "enter your name")
	}
	if utf8.RuneCountInString(name) > maxNameLength {
		return "", errorf(http.StatusBadRequest, "names must be at most %d characters", maxNameLength)
	}
	return name, nil
}

var errNameTakenHTTP = errorf(http.StatusConflict,
	"someone in the Fellowship already goes by that name; choose another, or sign in with your passkey if it's you")

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

// signIn creates a session for the player and sets its cookie.
func (s *Server) signIn(w http.ResponseWriter, r *http.Request, u User) error {
	token := rand.Text()
	expiry := time.Now().Add(sessionTTL)
	if err := s.repo.createSession(r.Context(), u.ID, token, expiry); err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiry,
		HttpOnly: true,
		// Browsers treat localhost as secure, so this works locally too.
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	s.log.InfoContext(r.Context(), "Quiz player signed in", slog.String("quiz_user_id", u.ID), slog.Bool("admin", u.IsAdmin))
	return nil
}

type meResponse struct {
	// User is the signed in player, or null.
	User          *User `json:"user"`
	RevealAnswers bool  `json:"revealAnswers"`
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) error {
	reveal, err := s.repo.revealAnswers(r.Context())
	if err != nil {
		return err
	}
	resp := meResponse{RevealAnswers: reveal}
	if u, ok := currentUser(r); ok {
		resp.User = &u
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(cookieName); err == nil {
		if err := s.repo.deleteSession(r.Context(), c.Value); err != nil {
			return fmt.Errorf("failed to delete session: %w", err)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, struct{}{})
	return nil
}

// quizSummary is a tier, and the signed in player's result in it.
type quizSummary struct {
	Tier          Tier `json:"tier"`
	QuestionCount int  `json:"questionCount"`
	Points        int  `json:"pointsPerQuestion"`
	Submitted     bool `json:"submitted"`
	// Correct is how many questions the player got right, once submitted.
	Correct int `json:"correct"`
}

func (s *Server) listQuizzes(w http.ResponseWriter, r *http.Request) error {
	counts, err := s.repo.questionCounts(r.Context())
	if err != nil {
		return err
	}
	results := map[Tier]submission{}
	if u, ok := currentUser(r); ok {
		subs, err := s.repo.submissions(r.Context(), u.ID)
		if err != nil {
			return err
		}
		for _, sub := range subs {
			results[sub.Tier] = sub
		}
	}
	quizzes := make([]quizSummary, len(tiers))
	for i, t := range tiers {
		sub, submitted := results[t]
		quizzes[i] = quizSummary{Tier: t, QuestionCount: counts[t], Points: points[t], Submitted: submitted, Correct: sub.Correct}
	}
	writeJSON(w, http.StatusOK, map[string]any{"quizzes": quizzes})
	return nil
}

// playerQuestion is a question as players see it: without the answer until
// they've submitted and the quiz master reveals the answers.
type playerQuestion struct {
	ID      string   `json:"id"`
	Prompt  string   `json:"prompt"`
	Choices []string `json:"choices"`
	// Chosen is the player's answer once submitted, or -1.
	Chosen int `json:"chosen"`
	// Correct is whether the player's answer was right, once submitted.
	Correct bool `json:"correct"`
	// AnswerIndex is the correct answer, or -1 while it's hidden.
	AnswerIndex int `json:"answerIndex"`
}

func (s *Server) getQuiz(w http.ResponseWriter, r *http.Request) error {
	u, ok := currentUser(r)
	if !ok {
		return errSignIn
	}
	tier, err := pathTier(r)
	if err != nil {
		return err
	}
	questions, err := s.repo.questions(r.Context(), tier)
	if err != nil {
		return err
	}
	subs, err := s.repo.submissions(r.Context(), u.ID)
	if err != nil {
		return err
	}
	submitted := slices.ContainsFunc(subs, func(sub submission) bool { return sub.Tier == tier })
	var answers map[string]int
	reveal := false
	if submitted {
		if answers, err = s.repo.answers(r.Context(), u.ID, tier); err != nil {
			return err
		}
		if reveal, err = s.repo.revealAnswers(r.Context()); err != nil {
			return err
		}
	}
	out := make([]playerQuestion, len(questions))
	for i, q := range questions {
		pq := playerQuestion{ID: q.ID, Prompt: q.Prompt, Choices: q.Choices, Chosen: -1, AnswerIndex: -1}
		if submitted {
			if c, ok := answers[q.ID]; ok {
				pq.Chosen = c
				pq.Correct = c == q.AnswerIndex
			}
			// The quiz master knows the answers anyway.
			if reveal || u.IsAdmin {
				pq.AnswerIndex = q.AnswerIndex
			}
		}
		out[i] = pq
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tier":              tier,
		"pointsPerQuestion": points[tier],
		"submitted":         submitted,
		"questions":         out,
	})
	return nil
}

func (s *Server) submit(w http.ResponseWriter, r *http.Request) error {
	u, ok := currentUser(r)
	if !ok {
		return errSignIn
	}
	if u.IsAdmin {
		return errorf(http.StatusForbidden, "the quiz master knows all the answers, so can't take the quiz")
	}
	tier, err := pathTier(r)
	if err != nil {
		return err
	}
	var req struct {
		// Answers are the chosen choices' indexes by question ID.
		Answers map[string]int `json:"answers"`
	}
	if err := readJSON(w, r, &req); err != nil {
		return err
	}
	questions, err := s.repo.questions(r.Context(), tier)
	if err != nil {
		return err
	}
	byID := map[string]Question{}
	for _, q := range questions {
		byID[q.ID] = q
	}
	for id, choice := range req.Answers {
		q, ok := byID[id]
		if !ok {
			return errorf(http.StatusConflict, "the questions have changed since you started; reload the quiz")
		}
		if choice < 0 || choice >= len(q.Choices) {
			return errorf(http.StatusBadRequest, "invalid choice for %q", q.Prompt)
		}
	}
	if err := s.repo.submit(r.Context(), u.ID, tier, req.Answers); err != nil {
		if errors.Is(err, errAlreadySubmitted) {
			return errorf(http.StatusConflict, "you've already submitted this quiz; ask the quiz master to reset it to take it again")
		}
		return err
	}
	s.log.InfoContext(r.Context(), "Quiz submitted", slog.String("quiz_user_id", u.ID), slog.String("tier", string(tier)))
	return s.getQuiz(w, r)
}

// tierResult is a player's result in a tier.
type tierResult struct {
	Submitted bool `json:"submitted"`
	Correct   int  `json:"correct"`
	Total     int  `json:"total"`
}

// leaderboardEntry is a player's place on the leaderboard.
type leaderboardEntry struct {
	Rank   int                 `json:"rank"`
	UserID string              `json:"userId"`
	Name   string              `json:"name"`
	Points int                 `json:"points"`
	Tiers  map[Tier]tierResult `json:"tiers"`
	// lastSubmitTime breaks ties: the first to reach a score ranks higher.
	lastSubmitTime time.Time
}

func (s *Server) leaderboard(w http.ResponseWriter, r *http.Request) error {
	users, err := s.repo.users(r.Context())
	if err != nil {
		return err
	}
	subs, err := s.repo.submissions(r.Context(), "")
	if err != nil {
		return err
	}
	counts, err := s.repo.questionCounts(r.Context())
	if err != nil {
		return err
	}
	byUser := map[string]*leaderboardEntry{}
	for _, sub := range subs {
		id := store.UUIDToString(sub.UserID)
		e := byUser[id]
		if e == nil {
			e = &leaderboardEntry{UserID: id, Tiers: map[Tier]tierResult{}}
			byUser[id] = e
		}
		e.Points += sub.Correct * points[sub.Tier]
		e.Tiers[sub.Tier] = tierResult{Submitted: true, Correct: sub.Correct, Total: counts[sub.Tier]}
		if sub.SubmitTime.After(e.lastSubmitTime) {
			e.lastSubmitTime = sub.SubmitTime
		}
	}
	entries := []*leaderboardEntry{}
	for _, u := range users {
		e := byUser[u.ID]
		// The quiz master doesn't compete.
		if e == nil || u.IsAdmin {
			continue
		}
		e.Name = u.Name
		for _, t := range tiers {
			if _, ok := e.Tiers[t]; !ok {
				e.Tiers[t] = tierResult{Total: counts[t]}
			}
		}
		entries = append(entries, e)
	}
	slices.SortFunc(entries, func(a, b *leaderboardEntry) int {
		return cmp.Or(cmp.Compare(b.Points, a.Points), a.lastSubmitTime.Compare(b.lastSubmitTime), strings.Compare(a.Name, b.Name))
	})
	for i, e := range entries {
		e.Rank = i + 1
		// Players with the same points share a rank.
		if i > 0 && e.Points == entries[i-1].Points {
			e.Rank = entries[i-1].Rank
		}
	}
	maxPoints := 0
	for _, t := range tiers {
		maxPoints += counts[t] * points[t]
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "maxPoints": maxPoints})
	return nil
}

func (s *Server) listQuestions(w http.ResponseWriter, r *http.Request) error {
	questions, err := s.repo.questions(r.Context(), "")
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"questions": questions})
	return nil
}

// readQuestion reads and validates a question from the request.
func readQuestion(w http.ResponseWriter, r *http.Request) (Question, error) {
	var q Question
	if err := readJSON(w, r, &q); err != nil {
		return Question{}, err
	}
	if !validTier(q.Tier) {
		return Question{}, errorf(http.StatusBadRequest, "tier must be easy, medium or hard")
	}
	q.Prompt = strings.TrimSpace(q.Prompt)
	if q.Prompt == "" {
		return Question{}, errorf(http.StatusBadRequest, "enter the question")
	}
	if utf8.RuneCountInString(q.Prompt) > maxPromptLength {
		return Question{}, errorf(http.StatusBadRequest, "questions must be at most %d characters", maxPromptLength)
	}
	if len(q.Choices) < 2 || len(q.Choices) > 6 {
		return Question{}, errorf(http.StatusBadRequest, "questions must have 2 to 6 choices")
	}
	for i, c := range q.Choices {
		c = strings.TrimSpace(c)
		if c == "" {
			return Question{}, errorf(http.StatusBadRequest, "choice %d is empty; fill it in or remove it", i+1)
		}
		if utf8.RuneCountInString(c) > maxChoiceLength {
			return Question{}, errorf(http.StatusBadRequest, "choices must be at most %d characters", maxChoiceLength)
		}
		q.Choices[i] = c
	}
	if q.AnswerIndex < 0 || q.AnswerIndex >= len(q.Choices) {
		return Question{}, errorf(http.StatusBadRequest, "mark which choice is correct")
	}
	return q, nil
}

func (s *Server) createQuestion(w http.ResponseWriter, r *http.Request) error {
	q, err := readQuestion(w, r)
	if err != nil {
		return err
	}
	if q, err = s.repo.createQuestion(r.Context(), q); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, q)
	return nil
}

func (s *Server) updateQuestion(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	q, err := readQuestion(w, r)
	if err != nil {
		return err
	}
	q.ID = id
	if err := s.repo.updateQuestion(r.Context(), q); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, q)
	return nil
}

func (s *Server) deleteQuestion(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.repo.deleteQuestion(r.Context(), id); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, struct{}{})
	return nil
}

// adminUser is a player as the quiz master sees them.
type adminUser struct {
	User
	// Submitted lists the tiers the player submitted.
	Submitted []Tier `json:"submitted"`
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) error {
	users, err := s.repo.users(r.Context())
	if err != nil {
		return err
	}
	subs, err := s.repo.submissions(r.Context(), "")
	if err != nil {
		return err
	}
	submitted := map[string][]Tier{}
	for _, sub := range subs {
		id := store.UUIDToString(sub.UserID)
		submitted[id] = append(submitted[id], sub.Tier)
	}
	out := make([]adminUser, len(users))
	for i, u := range users {
		ts := append([]Tier{}, submitted[u.ID]...)
		slices.SortFunc(ts, func(a, b Tier) int { return slices.Index(tiers, a) - slices.Index(tiers, b) })
		out[i] = adminUser{User: u, Submitted: ts}
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
	return nil
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) error {
	me, _ := currentUser(r)
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Name    string `json:"name"`
		IsAdmin bool   `json:"isAdmin"`
	}
	if err := readJSON(w, r, &req); err != nil {
		return err
	}
	name, err := cleanName(req.Name)
	if err != nil {
		return err
	}
	if id == me.ID && !req.IsAdmin {
		return errorf(http.StatusBadRequest, "you can't stop being the quiz master yourself; make someone else quiz master and ask them")
	}
	if err := s.repo.updateUser(r.Context(), id, name, req.IsAdmin); err != nil {
		if errors.Is(err, errNameTaken) {
			return errNameTakenHTTP
		}
		return err
	}
	writeJSON(w, http.StatusOK, struct{}{})
	return nil
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) error {
	me, _ := currentUser(r)
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if id == me.ID {
		return errorf(http.StatusBadRequest, "you can't remove yourself")
	}
	if err := s.repo.deleteUser(r.Context(), id); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, struct{}{})
	return nil
}

func (s *Server) resetSubmission(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	tier, err := pathTier(r)
	if err != nil {
		return err
	}
	if err := s.repo.resetSubmission(r.Context(), id, tier); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, struct{}{})
	return nil
}

func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		RevealAnswers bool `json:"revealAnswers"`
	}
	if err := readJSON(w, r, &req); err != nil {
		return err
	}
	if err := s.repo.setRevealAnswers(r.Context(), req.RevealAnswers); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, req)
	return nil
}

// logDebug logs an expected failure of a player's request.
func (s *Server) logDebug(r *http.Request, msg string, err error) {
	s.log.DebugContext(r.Context(), msg, slogor.Err(err))
}
