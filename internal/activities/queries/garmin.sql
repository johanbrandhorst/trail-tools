-- name: UpsertGarminConnection :one
insert into garmin_connections (
  user_id,
  garmin_user_id,
  access_token,
  refresh_token,
  expiry,
  permissions
) values (
  $1, $2, $3, $4, $5, $6
)
on conflict (user_id) do update set
  garmin_user_id = excluded.garmin_user_id,
  access_token = excluded.access_token,
  refresh_token = excluded.refresh_token,
  expiry = excluded.expiry,
  permissions = excluded.permissions,
  sync_message = '',
  update_time = now()
returning *;

-- name: GetGarminConnection :one
select * from garmin_connections
where user_id = $1;

-- name: GetGarminConnectionForUpdate :one
select * from garmin_connections
where user_id = $1
for update;

-- name: GetGarminConnectionByGarminUser :one
select * from garmin_connections
where garmin_user_id = $1;

-- name: ListGarminConnections :many
select * from garmin_connections
order by workouts_sync_time nulls first;

-- name: UpdateGarminTokens :exec
update garmin_connections set
  access_token = $2,
  refresh_token = $3,
  expiry = $4,
  update_time = now()
where user_id = $1;

-- name: UpdateGarminPermissions :exec
update garmin_connections set
  permissions = $2,
  update_time = now()
where user_id = $1;

-- name: FinishGarminWorkoutsSync :exec
update garmin_connections set
  workouts_sync_time = $2,
  sync_message = $3,
  update_time = now()
where user_id = $1;

-- name: SetGarminSyncMessage :exec
update garmin_connections set
  sync_message = $2,
  update_time = now()
where user_id = $1;

-- name: DeleteGarminConnection :execrows
delete from garmin_connections
where user_id = $1;

-- name: GetGarminActivity :one
select id, strava_id, data_source, streams_synced, garmin_samples, sport_type from strava_activities
where user_id = $1 and garmin_activity_id = $2;

-- name: AttachGarminToStravaActivity :one
-- Gives an activity from Strava its Garmin ID when Garmin's copy of it
-- arrives, so it's one activity: the one Strava says it got from Garmin,
-- or else the one that started within two minutes of it.
update strava_activities set
  garmin_activity_id = sqlc.arg(garmin_activity_id)::bigint,
  update_time = now()
where id = (
  select c.id from strava_activities c
  where c.user_id = sqlc.arg(user_id)::uuid and c.garmin_activity_id is null
    and (
      c.strava_external_id in ('garmin_ping_' || sqlc.arg(garmin_activity_id)::bigint, 'garmin_push_' || sqlc.arg(garmin_activity_id)::bigint)
      or abs(extract(epoch from c.start_time - sqlc.arg(start_time)::timestamptz)) < 120
    )
  order by
    c.strava_external_id in ('garmin_ping_' || sqlc.arg(garmin_activity_id)::bigint, 'garmin_push_' || sqlc.arg(garmin_activity_id)::bigint) desc,
    abs(extract(epoch from c.start_time - sqlc.arg(start_time)::timestamptz))
  limit 1
)
returning id, strava_id, data_source, streams_synced, garmin_samples, sport_type;

-- name: InsertGarminActivity :one
insert into strava_activities (
  user_id,
  garmin_activity_id,
  data_source,
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
  device_name,
  relative_effort
) values (
  sqlc.arg(user_id), sqlc.arg(garmin_activity_id), 'garmin', sqlc.arg(name), sqlc.arg(sport_type), sqlc.arg(start_time),
  sqlc.arg(start_date_local), '', sqlc.arg(distance_meters), sqlc.arg(moving_time_seconds), sqlc.arg(elapsed_time_seconds),
  sqlc.arg(elevation_gain_meters), sqlc.arg(average_speed_mps), sqlc.arg(max_speed_mps), sqlc.arg(average_heartrate_bpm),
  sqlc.arg(max_heartrate_bpm), sqlc.arg(average_cadence_spm), '', sqlc.arg(device_name), 0
)
returning id, strava_id, data_source, streams_synced, garmin_samples, sport_type;

-- name: UpdateGarminActivity :exec
-- Updates an activity from Garmin's summary: its stats if they're Garmin's,
-- and its name and sport if it isn't on Strava, whose names win.
update strava_activities set
  name = case when strava_id is null then sqlc.arg(name) else name end,
  sport_type = case when strava_id is null then sqlc.arg(sport_type) else sport_type end,
  distance_meters = case when data_source = 'garmin' then sqlc.arg(distance_meters) else distance_meters end,
  moving_time_seconds = case when data_source = 'garmin' then sqlc.arg(moving_time_seconds) else moving_time_seconds end,
  elapsed_time_seconds = case when data_source = 'garmin' then sqlc.arg(elapsed_time_seconds) else elapsed_time_seconds end,
  elevation_gain_meters = case when data_source = 'garmin' then sqlc.arg(elevation_gain_meters) else elevation_gain_meters end,
  average_speed_mps = case when data_source = 'garmin' then sqlc.arg(average_speed_mps) else average_speed_mps end,
  max_speed_mps = case when data_source = 'garmin' then sqlc.arg(max_speed_mps) else max_speed_mps end,
  average_heartrate_bpm = case when data_source = 'garmin' then sqlc.arg(average_heartrate_bpm) else average_heartrate_bpm end,
  max_heartrate_bpm = case when data_source = 'garmin' then sqlc.arg(max_heartrate_bpm) else max_heartrate_bpm end,
  average_cadence_spm = case when data_source = 'garmin' then sqlc.arg(average_cadence_spm) else average_cadence_spm end,
  device_name = case when device_name = '' then sqlc.arg(device_name) else device_name end,
  update_time = now()
where id = sqlc.arg(id);

-- name: StoreGarminSamples :exec
-- Marks an activity's samples as Garmin's, from its FIT file.
update strava_activities set
  data_source = 'garmin',
  garmin_samples = true,
  summary_polyline = case when sqlc.arg(summary_polyline)::text <> '' then sqlc.arg(summary_polyline)::text else summary_polyline end,
  min_lat = coalesce(sqlc.narg(min_lat)::double precision, min_lat),
  min_lng = coalesce(sqlc.narg(min_lng)::double precision, min_lng),
  max_lat = coalesce(sqlc.narg(max_lat)::double precision, max_lat),
  max_lng = coalesce(sqlc.narg(max_lng)::double precision, max_lng),
  update_time = now()
where id = sqlc.arg(id);

-- name: DeleteGarminActivity :one
-- Deletes an activity deleted on Garmin, unless it's on Strava too: then
-- it only loses its Garmin ID. Returns the activity's ID and whether it was
-- deleted.
with detached as (
  update strava_activities set
    garmin_activity_id = null,
    update_time = now()
  where user_id = sqlc.arg(user_id)::uuid and garmin_activity_id = sqlc.arg(garmin_activity_id)::bigint and strava_id is not null
  returning id
), deleted as (
  delete from strava_activities
  where user_id = sqlc.arg(user_id)::uuid and garmin_activity_id = sqlc.arg(garmin_activity_id)::bigint and strava_id is null
  returning id
)
select
  coalesce((select id from deleted), (select id from detached))::uuid as id,
  (select count(*) from deleted) > 0 as deleted;

-- name: UpsertGarminActivityFile :exec
insert into garmin_activity_files (
  user_id,
  garmin_activity_id,
  callback_url,
  expire_time
) values (
  $1, $2, $3, $4
)
on conflict (user_id, garmin_activity_id) do update set
  callback_url = excluded.callback_url,
  expire_time = excluded.expire_time,
  attempts = 0;

-- name: ListGarminActivityFiles :many
-- Lists the files to download, of all users or one, leaving out expired
-- links and those that failed too often.
select * from garmin_activity_files
where (sqlc.narg(user_id)::uuid is null or user_id = sqlc.narg(user_id)::uuid)
  and expire_time > now() and attempts < sqlc.arg(max_attempts)
order by create_time
limit sqlc.arg(row_limit);

-- name: DeleteGarminActivityFile :exec
delete from garmin_activity_files
where user_id = $1 and garmin_activity_id = $2;

-- name: RecordGarminActivityFileFailure :exec
update garmin_activity_files set
  attempts = attempts + 1
where user_id = $1 and garmin_activity_id = $2;

-- name: DeleteExpiredGarminActivityFiles :exec
delete from garmin_activity_files
where expire_time <= now() or attempts >= sqlc.arg(max_attempts);

-- name: CountGarminStatus :one
-- Counts the user's activity files waiting to download and workouts on
-- their Garmin calendar, in one round trip.
select
  (select count(*) from garmin_activity_files f
    where f.user_id = sqlc.arg(user_id)::uuid and f.expire_time > now() and f.attempts < sqlc.arg(max_attempts))::bigint as pending_files,
  (select count(*) from garmin_workouts w where w.user_id = sqlc.arg(user_id)::uuid)::bigint as workouts;
