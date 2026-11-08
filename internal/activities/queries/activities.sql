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
  photo_count,
  strava_external_id
) values (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24
)
on conflict (user_id, strava_id) do update set
  -- Strava's name and sport, which users edit there, win; the stats of an
  -- activity from Garmin are the watch's own, and kept.
  name = excluded.name,
  sport_type = excluded.sport_type,
  start_time = excluded.start_time,
  start_date_local = excluded.start_date_local,
  timezone = excluded.timezone,
  distance_meters = case when strava_activities.data_source = 'garmin' then strava_activities.distance_meters else excluded.distance_meters end,
  moving_time_seconds = case when strava_activities.data_source = 'garmin' then strava_activities.moving_time_seconds else excluded.moving_time_seconds end,
  elapsed_time_seconds = case when strava_activities.data_source = 'garmin' then strava_activities.elapsed_time_seconds else excluded.elapsed_time_seconds end,
  elevation_gain_meters = case when strava_activities.data_source = 'garmin' then strava_activities.elevation_gain_meters else excluded.elevation_gain_meters end,
  average_speed_mps = case when strava_activities.data_source = 'garmin' then strava_activities.average_speed_mps else excluded.average_speed_mps end,
  max_speed_mps = case when strava_activities.data_source = 'garmin' then strava_activities.max_speed_mps else excluded.max_speed_mps end,
  average_heartrate_bpm = case when strava_activities.data_source = 'garmin' then strava_activities.average_heartrate_bpm else excluded.average_heartrate_bpm end,
  max_heartrate_bpm = case when strava_activities.data_source = 'garmin' then strava_activities.max_heartrate_bpm else excluded.max_heartrate_bpm end,
  average_cadence_spm = case when strava_activities.data_source = 'garmin' then strava_activities.average_cadence_spm else excluded.average_cadence_spm end,
  summary_polyline = case when strava_activities.data_source = 'garmin' and strava_activities.summary_polyline <> '' then strava_activities.summary_polyline else excluded.summary_polyline end,
  relative_effort = excluded.relative_effort,
  strava_external_id = excluded.strava_external_id,
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
returning id, (data_source = 'garmin' and garmin_samples)::boolean as has_garmin_samples;

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
select a.id, a.strava_id::bigint as strava_id, (a.data_source = 'garmin' and a.garmin_samples)::boolean as has_garmin_samples from strava_activities a
where a.user_id = sqlc.arg(user_id) and a.strava_id is not null and not a.streams_synced and a.sync_failures < sqlc.arg(max_failures)
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

-- name: SetStravaSamples :exec
-- Marks an activity's samples as Strava's, once they're stored.
update strava_activities set
  data_source = 'strava',
  garmin_samples = false
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
  id, coalesce(strava_id, 0)::bigint as strava_id, coalesce(garmin_activity_id, 0)::bigint as garmin_activity_id, data_source, name, description, sport_type, start_time, start_date_local, timezone,
  distance_meters, moving_time_seconds, elapsed_time_seconds, elevation_gain_meters,
  average_speed_mps, max_speed_mps, average_heartrate_bpm, max_heartrate_bpm,
  average_cadence_spm, summary_polyline, device_name, relative_effort, streams_synced, garmin_samples,
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
  id, coalesce(strava_id, 0)::bigint as strava_id, coalesce(garmin_activity_id, 0)::bigint as garmin_activity_id, data_source, name, description, sport_type, start_time, start_date_local, timezone,
  distance_meters, moving_time_seconds, elapsed_time_seconds, elevation_gain_meters,
  average_speed_mps, max_speed_mps, average_heartrate_bpm, max_heartrate_bpm,
  average_cadence_spm, summary_polyline, device_name, relative_effort, streams_synced, garmin_samples,
  coalesce(synced_photo_count, photo_count)::integer as photo_count
from strava_activities
where id = $1 and user_id = $2;

-- name: ListActivityTotals :many
select id, start_date_local, distance_meters, moving_time_seconds, elevation_gain_meters,
  (streams_synced or garmin_samples)::boolean as has_streams
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

-- name: LinkWorkouts :exec
-- Links the user's workouts that have no activity to the activities of the
-- same day that no workout has: runs and races to runs, strength workouts
-- to strength training, ski tours to ski touring and other cross-training
-- to anything else. On a day without a planned ski tour, ski touring
-- activities complete other cross-training, as a ski tour may have been
-- planned as that before ski tours could be. With several
-- of a kind on a day, they're paired in order, the workouts by their start
-- time and the activities by theirs, so the morning run completes the
-- morning workout without comparing times across time zones. Workouts whose
-- link the user removed are left alone.
with workout_kinds as (
  select w.id, w.date, k.kind,
    row_number() over (partition by w.date, k.kind order by w.start_time nulls last, w.id) as n
  from workouts w
  join training_plans p on p.id = w.plan_id
  cross join lateral (select case
    when w.type = 'strength' then 'strength'
    when w.type in ('base', 'intensity', 'race') then 'run'
    when w.cross_training_sport = 'ski_touring' then 'ski_touring'
    else 'cross_training'
  end as kind) k
  where p.user_id = sqlc.arg(user_id)::uuid
    and w.type <> 'rest'
    and w.completed_activity_id is null
    and not w.link_dismissed
),
activity_kinds as (
  select a.id, a.start_date_local as date, k.kind,
    row_number() over (partition by a.start_date_local, k.kind order by a.start_time, a.id) as n
  from strava_activities a
  cross join lateral (select case
    when a.sport_type = any(sqlc.arg(strength_sport_types)::text[]) then 'strength'
    when a.sport_type = any(sqlc.arg(run_sport_types)::text[]) then 'run'
    when a.sport_type = any(sqlc.arg(unplanned_sport_types)::text[]) then null
    when a.sport_type = any(sqlc.arg(ski_touring_sport_types)::text[])
      and a.start_date_local in (select date from workout_kinds where kind = 'ski_touring') then 'ski_touring'
    else 'cross_training'
  end as kind) k
  where a.user_id = sqlc.arg(user_id)::uuid
    and k.kind is not null
    and a.start_date_local in (select date from workout_kinds)
    and not exists (select 1 from workouts o where o.completed_activity_id = a.id)
)
update workouts w set
  completed_activity_id = a.id
from workout_kinds wk
join activity_kinds a on a.date = wk.date and a.kind = wk.kind and a.n = wk.n
where w.id = wk.id;

-- name: AttachStravaToGarminActivity :exec
-- Gives an activity from Garmin its Strava ID when Strava's copy of it
-- arrives, so it's one activity: the one Strava says it got from Garmin,
-- or else the one that started within two minutes of it. Its details sync
-- from Strava again, for its description and photos, keeping Garmin's
-- samples.
update strava_activities set
  strava_id = sqlc.arg(strava_id)::bigint,
  streams_synced = false,
  update_time = now()
where id = (
  select c.id from strava_activities c
  where c.user_id = sqlc.arg(user_id)::uuid and c.strava_id is null and c.garmin_activity_id is not null
    and (
      c.garmin_activity_id::text = substring(sqlc.arg(external_id)::text from '^garmin_(?:ping|push)_([0-9]+)')
      or abs(extract(epoch from c.start_time - sqlc.arg(start_time)::timestamptz)) < 120
    )
  order by
    c.garmin_activity_id::text = coalesce(substring(sqlc.arg(external_id)::text from '^garmin_(?:ping|push)_([0-9]+)'), '') desc,
    abs(extract(epoch from c.start_time - sqlc.arg(start_time)::timestamptz))
  limit 1
)
and not exists (
  select 1 from strava_activities s
  where s.user_id = sqlc.arg(user_id)::uuid and s.strava_id = sqlc.arg(strava_id)::bigint
);
