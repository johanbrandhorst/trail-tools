-- The passkey a session was signed in with, so deleting a passkey ends the
-- sessions it started. It's null for sessions started before this was
-- recorded, which deleting any of the user's passkeys ends too, and for the
-- session a passkey was deleted from, which is kept.
alter table sessions add column passkey_id bytea references passkeys(id) on delete set null;

-- Sessions are found by their user to sign out the user's other sessions.
create index sessions_user_id on sessions (user_id);
