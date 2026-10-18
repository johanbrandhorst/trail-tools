-- name: ListPrivacyZones :many
select z.*, u.privacy_seed from privacy_zones z
join users u on u.id = z.user_id
where z.user_id = $1
order by z.create_time, z.id;

-- name: DeletePrivacyZones :exec
delete from privacy_zones
where user_id = $1;

-- name: InsertPrivacyZones :exec
insert into privacy_zones (user_id, name, lat, lng, radius_meters)
select
  sqlc.arg(user_id)::uuid,
  unnest(sqlc.arg(names)::text[]),
  unnest(sqlc.arg(lats)::double precision[]),
  unnest(sqlc.arg(lngs)::double precision[]),
  unnest(sqlc.arg(radii_meters)::integer[]);
