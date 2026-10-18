-- name: UpsertActivity :one
insert into strava_activities (
  user_id,
  strava_id,
  name,
  sport_type,
  start_time,
  start_date_local,
  timezone,
  distance_meters,
  moving_time_seconds,
  elapsed_time_seconds,
  elevation_gain_meters,
  average_speed_mps,
  max_speed_mps,
  average_heartrate_bpm,
  max_heartrate_bpm,
  average_cadence_spm,
  summary_polyline,
  relative_effort,
  min_lat,
  min_lng,
  max_lat,
  max_lng,
  photo_count
) values (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23
)
on conflict (user_id, strava_id) do update set
  name = excluded.name,
  sport_type = excluded.sport_type,
  start_time = excluded.start_time,
  start_date_local = excluded.start_date_local,
  timezone = excluded.timezone,
  distance_meters = excluded.distance_meters,
  moving_time_seconds = excluded.moving_time_seconds,
  elapsed_time_seconds = excluded.elapsed_time_seconds,
  elevation_gain_meters = excluded.elevation_gain_meters,
  average_speed_mps = excluded.average_speed_mps,
  max_speed_mps = excluded.max_speed_mps,
  average_heartrate_bpm = excluded.average_heartrate_bpm,
  max_heartrate_bpm = excluded.max_heartrate_bpm,
  average_cadence_spm = excluded.average_cadence_spm,
  summary_polyline = excluded.summary_polyline,
  relative_effort = excluded.relative_effort,
  min_lat = coalesce(strava_activities.min_lat, excluded.min_lat),
  min_lng = coalesce(strava_activities.min_lng, excluded.min_lng),
  max_lat = coalesce(strava_activities.max_lat, excluded.max_lat),
  max_lng = coalesce(strava_activities.max_lng, excluded.max_lng),
  photo_count = excluded.photo_count,
  -- Photos are synced with the details, so sync them again when photos
  -- are added or removed on Strava.
  streams_synced = strava_activities.streams_synced and strava_activities.photo_count = excluded.photo_count,
  -- An activity that changed may sync now, so it's no longer skipped.
  sync_failures = case
    when strava_activities.photo_count = excluded.photo_count then strava_activities.sync_failures
    else 0
  end,
  update_time = now()
returning id;

-- name: GetLatestActivityPolyline :one
select summary_polyline from strava_activities
where user_id = $1 and summary_polyline <> ''
order by start_date_local desc, start_time desc
limit 1;

-- name: GetLatestActivityStartTime :one
select max(start_time)::timestamptz from strava_activities
where user_id = $1;

-- name: ListActivitiesWithoutStreams :many
-- Activities that started more than recent_window before the latest one are
-- only listed when they're requested, and those that failed to sync
-- max_failures times are left out.
select a.id, a.strava_id from strava_activities a
where a.user_id = sqlc.arg(user_id) and not a.streams_synced and a.sync_failures < sqlc.arg(max_failures)
  and (a.sync_requested or a.start_time >= (
    select max(latest.start_time) from strava_activities latest where latest.user_id = sqlc.arg(user_id)
  ) - sqlc.arg(recent_window)::interval)
order by a.sync_requested desc, a.sync_failures, a.start_time desc
limit sqlc.arg(row_limit);

-- name: RequestActivitySync :exec
update strava_activities set
  sync_requested = true,
  sync_failures = 0
where id = $1 and user_id = $2 and not streams_synced;

-- name: RecordActivitySyncFailure :exec
update strava_activities set
  sync_failures = sync_failures + 1
where id = $1 and user_id = $2;

-- name: CountActivities :one
select
  count(*) as total,
  count(*) filter (
    where not a.streams_synced and a.sync_failures < sqlc.arg(max_failures) and (a.sync_requested or a.start_time >= (
      select max(latest.start_time) from strava_activities latest where latest.user_id = sqlc.arg(user_id)
    ) - sqlc.arg(recent_window)::interval)
  ) as pending_streams,
  count(*) filter (where not a.streams_synced and a.sync_failures >= sqlc.arg(max_failures)) as failed_streams
from strava_activities a
where a.user_id = sqlc.arg(user_id);

-- name: SetActivityDetail :exec
update strava_activities set
  description = $2,
  device_name = $3,
  synced_photo_count = $4,
  update_time = now()
where id = $1;

-- name: MarkActivityStreamsSynced :exec
update strava_activities set
  streams_synced = true,
  sync_requested = false,
  sync_failures = 0,
  min_lat = coalesce(sqlc.narg(min_lat)::double precision, strava_activities.min_lat),
  min_lng = coalesce(sqlc.narg(min_lng)::double precision, strava_activities.min_lng),
  max_lat = coalesce(sqlc.narg(max_lat)::double precision, strava_activities.max_lat),
  max_lng = coalesce(sqlc.narg(max_lng)::double precision, strava_activities.max_lng),
  update_time = now()
where id = sqlc.arg(id);

-- name: UpsertActivityStreams :exec
-- The streams are stored in the blob store, and the row records that the
-- activity has them, with its track.
insert into strava_activity_streams (
  activity_id,
  track_polyline
) values (
  $1,
  sqlc.arg(track_polyline)::text
)
on conflict (activity_id) do update set
  track_polyline = excluded.track_polyline;

-- name: ListActivities :many
select
  id, strava_id, name, description, sport_type, start_time, start_date_local, timezone,
  distance_meters, moving_time_seconds, elapsed_time_seconds, elevation_gain_meters,
  average_speed_mps, max_speed_mps, average_heartrate_bpm, max_heartrate_bpm,
  average_cadence_spm, summary_polyline, device_name, relative_effort, streams_synced,
  coalesce(synced_photo_count, photo_count)::integer as photo_count
from strava_activities
where user_id = $1
  and (sqlc.narg(start_date)::date is null or start_date_local >= sqlc.narg(start_date)::date)
  and (sqlc.narg(end_date)::date is null or start_date_local <= sqlc.narg(end_date)::date)
  and (cardinality(sqlc.arg(sport_types)::text[]) = 0 or sport_type = any(sqlc.arg(sport_types)::text[]))
order by
  case when sqlc.arg(sort)::text = 'distance' then distance_meters end desc nulls last,
  case when sqlc.arg(sort)::text = 'elevation_gain' then elevation_gain_meters end desc nulls last,
  case when sqlc.arg(sort)::text = 'moving_time' then moving_time_seconds end desc nulls last,
  start_time desc, id
limit $2 offset $3;

-- name: GetActivity :one
select
  id, strava_id, name, description, sport_type, start_time, start_date_local, timezone,
  distance_meters, moving_time_seconds, elapsed_time_seconds, elevation_gain_meters,
  average_speed_mps, max_speed_mps, average_heartrate_bpm, max_heartrate_bpm,
  average_cadence_spm, summary_polyline, device_name, relative_effort, streams_synced,
  coalesce(synced_photo_count, photo_count)::integer as photo_count
from strava_activities
where id = $1 and user_id = $2;

-- name: ListActivityTotals :many
select start_date_local, distance_meters, moving_time_seconds, elevation_gain_meters
from strava_activities
where user_id = $1
  and start_date_local >= sqlc.arg(start_date)::date
  and start_date_local <= sqlc.arg(end_date)::date
  and (cardinality(sqlc.arg(sport_types)::text[]) = 0 or sport_type = any(sqlc.arg(sport_types)::text[]))
order by start_date_local;

-- name: ListActivityTracksInBounds :many
select a.id, a.summary_polyline, s.track_polyline
from strava_activities a
left join strava_activity_streams s on s.activity_id = a.id
where a.user_id = $1
  and a.min_lat <= sqlc.arg(north)::double precision
  and a.max_lat >= sqlc.arg(south)::double precision
  and a.min_lng <= sqlc.arg(east)::double precision
  and a.max_lng >= sqlc.arg(west)::double precision
  and (sqlc.narg(start_date)::date is null or a.start_date_local >= sqlc.narg(start_date)::date)
  and (sqlc.narg(end_date)::date is null or a.start_date_local <= sqlc.narg(end_date)::date)
order by a.start_date_local desc, a.id
limit sqlc.arg(max_activities);

-- name: ListActivitySummaryTracksInBounds :many
select id, summary_polyline
from strava_activities
where user_id = $1
  and min_lat <= sqlc.arg(north)::double precision
  and max_lat >= sqlc.arg(south)::double precision
  and min_lng <= sqlc.arg(east)::double precision
  and max_lng >= sqlc.arg(west)::double precision
  and (sqlc.narg(start_date)::date is null or start_date_local >= sqlc.narg(start_date)::date)
  and (sqlc.narg(end_date)::date is null or start_date_local <= sqlc.narg(end_date)::date)
order by start_date_local desc, id
limit sqlc.arg(max_activities);
