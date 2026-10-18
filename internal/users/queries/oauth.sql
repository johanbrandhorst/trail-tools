-- name: CreateOAuthAuthorizationCode :exec
insert into oauth_authorization_codes (
  code_sha256,
  user_id,
  client_id,
  client_name,
  redirect_uri,
  code_challenge,
  expire_time
) values (
  $1,
  $2,
  $3,
  $4,
  $5,
  $6,
  $7
);

-- name: DeleteExpiredOAuthAuthorizationCodes :exec
delete from oauth_authorization_codes
where expire_time <= now();

-- ConsumeOAuthAuthorizationCode marks the code as used, so it can only be used
-- once.
-- name: ConsumeOAuthAuthorizationCode :one
update oauth_authorization_codes
set consume_time = now()
where code_sha256 = $1 and expire_time > now() and consume_time is null
returning *;

-- DeleteUsedOAuthAuthorizationCode deletes a code that was used already,
-- returning the grant it was exchanged for, if it was.
-- name: DeleteUsedOAuthAuthorizationCode :one
delete from oauth_authorization_codes
where code_sha256 = $1 and consume_time is not null
returning user_id, oauth_grant_id;

-- SetOAuthAuthorizationCodeGrant records the grant a code was exchanged for.
-- It updates no rows if the code was used again meanwhile, and deleted.
-- name: SetOAuthAuthorizationCodeGrant :execrows
update oauth_authorization_codes
set oauth_grant_id = $2
where code_sha256 = $1;

-- name: CreateOAuthGrant :one
insert into oauth_grants (
  user_id,
  client_id,
  client_name,
  redirect_uri,
  refresh_token_sha256,
  refresh_expire_time
) values (
  $1,
  $2,
  $3,
  $4,
  $5,
  $6
)
returning *;

-- name: DeleteExpiredOAuthGrants :exec
delete from oauth_grants
where user_id = $1 and refresh_expire_time <= now();

-- name: GetOAuthGrantByRefreshToken :one
select * from oauth_grants
where refresh_token_sha256 = $1 and refresh_expire_time > now()
for update;

-- name: RotateOAuthRefreshToken :exec
update oauth_grants
set
  refresh_token_sha256 = $2,
  refresh_expire_time = $3
where id = $1;

-- name: CreateOAuthAccessToken :one
insert into api_tokens (
  user_id,
  name,
  secret_sha256,
  oauth_grant_id,
  expire_time
) values (
  $1,
  $2,
  $3,
  $4,
  $5
)
returning *;

-- name: DeleteOAuthAccessTokens :exec
delete from api_tokens
where oauth_grant_id = $1;

-- name: CreateOAuthUsedRefreshToken :exec
insert into oauth_used_refresh_tokens (
  refresh_token_sha256,
  oauth_grant_id,
  expire_time
) values (
  $1,
  $2,
  $3
);

-- name: DeleteExpiredOAuthUsedRefreshTokens :exec
delete from oauth_used_refresh_tokens
where oauth_grant_id = $1 and expire_time <= now();

-- DeleteOAuthGrantByUsedRefreshToken revokes the grant of a refresh token that
-- was rotated out.
-- name: DeleteOAuthGrantByUsedRefreshToken :execrows
delete from oauth_grants
where id = (
  select oauth_used_refresh_tokens.oauth_grant_id from oauth_used_refresh_tokens
  where oauth_used_refresh_tokens.refresh_token_sha256 = $1
);

-- name: ListOAuthGrants :many
select
  oauth_grants.id,
  oauth_grants.client_name,
  oauth_grants.redirect_uri,
  oauth_grants.create_time,
  max(api_tokens.last_used_time)::timestamptz as last_used_time
from oauth_grants
left join api_tokens on api_tokens.oauth_grant_id = oauth_grants.id
where oauth_grants.user_id = $1 and oauth_grants.refresh_expire_time > now()
group by oauth_grants.id
order by oauth_grants.create_time desc;

-- name: DeleteOAuthGrant :execrows
delete from oauth_grants
where id = $1 and user_id = $2;
