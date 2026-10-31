package quiz

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// schemaLockID is the advisory lock that serializes creating the schema, so
// two server revisions starting side by side during a deploy don't race to
// create the same tables. It is "quiz" in ASCII.
const schemaLockID = 0x7175697a

// schema creates the quiz's tables in their own Postgres schema, rather than
// in the migrations, so that removing the quiz after the party leaves no
// trace in the migration history: drop the schema with `drop schema quiz
// cascade` and delete this package. Every statement is idempotent, as it
// runs on every start.
const schema = `
create schema if not exists quiz;

-- Players of the quiz. They're separate from the app's users, so that guests
-- of the party don't get accounts on the app. The first player becomes the
-- quiz master (is_admin), who edits questions and manages players.
create table if not exists quiz.users (
  id uuid primary key default gen_random_uuid(),
  name text not null,
  is_admin boolean not null default false,
  -- The WebAuthn user handle of the player's passkeys.
  user_handle bytea not null unique,
  create_time timestamptz not null default current_timestamp
);
-- Names are unique, so the leaderboard tells players apart, and a player
-- joining again on another device is told to sign in instead.
create unique index if not exists users_lower_name on quiz.users (lower(name));

create table if not exists quiz.passkeys (
  -- The credential ID.
  id bytea primary key,
  user_id uuid not null references quiz.users (id) on delete cascade,
  -- The PKIX encoded public key.
  public_key bytea not null,
  algorithm integer not null,
  sign_count bigint not null default 0,
  create_time timestamptz not null default current_timestamp
);

-- Pending WebAuthn ceremonies, used once.
create table if not exists quiz.challenges (
  id uuid primary key default gen_random_uuid(),
  ceremony text not null check (ceremony in ('registration', 'login')),
  challenge bytea not null,
  -- The new player's handle and name, for registrations.
  user_handle bytea,
  user_name text not null default '',
  expire_time timestamptz not null
);

create table if not exists quiz.sessions (
  -- The SHA-256 hash of the session cookie's token, so a leaked database
  -- doesn't leak sessions.
  token_hash bytea primary key,
  user_id uuid not null references quiz.users (id) on delete cascade,
  expire_time timestamptz not null
);

create table if not exists quiz.questions (
  id uuid primary key default gen_random_uuid(),
  tier text not null check (tier in ('easy', 'medium', 'hard')),
  -- The order of the question within its tier.
  position integer not null default 0,
  prompt text not null,
  choices text[] not null check (cardinality(choices) between 2 and 6),
  -- The index of the correct choice, from 0.
  answer_index integer not null check (answer_index >= 0 and answer_index < cardinality(choices)),
  create_time timestamptz not null default current_timestamp,
  update_time timestamptz not null default current_timestamp
);
create index if not exists questions_tier_position on quiz.questions (tier, position);

-- The players' answers. Scores are computed from them when they're shown,
-- so correcting a question's answer corrects everyone's scores.
create table if not exists quiz.answers (
  user_id uuid not null references quiz.users (id) on delete cascade,
  question_id uuid not null references quiz.questions (id) on delete cascade,
  choice_index integer not null,
  primary key (user_id, question_id)
);

-- Submitted quizzes. Each player submits each tier once, unless the quiz
-- master resets it.
create table if not exists quiz.submissions (
  user_id uuid not null references quiz.users (id) on delete cascade,
  tier text not null check (tier in ('easy', 'medium', 'hard')),
  submit_time timestamptz not null default current_timestamp,
  primary key (user_id, tier)
);

-- The quiz's settings, in a single row.
create table if not exists quiz.settings (
  id boolean primary key default true check (id),
  -- Whether players see the correct answers of the quizzes they submitted.
  reveal_answers boolean not null default false
);
`

// migrate creates the schema, and seeds the questions the first time.
func migrate(ctx context.Context, db pgxBeginner) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin: %w", err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", schemaLockID); err != nil {
		return fmt.Errorf("failed to lock the schema: %w", err)
	}
	if _, err := tx.Exec(ctx, schema); err != nil {
		return fmt.Errorf("failed to create the schema: %w", err)
	}
	// The settings row is created once, which tells whether to seed the
	// questions: seeding them on every start would bring back the questions
	// the quiz master deleted.
	tag, err := tx.Exec(ctx, "insert into quiz.settings default values on conflict do nothing")
	if err != nil {
		return fmt.Errorf("failed to create the settings: %w", err)
	}
	if tag.RowsAffected() == 1 {
		b := &pgx.Batch{}
		for _, q := range seedQuestions {
			b.Queue("insert into quiz.questions (tier, position, prompt, choices, answer_index) values ($1, $2, $3, $4, $5)",
				q.Tier, q.Position, q.Prompt, q.Choices, q.AnswerIndex)
		}
		if err := tx.SendBatch(ctx, b).Close(); err != nil {
			return fmt.Errorf("failed to seed the questions: %w", err)
		}
	}
	return tx.Commit(ctx)
}
