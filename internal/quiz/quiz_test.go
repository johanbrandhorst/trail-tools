package quiz

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	"github.com/johanbrandhorst/trail-tools/internal/store/storetest"
)

// player is a browser of a player: it keeps the quiz's session cookie,
// which Go's cookie jar wouldn't send over plain HTTP as it's secure.
type player struct {
	t       *testing.T
	baseURL string
	cookie  string
	auth    *authenticator
	// credentialID is the player's passkey, once registered.
	credentialID []byte
}

// do sends a JSON request and decodes the response into out, if set,
// returning the status code.
func (p *player) do(method, path string, body, out any) int {
	p.t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			p.t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, p.baseURL+Path+"/api"+path, reader)
	if err != nil {
		p.t.Fatal(err)
	}
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", p.baseURL)
	}
	if p.cookie != "" {
		req.AddCookie(&http.Cookie{Name: cookieName, Value: p.cookie})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		p.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			p.cookie = c.Value
		}
	}
	if out != nil && resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			p.t.Fatalf("%s %s: decoding response: %v", method, path, err)
		}
	}
	return resp.StatusCode
}

// mustDo is do, failing the test unless the response is OK.
func (p *player) mustDo(method, path string, body, out any) {
	p.t.Helper()
	if code := p.do(method, path, body, out); code != http.StatusOK {
		p.t.Fatalf("%s %s: got status %d, want 200", method, path, code)
	}
}

// register joins the quiz with a new passkey.
func (p *player) register(name string) (User, int) {
	p.t.Helper()
	var begin beginRegistrationResponse
	if code := p.do(http.MethodPost, "/register/begin", map[string]string{"name": name}, &begin); code != http.StatusOK {
		return User{}, code
	}
	id, clientData, attestation := p.auth.create(begin.Challenge, begin.UserHandle)
	var me meResponse
	code := p.do(http.MethodPost, "/register/finish", map[string]any{
		"challengeId":       begin.ChallengeID,
		"clientDataJson":    clientData,
		"attestationObject": attestation,
	}, &me)
	if code != http.StatusOK {
		return User{}, code
	}
	p.credentialID = id
	return *me.User, code
}

// signIn signs in with the player's passkey.
func (p *player) signIn() User {
	p.t.Helper()
	var begin beginLoginResponse
	p.mustDo(http.MethodPost, "/login/begin", nil, &begin)
	clientData, authData, sig := p.auth.get(p.credentialID, begin.Challenge)
	var me meResponse
	p.mustDo(http.MethodPost, "/login/finish", map[string]any{
		"challengeId":       begin.ChallengeID,
		"credentialId":      p.credentialID,
		"clientDataJson":    clientData,
		"authenticatorData": authData,
		"signature":         sig,
	}, &me)
	return *me.User
}

type leaderboardResponse struct {
	Entries []struct {
		Rank   int    `json:"rank"`
		Name   string `json:"name"`
		Points int    `json:"points"`
	} `json:"entries"`
	MaxPoints int `json:"maxPoints"`
}

type quizResponse struct {
	Submitted bool `json:"submitted"`
	Questions []struct {
		ID          string `json:"id"`
		Chosen      int    `json:"chosen"`
		Correct     bool   `json:"correct"`
		AnswerIndex int    `json:"answerIndex"`
	} `json:"questions"`
}

func TestQuiz(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := storetest.New(t)
	srv := httptest.NewServer(nil)
	t.Cleanup(srv.Close)
	log := slog.New(slog.DiscardHandler)
	s, err := newServer(ctx, log, errreport.NewLocal(log), db, srv.URL)
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	srv.Config.Handler = s
	// Creating the schema again, as on every start, keeps the questions.
	if err := migrate(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	u, _ := url.Parse(srv.URL)
	newPlayer := func() *player {
		return &player{t: t, baseURL: srv.URL, auth: newAuthenticator(t, srv.URL, u.Hostname())}
	}

	// The page is served, with its own content security policy.
	resp, err := http.Get(srv.URL + Path)
	if err != nil {
		t.Fatalf("GET %s: %v", Path, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Errorf("GET %s: got status %d and CSP %q", Path, resp.StatusCode, resp.Header.Get("Content-Security-Policy"))
	}

	// The QR code links to the quiz.
	resp, err = http.Get(srv.URL + Path + "/qr.svg")
	if err != nil {
		t.Fatalf("GET QR code: %v", err)
	}
	svg, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/svg+xml" || !bytes.HasPrefix(svg, []byte("<svg")) {
		t.Errorf("GET QR code: got status %d, type %q, body %.40q", resp.StatusCode, resp.Header.Get("Content-Type"), svg)
	}

	// The first player to join becomes the quiz master; the next don't.
	master, bob := newPlayer(), newPlayer()
	if got, code := master.register("  Gandalf   the Grey "); code != http.StatusOK || !got.IsAdmin || got.Name != "Gandalf the Grey" {
		t.Fatalf("register Gandalf: got %+v, %d, want the quiz master", got, code)
	}
	if got, code := bob.register("Bilbo"); code != http.StatusOK || got.IsAdmin {
		t.Fatalf("register Bilbo: got %+v, %d, want a player", got, code)
	}
	// Names are unique, ignoring case.
	if _, code := newPlayer().register("bilbo"); code != http.StatusConflict {
		t.Errorf("register bilbo again: got status %d, want 409", code)
	}

	// Bilbo signs in again on a new session with his passkey.
	bob.cookie = ""
	if got := bob.signIn(); got.Name != "Bilbo" {
		t.Errorf("signIn: got %+v, want Bilbo", got)
	}

	// Anonymous visitors can see the leaderboard, but not the questions.
	anon := newPlayer()
	if code := anon.do(http.MethodGet, "/quizzes/easy", nil, nil); code != http.StatusUnauthorized {
		t.Errorf("anonymous GET quiz: got status %d, want 401", code)
	}
	if code := anon.do(http.MethodGet, "/leaderboard", nil, nil); code != http.StatusOK {
		t.Errorf("anonymous GET leaderboard: got status %d, want 200", code)
	}
	// Players can't use the quiz master's page.
	if code := bob.do(http.MethodGet, "/admin/questions", nil, nil); code != http.StatusForbidden {
		t.Errorf("player GET admin questions: got status %d, want 403", code)
	}
	// Requests that change anything must be JSON from the quiz's page.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+Path+"/api/quizzes/easy/submit", strings.NewReader("answers=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: cookieName, Value: bob.cookie})
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("form POST: got %v, %v, want 415", resp, err)
	}

	// The questions don't give away their answers.
	var quiz quizResponse
	bob.mustDo(http.MethodGet, "/quizzes/easy", nil, &quiz)
	if len(quiz.Questions) != 10 || quiz.Submitted {
		t.Fatalf("GET easy quiz: got %d questions, submitted %v, want 10 unsubmitted", len(quiz.Questions), quiz.Submitted)
	}
	for _, q := range quiz.Questions {
		if q.AnswerIndex != -1 {
			t.Errorf("question %s: got answer %d before submitting, want it hidden", q.ID, q.AnswerIndex)
		}
	}

	// Bilbo answers every easy question correctly but the last.
	var questions struct{ Questions []Question }
	master.mustDo(http.MethodGet, "/admin/questions", nil, &questions)
	answers := map[string]int{}
	var easy []Question
	for _, q := range questions.Questions {
		if q.Tier == TierEasy {
			easy = append(easy, q)
			answers[q.ID] = q.AnswerIndex
		}
	}
	last := easy[len(easy)-1]
	answers[last.ID] = (last.AnswerIndex + 1) % len(last.Choices)
	bob.mustDo(http.MethodPost, "/quizzes/easy/submit", map[string]any{"answers": answers}, &quiz)
	correct := 0
	for _, q := range quiz.Questions {
		if q.Correct {
			correct++
		}
		if q.AnswerIndex != -1 {
			t.Errorf("question %s: got answer %d before they're revealed, want it hidden", q.ID, q.AnswerIndex)
		}
	}
	if !quiz.Submitted || correct != 9 {
		t.Errorf("submit: got submitted %v with %d correct, want 9", quiz.Submitted, correct)
	}
	// Each quiz is submitted once.
	if code := bob.do(http.MethodPost, "/quizzes/easy/submit", map[string]any{"answers": answers}, nil); code != http.StatusConflict {
		t.Errorf("submit again: got status %d, want 409", code)
	}
	// The leaderboard has Bilbo, but not the quiz master, who hasn't
	// submitted anything yet.
	var board leaderboardResponse
	anon.mustDo(http.MethodGet, "/leaderboard", nil, &board)
	if len(board.Entries) != 1 || board.Entries[0].Name != "Bilbo" || board.Entries[0].Points != 9 || board.Entries[0].Rank != 1 {
		t.Errorf("leaderboard: got %+v, want Bilbo first with 9 points", board.Entries)
	}
	if board.MaxPoints != 10*1+10*2+10*3 {
		t.Errorf("leaderboard: got %d max points, want 60", board.MaxPoints)
	}

	// Correcting the last question's answer to Bilbo's re-scores him.
	last.AnswerIndex = answers[last.ID]
	master.mustDo(http.MethodPut, "/admin/questions/"+last.ID, last, nil)
	anon.mustDo(http.MethodGet, "/leaderboard", nil, &board)
	if len(board.Entries) != 1 || board.Entries[0].Points != 10 {
		t.Errorf("leaderboard after correcting: got %+v, want Bilbo with 10 points", board.Entries)
	}

	// Revealing the answers shows them to players who submitted.
	master.mustDo(http.MethodPut, "/admin/settings", map[string]bool{"revealAnswers": true}, nil)
	bob.mustDo(http.MethodGet, "/quizzes/easy", nil, &quiz)
	for _, q := range quiz.Questions {
		if q.AnswerIndex == -1 {
			t.Errorf("question %s: got the answer hidden after revealing", q.ID)
		}
	}

	// Invalid questions are refused with a message.
	if code := master.do(http.MethodPost, "/admin/questions", Question{Tier: TierHard, Prompt: "Who?", Choices: []string{"Only one"}}, nil); code != http.StatusBadRequest {
		t.Errorf("create invalid question: got status %d, want 400", code)
	}
	var created Question
	master.mustDo(http.MethodPost, "/admin/questions", Question{Tier: TierHard, Prompt: "Who?", Choices: []string{"Tom", "Fatty"}, AnswerIndex: 1}, &created)
	if created.Position != 10 {
		t.Errorf("create question: got position %d, want 10, the end of the tier", created.Position)
	}

	// The quiz master can't step down alone, nor remove themselves.
	var users struct{ Users []adminUser }
	master.mustDo(http.MethodGet, "/admin/users", nil, &users)
	if len(users.Users) != 2 || len(users.Users[1].Submitted) != 1 {
		t.Fatalf("list users: got %+v, want Gandalf and Bilbo with one submission", users.Users)
	}
	gandalf, bilbo := users.Users[0], users.Users[1]
	if code := master.do(http.MethodPut, "/admin/users/"+gandalf.ID, map[string]any{"name": "Gandalf", "isAdmin": false}, nil); code != http.StatusBadRequest {
		t.Errorf("demote self: got status %d, want 400", code)
	}
	if code := master.do(http.MethodDelete, "/admin/users/"+gandalf.ID, nil, nil); code != http.StatusBadRequest {
		t.Errorf("remove self: got status %d, want 400", code)
	}

	// Resetting Bilbo's quiz takes him off the leaderboard, and lets him
	// take it again.
	master.mustDo(http.MethodDelete, "/admin/users/"+bilbo.ID+"/submissions/easy", nil, nil)
	anon.mustDo(http.MethodGet, "/leaderboard", nil, &board)
	if len(board.Entries) != 0 {
		t.Errorf("leaderboard after reset: got %+v, want none", board.Entries)
	}
	bob.mustDo(http.MethodPost, "/quizzes/easy/submit", map[string]any{"answers": answers}, nil)

	// Removing Bilbo signs him out.
	master.mustDo(http.MethodDelete, "/admin/users/"+bilbo.ID, nil, nil)
	if code := bob.do(http.MethodGet, "/quizzes/easy", nil, nil); code != http.StatusUnauthorized {
		t.Errorf("removed player GET quiz: got status %d, want 401", code)
	}

	// The quiz master plays too, and makes the leaderboard.
	master.mustDo(http.MethodPost, "/quizzes/easy/submit", map[string]any{"answers": answers}, nil)
	anon.mustDo(http.MethodGet, "/leaderboard", nil, &board)
	if len(board.Entries) != 1 || board.Entries[0].Name != "Gandalf the Grey" || board.Entries[0].Points != 10 {
		t.Errorf("leaderboard with the quiz master: got %+v, want Gandalf with 10 points", board.Entries)
	}
}
