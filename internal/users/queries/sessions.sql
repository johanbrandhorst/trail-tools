-- CreateSession creates a session, and deletes the expired ones in the same
-- round trip, as nothing else does.
-- name: CreateSession :exec
with expired as (
  delete from sessions where expiry <= now()
)
insert into sessions (user_id, token_sha256, expiry) values ($1, $2, $3);

-- name: GetSessionUser :one
select p.*, u.is_admin, u.avatar_key from sessions s
join passkey_users p on p.id = s.user_id
join users u on u.id = s.user_id
where s.token_sha256 = $1 and s.expiry > now();

-- name: DeleteSession :exec
delete from sessions where token_sha256 = $1;
