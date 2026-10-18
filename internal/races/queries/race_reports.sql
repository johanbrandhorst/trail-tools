-- name: CreateRaceReport :one
insert into race_reports (
  user_id,
  race_id,
  activity_id,
  title,
  slug,
  race_name,
  race_date,
  distance_meters,
  elevation_gain_meters,
  finish_seconds,
  track
) values (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
)
returning *;

-- name: GetRaceReport :one
select * from race_reports
where id = $1 and user_id = $2;

-- name: GetRaceReportBySlug :one
select * from race_reports
where user_id = $1 and slug = $2;

-- name: ListRaceReports :many
select * from race_reports
where user_id = $1
  and (not sqlc.arg(published_only)::boolean or published)
  and (sqlc.narg(race_id)::uuid is null or race_id = sqlc.narg(race_id)::uuid)
order by race_date desc, create_time desc;

-- name: UpdateRaceReport :one
update race_reports set
  title = coalesce(sqlc.narg(title), title),
  slug = coalesce(sqlc.narg(slug), slug),
  activity_id = case when sqlc.arg(set_activity)::boolean then sqlc.narg(activity_id)::uuid else activity_id end,
  track = case when sqlc.arg(set_activity)::boolean then sqlc.narg(track)::bytea else track end,
  finish_seconds = coalesce(sqlc.narg(finish_seconds), finish_seconds),
  placement = coalesce(sqlc.narg(placement), placement),
  summary = coalesce(sqlc.narg(summary), summary),
  preparation = coalesce(sqlc.narg(preparation), preparation),
  race_day = coalesce(sqlc.narg(race_day), race_day),
  nutrition = coalesce(sqlc.narg(nutrition), nutrition),
  went_well = coalesce(sqlc.narg(went_well), went_well),
  went_badly = coalesce(sqlc.narg(went_badly), went_badly),
  lessons = coalesce(sqlc.narg(lessons), lessons),
  did_not_finish = coalesce(sqlc.narg(did_not_finish), did_not_finish),
  stop_distance_meters = coalesce(sqlc.narg(stop_distance_meters), stop_distance_meters),
  stop_checkpoint = coalesce(sqlc.narg(stop_checkpoint), stop_checkpoint),
  stop_seconds = coalesce(sqlc.narg(stop_seconds), stop_seconds),
  update_time = now()
where id = sqlc.arg(id) and user_id = sqlc.arg(user_id)
returning *;

-- name: SetRaceReportPublished :one
update race_reports set
  published = sqlc.arg(published),
  publish_time = case when sqlc.arg(published) then coalesce(publish_time, now()) else publish_time end,
  update_time = now()
where id = sqlc.arg(id) and user_id = sqlc.arg(user_id)
returning *;

-- name: DeleteRaceReport :many
-- Deletes one of the user's reports, and returns the object keys of its
-- photos: a row for each photo, or one row without a key for a report
-- without photos, and no rows if there's no report. The select sees the
-- photos as they were before the delete.
with deleted as (
  delete from race_reports r
  where r.id = $1 and r.user_id = $2
  returning r.id
)
select p.object_key from deleted d
left join race_report_photos p on p.report_id = d.id;

-- name: SetRaceReportTrack :exec
-- Stores the track of a report made before reports had one.
update race_reports set track = $3
where id = $1 and user_id = $2 and track is null;

-- name: ListRaceReportSegments :many
select * from race_report_segments
where report_id = $1
order by position;

-- name: DeleteRaceReportSegments :exec
delete from race_report_segments
where report_id = $1;

-- name: InsertRaceReportSegments :exec
insert into race_report_segments (report_id, position, title, body, start_distance_meters, end_distance_meters)
select
  sqlc.arg(report_id)::uuid,
  unnest(sqlc.arg(positions)::integer[]),
  unnest(sqlc.arg(titles)::text[]),
  unnest(sqlc.arg(bodies)::text[]),
  unnest(sqlc.arg(start_distances_meters)::double precision[]),
  unnest(sqlc.arg(end_distances_meters)::double precision[]);

-- name: TouchRaceReport :exec
update race_reports set update_time = now()
where id = $1;
