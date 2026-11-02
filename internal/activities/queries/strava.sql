-- name: UpsertStravaConnection :one
insert into strava_connections (
  user_id,
  athlete_id,
  athlete_name,
  access_token,
  refresh_token,
  expiry
) values (
  $1,
  $2,
  $3,
  $4,
  $5,
  $6
)
on conflict (user_id) do update set
  athlete_id = excluded.athlete_id,
  athlete_name = excluded.athlete_name,
  access_token = excluded.access_token,
  refresh_token = excluded.refresh_token,
  expiry = excluded.expiry,
  -- Reconnecting starts afresh, rather than keeping a failed sync waiting
  -- for the reconnection.
  sync_state = 'idle',
  sync_message = '',
  sync_failures = 0,
  resume_time = null,
  sync_state_time = current_timestamp
returning *;

-- name: GetStravaConnection :one
select * from strava_connections
where user_id = $1;

-- name: ListStravaConnections :many
select * from strava_connections
order by sync_start_time nulls first, user_id;

-- name: GetStravaConnectionForUpdate :one
select * from strava_connections
where user_id = $1
for update;

-- name: UpdateStravaTokens :exec
update strava_connections set
  access_token = $2,
  refresh_token = $3,
  expiry = $4
where user_id = $1;

-- name: UpdateStravaSyncState :exec
update strava_connections set
  sync_state = $2,
  sync_message = $3,
  resume_time = $4,
  last_sync_time = coalesce(sqlc.narg(last_sync_time), last_sync_time),
  sync_state_time = current_timestamp
where user_id = $1;

-- name: StartStravaSync :exec
update strava_connections set
  sync_state = 'running',
  sync_message = $2,
  resume_time = null,
  sync_start_time = current_timestamp,
  sync_state_time = current_timestamp
where user_id = $1;

-- name: FinishStravaSync :exec
update strava_connections set
  sync_state = 'idle',
  sync_message = $2,
  last_sync_time = $3,
  sync_failures = 0,
  resume_time = null,
  sync_state_time = current_timestamp
where user_id = $1;

-- name: FailStravaSync :exec
update strava_connections set
  sync_state = 'failed',
  sync_message = $2,
  sync_failures = $3,
  resume_time = $4,
  sync_state_time = current_timestamp
where user_id = $1;

-- name: UpdateStravaListBefore :exec
update strava_connections set
  list_before = $2
where user_id = $1;

-- name: DeleteStravaConnection :execrows
delete from strava_connections
where user_id = $1;

-- name: ListStravaConnectionsByAthlete :many
select * from strava_connections
where athlete_id = $1;

-- name: GetActivityIDByStravaID :one
select id, garmin_activity_id, garmin_samples from strava_activities
where user_id = sqlc.arg(user_id) and strava_id = sqlc.arg(strava_id)::bigint;

-- name: DeleteActivityByStravaID :one
-- Deletes an activity deleted on Strava, unless it's on Garmin too: then
-- it only loses its Strava ID. Returns whether the activity was deleted.
with detached as (
  update strava_activities set
    strava_id = null,
    update_time = now()
  where user_id = sqlc.arg(user_id)::uuid and strava_id = sqlc.arg(strava_id)::bigint and garmin_activity_id is not null
  returning id
), deleted as (
  delete from strava_activities
  where user_id = sqlc.arg(user_id)::uuid and strava_id = sqlc.arg(strava_id)::bigint and garmin_activity_id is null
  returning id
)
select (select count(*) from deleted) > 0 as deleted;
