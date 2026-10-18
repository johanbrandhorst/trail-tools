-- name: LockRaceReport :one
-- Gets the ID of one of the user's reports, locking it until the end of the
-- transaction, so that photos are added to it one at a time.
select id from race_reports
where id = $1 and user_id = $2
for update;

-- name: ListRaceReportPhotos :many
select * from race_report_photos
where report_id = $1
order by position, create_time;

-- name: ShiftRaceReportPhotos :exec
-- Makes room for a photo at a position.
update race_report_photos set position = position + 1
where report_id = $1 and position >= $2;

-- name: AddRaceReportPhoto :one
insert into race_report_photos (
  report_id,
  position,
  object_key,
  width,
  height,
  caption,
  lat,
  lng,
  location_source,
  track_distance_meters,
  taken_time
) values (
  sqlc.arg(report_id),
  -- At the position if set, or else last.
  coalesce(
    sqlc.narg(position)::integer,
    (select coalesce(max(position) + 1, 0) from race_report_photos where report_id = sqlc.arg(report_id))
  ),
  sqlc.arg(object_key),
  sqlc.arg(width),
  sqlc.arg(height),
  sqlc.arg(caption),
  sqlc.narg(lat),
  sqlc.narg(lng),
  sqlc.narg(location_source),
  sqlc.narg(track_distance_meters),
  sqlc.narg(taken_time)
)
returning *;

-- name: GetRaceReportPhoto :one
select p.* from race_report_photos p
join race_reports r on r.id = p.report_id
where p.id = $1 and r.user_id = $2;

-- name: GetRaceReportPhotoAccess :one
-- Gets a photo with what's needed to decide who can see it.
select p.object_key, p.create_time, r.user_id, r.published from race_report_photos p
join race_reports r on r.id = p.report_id
where p.id = $1;

-- name: UpdateRaceReportPhotoCaption :one
update race_report_photos p set
  caption = $3
from race_reports r
where r.id = p.report_id and p.id = $1 and r.user_id = $2
returning p.*;

-- name: UpdateRaceReportPhotoLocation :one
update race_report_photos p set
  lat = sqlc.narg(lat),
  lng = sqlc.narg(lng),
  location_source = sqlc.narg(location_source),
  track_distance_meters = sqlc.narg(track_distance_meters)
from race_reports r
where r.id = p.report_id and p.id = sqlc.arg(id) and r.user_id = sqlc.arg(user_id)
returning p.*;

-- name: DeleteRaceReportPhoto :one
delete from race_report_photos p
using race_reports r
where r.id = p.report_id and p.id = $1 and r.user_id = $2
returning p.object_key;

-- name: MoveRaceReportPhoto :exec
-- Sets the location of a photo of a report, after the report's track
-- changed.
update race_report_photos set
  lat = sqlc.narg(lat),
  lng = sqlc.narg(lng),
  location_source = sqlc.narg(location_source),
  track_distance_meters = sqlc.narg(track_distance_meters)
where id = sqlc.arg(id) and report_id = sqlc.arg(report_id);
