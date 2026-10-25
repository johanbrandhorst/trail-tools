-- name: CreatePasskeyUser :one
insert into passkey_users (user_handle, name) values ($1, $2) returning *;

-- name: GetPasskeyUser :one
select p.*, u.is_admin, u.avatar_key from passkey_users p
join users u on u.id = p.id
where p.id = $1;

-- name: GetPasskeyUserByHandle :one
select p.*, u.is_admin, u.avatar_key from passkey_users p
join users u on u.id = p.id
where p.user_handle = $1;

-- name: SetAdmin :exec
update users set is_admin = $2 where id = $1;

-- name: HasAdmin :one
select exists(select 1 from users where is_admin);

-- name: GetFirstAdmin :one
select id from users where is_admin order by create_time, id limit 1;

-- name: CreatePasskey :one
insert into passkeys (
  id,
  user_id,
  name,
  public_key,
  algorithm,
  aaguid,
  transports,
  backup_eligible,
  backed_up,
  sign_count
) values (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
)
returning *;

-- name: GetPasskey :one
select * from passkeys where id = $1;

-- name: ListPasskeys :many
select * from passkeys where user_id = $1 order by create_time;

-- name: ListPasskeyIDs :many
select id from passkeys where user_id = $1;

-- name: UsePasskey :exec
update passkeys set
  sign_count = $2,
  backed_up = $3,
  last_used_time = now()
where id = $1;

-- name: DeletePasskey :execrows
delete from passkeys where id = $1 and user_id = $2;

-- name: CountPasskeys :one
select count(*) from passkeys where user_id = $1;

-- name: CreatePasskeyChallenge :one
insert into passkey_challenges (
  ceremony,
  challenge,
  user_handle,
  invitation_id,
  user_id,
  bootstrap,
  expire_time
) values (
  $1, $2, $3, $4, $5, $6, $7
)
returning id;

-- name: TakePasskeyChallenge :one
delete from passkey_challenges
where id = $1 and ceremony = $2 and expire_time > now()
returning *;

-- name: DeleteExpiredPasskeyChallenges :exec
delete from passkey_challenges where expire_time <= now();

-- name: CreateInvitation :one
insert into invitations (
  token_sha256,
  kind,
  created_by,
  user_id,
  note,
  expire_time
) values (
  $1, $2, $3, $4, $5, $6
)
returning *;

-- name: GetInvitationByToken :one
select i.*, coalesce(p.name, '')::text as created_by_name, coalesce(t.name, '')::text as user_name
from invitations i
left join passkey_users p on p.id = i.created_by
left join passkey_users t on t.id = i.user_id
where i.token_sha256 = $1;

-- name: GetInvitation :one
select * from invitations where id = $1;

-- name: UseInvitation :execrows
update invitations set use_time = now()
where id = $1 and use_time is null and expire_time > now();

-- name: ListInvitations :many
select i.*, coalesce(p.name, '')::text as created_by_name, coalesce(t.name, '')::text as user_name
from invitations i
left join passkey_users p on p.id = i.created_by
left join passkey_users t on t.id = i.user_id
where i.created_by = $1
order by i.create_time desc;

-- name: DeleteInvitation :execrows
delete from invitations where id = $1 and created_by = $2;
