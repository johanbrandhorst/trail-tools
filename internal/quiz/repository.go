package quiz

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// pgxBeginner is the database: a pool, or a test's connection.
type pgxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var (
	// errNameTaken is returned when another player has the name.
	errNameTaken = errors.New("name taken")
	// errAlreadySubmitted is returned when the player submitted the tier
	// before.
	errAlreadySubmitted = errors.New("already submitted")
)

// registrationLockID serializes registrations, so that only the very first
// player becomes the quiz master even if two join at the same moment. It is
// "quiz" in ASCII plus one.
const registrationLockID = schemaLockID + 1

// User is a player of the quiz.
type User struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	IsAdmin bool   `json:"isAdmin"`
	// CreateTime is when the player joined.
	CreateTime time.Time `json:"createTime"`
}

// Question is a multiple choice question of a tier.
type Question struct {
	ID       string `json:"id"`
	Tier     Tier   `json:"tier"`
	Position int    `json:"position"`
	Prompt   string `json:"prompt"`
	// Choices are the possible answers, in the order they're shown.
	Choices []string `json:"choices"`
	// AnswerIndex is the index of the correct choice.
	AnswerIndex int `json:"answerIndex"`
}

// passkey is a player's registered passkey.
type passkey struct {
	ID         []byte
	UserID     pgtype.UUID
	UserHandle []byte
	PublicKey  []byte
	Algorithm  int32
	SignCount  int64
}

// challenge is a pending WebAuthn ceremony.
type challenge struct {
	Ceremony   string
	Challenge  []byte
	UserHandle []byte
	UserName   string
}

// submission is a tier a player submitted, and how many of its questions
// they got right.
type submission struct {
	UserID     pgtype.UUID
	Tier       Tier
	SubmitTime time.Time
	Correct    int
}

// repository stores the quiz.
type repository struct {
	db pgxBeginner
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

func scanUser(row pgx.Row) (User, error) {
	var u User
	var id pgtype.UUID
	if err := row.Scan(&id, &u.Name, &u.IsAdmin, &u.CreateTime); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, store.ErrNotFound
		}
		return User{}, err
	}
	u.ID = store.UUIDToString(id)
	return u, nil
}

func (r *repository) nameTaken(ctx context.Context, name string) (bool, error) {
	var taken bool
	err := r.db.QueryRow(ctx, "select exists (select 1 from quiz.users where lower(name) = lower($1))", name).Scan(&taken)
	return taken, err
}

func (r *repository) createChallenge(ctx context.Context, c *challenge, expiry time.Time) (pgtype.UUID, error) {
	var id pgtype.UUID
	// Expired challenges are deleted along the way.
	err := r.db.QueryRow(ctx, `with expired as (delete from quiz.challenges where expire_time < now())
		insert into quiz.challenges (ceremony, challenge, user_handle, user_name, expire_time)
		values ($1, $2, $3, $4, $5) returning id`,
		c.Ceremony, c.Challenge, c.UserHandle, c.UserName, expiry).Scan(&id)
	return id, err
}

// takeChallenge deletes the unexpired challenge and returns it, so that it
// can only be used once.
func (r *repository) takeChallenge(ctx context.Context, id pgtype.UUID, ceremony string) (*challenge, error) {
	c := &challenge{}
	err := r.db.QueryRow(ctx, `delete from quiz.challenges where id = $1 and ceremony = $2 and expire_time > now()
		returning ceremony, challenge, user_handle, user_name`, id, ceremony).
		Scan(&c.Ceremony, &c.Challenge, &c.UserHandle, &c.UserName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return c, err
}

// register creates a player with their first passkey. The first player
// becomes the quiz master.
func (r *repository) register(ctx context.Context, name string, userHandle []byte, pk *passkey) (User, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", registrationLockID); err != nil {
		return User{}, fmt.Errorf("failed to lock registrations: %w", err)
	}
	u, err := scanUser(tx.QueryRow(ctx, `insert into quiz.users (name, is_admin, user_handle)
		values ($1, not exists (select 1 from quiz.users), $2)
		returning id, name, is_admin, create_time`, name, userHandle))
	if err != nil {
		if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "users_lower_name" {
			return User{}, errNameTaken
		}
		return User{}, fmt.Errorf("failed to create user: %w", err)
	}
	if _, err := tx.Exec(ctx, `insert into quiz.passkeys (id, user_id, public_key, algorithm, sign_count)
		values ($1, $2, $3, $4, $5)`, pk.ID, u.ID, pk.PublicKey, pk.Algorithm, pk.SignCount); err != nil {
		return User{}, fmt.Errorf("failed to create passkey: %w", err)
	}
	return u, tx.Commit(ctx)
}

func (r *repository) getPasskey(ctx context.Context, id []byte) (*passkey, error) {
	pk := &passkey{}
	err := r.db.QueryRow(ctx, `select p.id, p.user_id, u.user_handle, p.public_key, p.algorithm, p.sign_count
		from quiz.passkeys p join quiz.users u on u.id = p.user_id where p.id = $1`, id).
		Scan(&pk.ID, &pk.UserID, &pk.UserHandle, &pk.PublicKey, &pk.Algorithm, &pk.SignCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return pk, err
}

func (r *repository) usePasskey(ctx context.Context, id []byte, signCount uint32) error {
	_, err := r.db.Exec(ctx, "update quiz.passkeys set sign_count = $2 where id = $1", id, int64(signCount))
	return err
}

// createSession creates a session for the player and returns its token.
func (r *repository) createSession(ctx context.Context, userID string, token string, expiry time.Time) error {
	// Expired sessions are deleted along the way.
	_, err := r.db.Exec(ctx, `with expired as (delete from quiz.sessions where expire_time < now())
		insert into quiz.sessions (token_hash, user_id, expire_time) values ($1, $2, $3)`,
		hashToken(token), userID, expiry)
	return err
}

func (r *repository) sessionUser(ctx context.Context, token string) (User, error) {
	return scanUser(r.db.QueryRow(ctx, `select u.id, u.name, u.is_admin, u.create_time
		from quiz.sessions s join quiz.users u on u.id = s.user_id
		where s.token_hash = $1 and s.expire_time > now()`, hashToken(token)))
}

func (r *repository) deleteSession(ctx context.Context, token string) error {
	_, err := r.db.Exec(ctx, "delete from quiz.sessions where token_hash = $1", hashToken(token))
	return err
}

func (r *repository) revealAnswers(ctx context.Context) (bool, error) {
	var reveal bool
	err := r.db.QueryRow(ctx, "select reveal_answers from quiz.settings").Scan(&reveal)
	return reveal, err
}

func (r *repository) setRevealAnswers(ctx context.Context, reveal bool) error {
	_, err := r.db.Exec(ctx, "update quiz.settings set reveal_answers = $1", reveal)
	return err
}

// questions lists the questions, of a tier or, if tier is empty, of all
// tiers, in order.
func (r *repository) questions(ctx context.Context, tier Tier) ([]Question, error) {
	rows, err := r.db.Query(ctx, `select id, tier, position, prompt, choices, answer_index from quiz.questions
		where $1 = '' or tier = $1 order by array_position(array['easy', 'medium', 'hard'], tier), position, create_time`, string(tier))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Question, error) {
		var q Question
		var id pgtype.UUID
		err := row.Scan(&id, &q.Tier, &q.Position, &q.Prompt, &q.Choices, &q.AnswerIndex)
		q.ID = store.UUIDToString(id)
		return q, err
	})
}

// createQuestion adds a question at the end of its tier.
func (r *repository) createQuestion(ctx context.Context, q Question) (Question, error) {
	var id pgtype.UUID
	err := r.db.QueryRow(ctx, `insert into quiz.questions (tier, position, prompt, choices, answer_index)
		values ($1, (select coalesce(max(position) + 1, 0) from quiz.questions where tier = $1), $2, $3, $4)
		returning id, position`, q.Tier, q.Prompt, q.Choices, q.AnswerIndex).Scan(&id, &q.Position)
	q.ID = store.UUIDToString(id)
	return q, err
}

func (r *repository) updateQuestion(ctx context.Context, q Question) error {
	// A question moved to another tier goes to the end of it.
	tag, err := r.db.Exec(ctx, `update quiz.questions set tier = $2,
		  position = case when tier = $2 then $3 else (select coalesce(max(position) + 1, 0) from quiz.questions where tier = $2) end,
		  prompt = $4, choices = $5, answer_index = $6, update_time = now()
		where id = $1`, q.ID, q.Tier, q.Position, q.Prompt, q.Choices, q.AnswerIndex)
	if err == nil && tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return err
}

func (r *repository) deleteQuestion(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx, "delete from quiz.questions where id = $1", id)
	if err == nil && tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return err
}

// submit records the player's answers to a tier, by question ID. Questions
// left out count as wrong.
func (r *repository) submit(ctx context.Context, userID string, tier Tier, answers map[string]int) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	tag, err := tx.Exec(ctx, "insert into quiz.submissions (user_id, tier) values ($1, $2) on conflict do nothing", userID, tier)
	if err != nil {
		return fmt.Errorf("failed to create submission: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errAlreadySubmitted
	}
	ids := slices.Collect(maps.Keys(answers))
	choices := make([]int32, len(ids))
	for i, id := range ids {
		choices[i] = int32(answers[id])
	}
	if _, err := tx.Exec(ctx, `insert into quiz.answers (user_id, question_id, choice_index)
		select $1, q, c from unnest($2::uuid[], $3::int[]) as a (q, c)
		on conflict (user_id, question_id) do update set choice_index = excluded.choice_index`,
		userID, ids, choices); err != nil {
		return fmt.Errorf("failed to save answers: %w", err)
	}
	return tx.Commit(ctx)
}

// answers returns the player's answers to a tier's questions, by question ID.
func (r *repository) answers(ctx context.Context, userID string, tier Tier) (map[string]int, error) {
	rows, err := r.db.Query(ctx, `select a.question_id, a.choice_index from quiz.answers a
		join quiz.questions q on q.id = a.question_id where a.user_id = $1 and q.tier = $2`, userID, tier)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	answers := map[string]int{}
	for rows.Next() {
		var id pgtype.UUID
		var choice int
		if err := rows.Scan(&id, &choice); err != nil {
			return nil, err
		}
		answers[store.UUIDToString(id)] = choice
	}
	return answers, rows.Err()
}

// submissions lists the submitted tiers, of a player or, if userID is empty,
// of everyone, with how many of their questions were answered correctly.
func (r *repository) submissions(ctx context.Context, userID string) ([]submission, error) {
	rows, err := r.db.Query(ctx, `select s.user_id, s.tier, s.submit_time,
		  count(a.question_id) filter (where a.choice_index = q.answer_index)
		from quiz.submissions s
		left join quiz.questions q on q.tier = s.tier
		left join quiz.answers a on a.user_id = s.user_id and a.question_id = q.id
		where $1 = '' or s.user_id::text = $1
		group by s.user_id, s.tier, s.submit_time`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (submission, error) {
		var s submission
		err := row.Scan(&s.UserID, &s.Tier, &s.SubmitTime, &s.Correct)
		return s, err
	})
}

// questionCounts returns the number of questions of each tier.
func (r *repository) questionCounts(ctx context.Context) (map[Tier]int, error) {
	rows, err := r.db.Query(ctx, "select tier, count(*) from quiz.questions group by tier")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[Tier]int{}
	for rows.Next() {
		var t Tier
		var n int
		if err := rows.Scan(&t, &n); err != nil {
			return nil, err
		}
		counts[t] = n
	}
	return counts, rows.Err()
}

func (r *repository) users(ctx context.Context) ([]User, error) {
	rows, err := r.db.Query(ctx, "select id, name, is_admin, create_time from quiz.users order by create_time")
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (User, error) { return scanUser(row) })
}

func (r *repository) getUser(ctx context.Context, id string) (User, error) {
	return scanUser(r.db.QueryRow(ctx, "select id, name, is_admin, create_time from quiz.users where id = $1", id))
}

func (r *repository) updateUser(ctx context.Context, id, name string, isAdmin bool) error {
	tag, err := r.db.Exec(ctx, "update quiz.users set name = $2, is_admin = $3 where id = $1", id, name, isAdmin)
	if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return errNameTaken
	}
	if err == nil && tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return err
}

func (r *repository) deleteUser(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx, "delete from quiz.users where id = $1", id)
	if err == nil && tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return err
}

// resetSubmission deletes the player's submission of a tier and its
// answers, so they can take the quiz again.
func (r *repository) resetSubmission(ctx context.Context, userID string, tier Tier) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	tag, err := tx.Exec(ctx, "delete from quiz.submissions where user_id = $1 and tier = $2", userID, tier)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	if _, err := tx.Exec(ctx, `delete from quiz.answers a using quiz.questions q
		where a.question_id = q.id and a.user_id = $1 and q.tier = $2`, userID, tier); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
