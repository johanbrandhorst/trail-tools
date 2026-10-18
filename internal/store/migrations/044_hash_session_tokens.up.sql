-- Sessions are looked up by the SHA-256 hash of their token, like
-- invitations and API tokens, so the database doesn't hold tokens that sign
-- users in. The tokens of existing sessions are their IDs as text, so their
-- hashes are computed from those, and the sessions get new IDs.
--
-- Expiry is no longer checked on every write: sessions expire while stored,
-- so the check would reject updates of expired ones. Expired sessions are
-- deleted instead, here and whenever a session is created.
alter table sessions drop constraint sessions_expiry_check;

delete from sessions where expiry <= now();

alter table sessions add column token_sha256 bytea;

update sessions set
  token_sha256 = sha256(convert_to(id::text, 'UTF8')),
  id = gen_random_uuid();

alter table sessions alter column token_sha256 set not null;

alter table sessions add constraint sessions_token_sha256_key unique (token_sha256);
