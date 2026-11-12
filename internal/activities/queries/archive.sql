-- name: DeleteStravaArchiveActivities :exec
delete from strava_archive_activities
where user_id = $1;

-- name: InsertStravaArchiveActivities :exec
-- Stages the activities of an archive, but those already imported from an
-- archive.
insert into strava_archive_activities (
  user_id, strava_id, name, description, sport_type, start_time, elapsed_time_seconds, moving_time_seconds,
  distance_meters, elevation_gain_meters, average_speed_mps, max_speed_mps, average_heartrate_bpm,
  max_heartrate_bpm, average_cadence_spm, relative_effort, filename
)
select sqlc.arg(user_id)::uuid, r.*
from (
  select
    unnest(sqlc.arg(strava_ids)::bigint[]) as strava_id,
    unnest(sqlc.arg(names)::text[]) as name,
    unnest(sqlc.arg(descriptions)::text[]) as description,
    unnest(sqlc.arg(sport_types)::text[]) as sport_type,
    unnest(sqlc.arg(start_times)::timestamptz[]) as start_time,
    unnest(sqlc.arg(elapsed_time_seconds)::integer[]) as elapsed_time_seconds,
    unnest(sqlc.arg(moving_time_seconds)::integer[]) as moving_time_seconds,
    unnest(sqlc.arg(distance_meters)::double precision[]) as distance_meters,
    unnest(sqlc.arg(elevation_gain_meters)::double precision[]) as elevation_gain_meters,
    unnest(sqlc.arg(average_speed_mps)::double precision[]) as average_speed_mps,
    unnest(sqlc.arg(max_speed_mps)::double precision[]) as max_speed_mps,
    unnest(sqlc.arg(average_heartrate_bpm)::double precision[]) as average_heartrate_bpm,
    unnest(sqlc.arg(max_heartrate_bpm)::double precision[]) as max_heartrate_bpm,
    unnest(sqlc.arg(average_cadence_spm)::double precision[]) as average_cadence_spm,
    unnest(sqlc.arg(relative_efforts)::integer[]) as relative_effort,
    unnest(sqlc.arg(filenames)::text[]) as filename
) r
where not exists (
  select from strava_activities a
  where a.user_id = sqlc.arg(user_id)::uuid and a.strava_id = r.strava_id and a.from_strava_archive
)
on conflict (user_id, strava_id) do nothing;

-- name: ListStravaArchiveActivities :many
select strava_id, name, filename from strava_archive_activities
where user_id = $1
order by start_time desc;

-- name: GetStravaArchiveActivity :one
select * from strava_archive_activities
where user_id = $1 and strava_id = $2;

-- name: DeleteStravaArchiveActivity :exec
delete from strava_archive_activities
where user_id = $1 and strava_id = $2;

-- name: DeleteStaleStravaArchiveActivities :exec
delete from strava_archive_activities
where create_time < $1;

-- name: UpsertArchiveActivity :one
-- Creates or updates an activity from the user's Strava archive. An
-- activity already synced from Strava keeps its sport type, local date and
-- time zone, which Strava's API gives more precisely than the archive, and
-- the stats of an activity from Garmin are the watch's own, and kept.
insert into strava_activities (
  user_id, strava_id, name, description, sport_type, start_time, start_date_local, timezone, distance_meters,
  moving_time_seconds, elapsed_time_seconds, elevation_gain_meters, average_speed_mps, max_speed_mps,
  average_heartrate_bpm, max_heartrate_bpm, average_cadence_spm, summary_polyline, relative_effort,
  from_strava_archive
) values (
  $1, $2, $3, $4, $5, $6, $7, '', $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, true
)
on conflict (user_id, strava_id) do update set
  name = excluded.name,
  description = excluded.description,
  distance_meters = case when strava_activities.data_source = 'garmin' then strava_activities.distance_meters else excluded.distance_meters end,
  moving_time_seconds = case when strava_activities.data_source = 'garmin' then strava_activities.moving_time_seconds else excluded.moving_time_seconds end,
  elapsed_time_seconds = case when strava_activities.data_source = 'garmin' then strava_activities.elapsed_time_seconds else excluded.elapsed_time_seconds end,
  elevation_gain_meters = case when strava_activities.data_source = 'garmin' then strava_activities.elevation_gain_meters else excluded.elevation_gain_meters end,
  average_speed_mps = case when strava_activities.data_source = 'garmin' then strava_activities.average_speed_mps else excluded.average_speed_mps end,
  max_speed_mps = case when strava_activities.data_source = 'garmin' then strava_activities.max_speed_mps else excluded.max_speed_mps end,
  average_heartrate_bpm = case when strava_activities.data_source = 'garmin' then strava_activities.average_heartrate_bpm else excluded.average_heartrate_bpm end,
  max_heartrate_bpm = case when strava_activities.data_source = 'garmin' then strava_activities.max_heartrate_bpm else excluded.max_heartrate_bpm end,
  average_cadence_spm = case when strava_activities.data_source = 'garmin' then strava_activities.average_cadence_spm else excluded.average_cadence_spm end,
  summary_polyline = case when strava_activities.summary_polyline <> '' then strava_activities.summary_polyline else excluded.summary_polyline end,
  relative_effort = excluded.relative_effort,
  from_strava_archive = true,
  update_time = now()
returning id, (data_source = 'garmin' and garmin_samples)::boolean as has_garmin_samples;
