-- name: CreateTrainingPlan :one
insert into training_plans (
  user_id,
  name,
  description,
  start_date,
  end_date,
  goal_race_ids
) values (
  $1, $2, $3, $4, $5, $6
)
returning *;

-- name: GetTrainingPlan :one
select * from training_plans
where id = $1 and user_id = $2;

-- name: ListTrainingPlans :many
select * from training_plans
where user_id = $1
order by start_date desc;

-- name: UpdateTrainingPlan :one
update training_plans set
  name = $3,
  description = $4,
  start_date = $5,
  end_date = $6,
  goal_race_ids = $7,
  update_time = now()
where id = $1 and user_id = $2
returning *;

-- name: DeleteTrainingPlan :execrows
delete from training_plans
where id = $1 and user_id = $2;

-- name: UpsertPlanPeriod :one
insert into plan_periods (
  plan_id,
  granularity,
  start_date,
  title,
  notes,
  target_distance_meters,
  target_elevation_gain_meters,
  target_duration_seconds
) values (
  $1, $2, $3, $4, $5, $6, $7, $8
)
on conflict (plan_id, granularity, start_date) do update set
  title = excluded.title,
  notes = excluded.notes,
  target_distance_meters = excluded.target_distance_meters,
  target_elevation_gain_meters = excluded.target_elevation_gain_meters,
  target_duration_seconds = excluded.target_duration_seconds
returning *;

-- name: ListPlanPeriods :many
select * from plan_periods
where plan_id = $1
order by start_date, granularity;

-- name: DeletePlanPeriods :execrows
delete from plan_periods
where plan_id = $1 and id = any(sqlc.arg(ids)::uuid[]);

-- name: CreateWorkouts :many
insert into workouts (
  id,
  plan_id,
  date,
  type,
  title,
  description,
  target_distance_meters,
  target_elevation_gain_meters,
  target_duration_seconds,
  target_terrain,
  route_id,
  completed_activity_id
)
select
  w.id,
  sqlc.arg(plan_id)::uuid,
  w.date,
  w.type::workout_type,
  w.title,
  w.description,
  w.target_distance_meters,
  w.target_elevation_gain_meters,
  w.target_duration_seconds,
  nullif(w.target_terrain, '')::terrain,
  w.route_id,
  w.completed_activity_id
from (
  select
    unnest(sqlc.arg(ids)::uuid[]) as id,
    unnest(sqlc.arg(dates)::date[]) as date,
    unnest(sqlc.arg(types)::text[]) as type,
    unnest(sqlc.arg(titles)::text[]) as title,
    unnest(sqlc.arg(descriptions)::text[]) as description,
    unnest(sqlc.arg(target_distances_meters)::double precision[]) as target_distance_meters,
    unnest(sqlc.arg(target_elevation_gains_meters)::double precision[]) as target_elevation_gain_meters,
    unnest(sqlc.arg(target_durations_seconds)::integer[]) as target_duration_seconds,
    -- Empty if the workout has no target terrain.
    unnest(sqlc.arg(target_terrains)::text[]) as target_terrain,
    unnest(sqlc.arg(route_ids)::uuid[]) as route_id,
    unnest(sqlc.arg(completed_activity_ids)::uuid[]) as completed_activity_id
) w
returning *;

-- name: ListWorkouts :many
select * from workouts
where plan_id = $1
  and (sqlc.narg(start_date)::date is null or date >= sqlc.narg(start_date)::date)
  and (sqlc.narg(end_date)::date is null or date <= sqlc.narg(end_date)::date)
order by date, create_time;

-- name: ListUserWorkouts :many
select w.* from workouts w
join training_plans p on p.id = w.plan_id
where p.user_id = $1
  and (sqlc.narg(start_date)::date is null or w.date >= sqlc.narg(start_date)::date)
  and (sqlc.narg(end_date)::date is null or w.date <= sqlc.narg(end_date)::date)
order by w.date, w.create_time;

-- name: AddRacesToCalendar :exec
update races set
  on_calendar = true,
  update_time = now()
where user_id = $1 and id = any(sqlc.arg(ids)::uuid[]) and not on_calendar;

-- name: GetWorkout :one
select w.* from workouts w
join training_plans p on p.id = w.plan_id
where w.id = $1 and p.user_id = $2;

-- name: UpdateWorkout :one
update workouts set
  date = $2,
  type = $3,
  title = $4,
  description = $5,
  target_distance_meters = $6,
  target_elevation_gain_meters = $7,
  target_duration_seconds = $8,
  target_terrain = $9,
  route_id = $10,
  completed_activity_id = $11
where id = $1
returning *;

-- name: DeleteWorkouts :execrows
delete from workouts
where plan_id = $1 and id = any(sqlc.arg(ids)::uuid[]);

-- name: FindReferences :one
-- Finds which of the routes and activities exist and belong to the user.
select
  array(
    select id from routes
    where user_id = sqlc.arg(user_id)::uuid and id = any(sqlc.arg(route_ids)::uuid[])
  )::uuid[] as route_ids,
  array(
    select id from strava_activities
    where user_id = sqlc.arg(user_id)::uuid and id = any(sqlc.arg(activity_ids)::uuid[])
  )::uuid[] as activity_ids;

-- name: ListDatesOutsidePlan :many
-- Lists the workouts and periods of a plan outside the given dates, as the
-- kind ('workout', 'week' or 'month') and the date of each. Periods are
-- outside if none of their days are within the dates.
select 'workout'::text as kind, date from workouts
where plan_id = sqlc.arg(plan_id)::uuid
  and (date < sqlc.arg(start_date)::date or date > sqlc.arg(end_date)::date)
union all
select granularity::text as kind, start_date as date from plan_periods
where plan_id = sqlc.arg(plan_id)::uuid
  and (
    start_date > sqlc.arg(end_date)::date
    or case granularity
      when 'week' then start_date + 6
      else (start_date + interval '1 month' - interval '1 day')::date
    end < sqlc.arg(start_date)::date
  )
order by date, kind;

-- name: CountRaces :one
select count(*) from races
where user_id = $1 and id = any(sqlc.arg(ids)::uuid[]);
