-- Used authorization codes are kept until they expire, with the grant they
-- were exchanged for. A code used twice was stolen, so the grant is revoked
-- (RFC 6749 section 4.1.2): either the thief or the client holds its tokens.
alter table oauth_authorization_codes
  add column consume_time timestamptz,
  add column oauth_grant_id uuid references oauth_grants(id) on delete set null;

-- Refresh tokens that were rotated out, until they would have expired. A
-- rotated-out refresh token used again was stolen, so its grant is revoked
-- (RFC 9700 section 4.14.2): either the thief or the client holds the current
-- refresh token.
create table oauth_used_refresh_tokens (
  refresh_token_sha256 bytea primary key,
  oauth_grant_id uuid not null references oauth_grants(id) on delete cascade,
  expire_time timestamptz not null
);

create index oauth_used_refresh_tokens_oauth_grant_id on oauth_used_refresh_tokens (oauth_grant_id);
