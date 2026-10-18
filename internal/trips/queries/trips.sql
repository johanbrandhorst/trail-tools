-- name: GetProfile :one
select user_id, handle, display_name from public_profiles
where user_id = $1;

-- name: GetProfileByHandle :one
select user_id, handle, display_name from public_profiles
where handle = $1;

-- name: UpsertProfile :one
insert into public_profiles (
  user_id,
  handle,
  display_name
) values (
  $1, $2, $3
)
on conflict (user_id) do update set
  handle = excluded.handle,
  display_name = excluded.display_name,
  update_time = now()
returning user_id, handle, display_name;

-- name: GetUserName :one
select name from passkey_users
where id = $1;

-- name: CreateReport :one
insert into trip_reports (
  user_id,
  activity_id,
  title,
  slug,
  date,
  timezone,
  sport_type,
  start_time,
  distance_meters,
  moving_time_seconds,
  elapsed_time_seconds,
  elevation_gain_meters,
  track
) values (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
)
returning *;

-- name: GetReport :one
select * from trip_reports
where id = $1 and user_id = $2;

-- name: GetReportBySlug :one
select * from trip_reports
where user_id = $1 and slug = $2;

-- name: ListReports :many
select
  id, user_id, activity_id, title, slug, date, timezone, sport_type, start_time,
  distance_meters, moving_time_seconds, elapsed_time_seconds, elevation_gain_meters,
  (track is not null)::boolean as has_track, cover_photo_id, tags, published, publish_time,
  create_time, update_time
from trip_reports
where user_id = $1
  and (sqlc.narg(activity_id)::uuid is null or activity_id = sqlc.narg(activity_id)::uuid)
  and (not sqlc.arg(published_only)::boolean or published)
order by date desc, create_time desc;

-- name: UpdateReport :one
update trip_reports set
  title = coalesce(sqlc.narg(title), title),
  slug = coalesce(sqlc.narg(slug), slug),
  date = coalesce(sqlc.narg(date), date),
  cover_photo_id = case
    when sqlc.arg(set_cover_photo)::boolean then sqlc.narg(cover_photo_id)::uuid
    else cover_photo_id
  end,
  tags = coalesce(sqlc.narg(tags)::text[], tags),
  update_time = now()
where id = sqlc.arg(id) and user_id = sqlc.arg(user_id)
returning *;

-- name: SetReportPublished :one
update trip_reports set
  published = sqlc.arg(published),
  publish_time = case when sqlc.arg(published) then coalesce(publish_time, now()) else publish_time end,
  update_time = now()
where id = sqlc.arg(id) and user_id = sqlc.arg(user_id)
returning *;

-- name: ListPhotoObjectKeys :many
select object_key from trip_photos
where report_id = $1;

-- name: DeleteReport :execrows
delete from trip_reports
where id = $1 and user_id = $2;

-- name: ReportExists :one
select exists (select 1 from trip_reports where id = $1 and user_id = $2);

-- name: ShiftPhotos :exec
-- Makes room for a photo at a position in a section.
update trip_photos set position = position + 1
where section_id = $1 and position >= $2;

-- name: AddPhoto :one
insert into trip_photos (
  report_id,
  section_id,
  position,
  object_key,
  width,
  height,
  caption,
  lat,
  lng,
  location_source,
  track_distance_meters,
  taken_time,
  strava_photo_id
) values (
  sqlc.arg(report_id),
  sqlc.arg(section_id),
  -- At the position if set, or else last.
  coalesce(
    sqlc.narg(position)::integer,
    (select coalesce(max(position) + 1, 0) from trip_photos where report_id = sqlc.arg(report_id))
  ),
  sqlc.arg(object_key),
  sqlc.arg(width),
  sqlc.arg(height),
  sqlc.arg(caption),
  sqlc.narg(lat),
  sqlc.narg(lng),
  sqlc.narg(location_source),
  sqlc.narg(track_distance_meters),
  sqlc.narg(taken_time),
  sqlc.narg(strava_photo_id)
)
returning *;

-- name: ListPhotos :many
-- Lists photos in the order of the story: by section, then by position.
select p.* from trip_photos p
join trip_report_sections s on s.id = p.section_id
where p.report_id = any(sqlc.arg(report_ids)::uuid[])
order by p.report_id, s.position, p.position, p.create_time;

-- name: GetPhoto :one
select p.* from trip_photos p
join trip_reports r on r.id = p.report_id
where p.id = $1 and r.user_id = $2;

-- name: GetPhotoAccess :one
-- Gets a photo with what's needed to decide who can see it.
select p.object_key, p.create_time, r.user_id, r.published from trip_photos p
join trip_reports r on r.id = p.report_id
where p.id = $1;

-- name: UpdatePhotoCaption :one
update trip_photos p set
  caption = $3
from trip_reports r
where r.id = p.report_id and p.id = $1 and r.user_id = $2
returning p.*;

-- name: UpdatePhotoLocation :one
update trip_photos p set
  lat = sqlc.narg(lat),
  lng = sqlc.narg(lng),
  location_source = sqlc.narg(location_source),
  track_distance_meters = sqlc.narg(track_distance_meters)
from trip_reports r
where r.id = p.report_id and p.id = sqlc.arg(id) and r.user_id = sqlc.arg(user_id)
returning p.*;

-- name: SetPhotoSections :execrows
-- Moves photos to sections, numbering them in the order given.
update trip_photos p set
  section_id = (sqlc.arg(section_ids)::uuid[])[o.position],
  position = o.position - 1
from unnest(sqlc.arg(photo_ids)::uuid[]) with ordinality as o(id, position)
where p.id = o.id and p.report_id = sqlc.arg(report_id);

-- name: DeletePhoto :one
delete from trip_photos p
using trip_reports r
where r.id = p.report_id and p.id = $1 and r.user_id = $2
returning p.object_key;

-- name: TouchReport :exec
update trip_reports set
  update_time = now()
where id = $1;

-- name: LockReport :one
-- Gets the ID of one of the user's reports, locking it until the end of the
-- transaction.
select id from trip_reports
where id = $1 and user_id = $2
for update;

-- name: CreateSection :one
insert into trip_report_sections (
  report_id,
  position,
  title,
  body,
  start_distance_meters,
  end_distance_meters
) values (
  $1, $2, $3, $4, $5, $6
)
returning *;

-- name: UpdateSection :execrows
update trip_report_sections set
  position = $3,
  title = $4,
  body = $5,
  start_distance_meters = $6,
  end_distance_meters = $7,
  update_time = case
    when (title, body, start_distance_meters, end_distance_meters)
      is distinct from ($4::text, $5::text, $6::double precision, $7::double precision)
    then now() else update_time end
where id = $1 and report_id = $2;

-- name: ListSections :many
select * from trip_report_sections
where report_id = any(sqlc.arg(report_ids)::uuid[])
order by report_id, position;

-- name: MovePhotosOutOfSections :exec
-- Moves the photos of a report's sections that aren't kept to the end of a
-- section, in the order of the story.
with moved as (
  select p.id, row_number() over (order by s.position, p.position, p.create_time) as n
  from trip_photos p
  join trip_report_sections s on s.id = p.section_id
  where p.report_id = sqlc.arg(report_id) and not (p.section_id = any(sqlc.arg(kept_ids)::uuid[]))
)
update trip_photos p set
  section_id = sqlc.arg(section_id),
  position = (
    select coalesce(max(position) + 1, 0) from trip_photos where section_id = sqlc.arg(section_id)
  ) + moved.n - 1
from moved
where p.id = moved.id;

-- name: DeleteSectionsExcept :exec
delete from trip_report_sections
where report_id = sqlc.arg(report_id) and not (id = any(sqlc.arg(kept_ids)::uuid[]));

-- name: ListExcerpts :many
-- Lists the text of the first section with text of each report.
select distinct on (report_id) report_id, body from trip_report_sections
where report_id = any(sqlc.arg(report_ids)::uuid[]) and body <> ''
order by report_id, position;

-- name: CreatePhotoUploadLink :one
-- Creates a link to upload photos to a report with, and deletes the user's
-- expired links.
with expired as (
  delete from photo_upload_links
  where user_id = sqlc.arg(user_id) and expire_time <= now()
)
insert into photo_upload_links (user_id, token_sha256, trip_report_id, race_report_id, expire_time)
values (sqlc.arg(user_id), sqlc.arg(token_sha256), sqlc.narg(trip_report_id), sqlc.narg(race_report_id), sqlc.arg(expire_time))
returning *;

-- name: GetPhotoUploadLinkByToken :one
select * from photo_upload_links
where token_sha256 = $1 and expire_time > now();
