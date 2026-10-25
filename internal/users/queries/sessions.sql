-- CreateSession creates a session, and deletes the expired ones in the same
-- round trip, as nothing else does.
-- name: CreateSession :exec
with expired as (
  delete from sessions where expiry <= now()
)
insert into sessions (user_id, token_sha256, expiry, passkey_id) values ($1, $2, $3, $4);

-- name: GetSessionUser :one
select p.*, u.is_admin, u.avatar_key, s.expiry from sessions s
join passkey_users p on p.id = s.user_id
join users u on u.id = s.user_id
where s.token_sha256 = $1 and s.expiry > now();

-- name: DeleteSession :exec
delete from sessions where token_sha256 = $1;

-- DeleteOtherSessions deletes a user's sessions other than the one with the
-- token hash.
-- name: DeleteOtherSessions :execrows
delete from sessions
where user_id = $1 and token_sha256 <> sqlc.arg(keep_token_sha256)::bytea;

-- DeletePasskeySessions deletes the sessions a passkey started, and those
-- that don't record their passkey, other than the one with the token hash.
-- name: DeletePasskeySessions :exec
delete from sessions
where user_id = $1 and (passkey_id = sqlc.arg(passkey_id)::bytea or passkey_id is null)
  and token_sha256 <> sqlc.arg(keep_token_sha256)::bytea;
