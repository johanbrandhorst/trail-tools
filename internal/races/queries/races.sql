-- name: CreateRace :one
insert into races (
  user_id,
  name,
  date,
  start_time,
  location,
  website_url,
  distance_meters,
  elevation_gain_meters,
  cutoff_seconds,
  priority,
  course_route_id,
  notes,
  on_calendar,
  event_id
) values (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
)
returning *;

-- name: GetRace :one
select * from races
where id = $1 and user_id = $2;

-- name: ListRaces :many
select * from races
where user_id = $1
  and (not sqlc.arg(on_calendar_only)::boolean or on_calendar)
order by date, name;

-- name: UpdateRace :one
update races set
  name = $3,
  date = $4,
  start_time = $5,
  location = $6,
  website_url = $7,
  distance_meters = $8,
  elevation_gain_meters = $9,
  cutoff_seconds = $10,
  priority = $11,
  course_route_id = $12,
  notes = $13,
  on_calendar = $14,
  event_id = $15,
  update_time = now()
where id = $1 and user_id = $2
returning *;

-- name: DeleteRace :execrows
delete from races
where id = $1 and user_id = $2;

-- name: CreateRaceEvent :one
insert into race_events (
  user_id,
  name,
  location,
  website_url,
  notes
) values (
  $1, $2, $3, $4, $5
)
returning *;

-- name: GetRaceEvent :one
select * from race_events
where id = $1 and user_id = $2;

-- name: ListRaceEvents :many
select * from race_events
where user_id = $1
order by name, id;

-- name: UpdateRaceEvent :one
update race_events set
  name = $3,
  location = $4,
  website_url = $5,
  notes = $6,
  update_time = now()
where id = $1 and user_id = $2
returning *;

-- name: DeleteRaceEvent :execrows
delete from race_events
where id = $1 and user_id = $2;

-- name: RemoveGoalRace :exec
update training_plans set
  goal_race_ids = array_remove(goal_race_ids, sqlc.arg(race_id)::uuid)
where user_id = $1;

-- name: CreateRacePlan :one
insert into race_plans (
  user_id,
  race_id,
  goals_a_finish_seconds,
  goals_b_finish_seconds,
  goals_c_finish_seconds,
  goals_notes,
  pacing_strategy,
  pacing_hike_grade_percent,
  pacing_heart_rate_cap_bpm,
  pacing_notes,
  nutrition_carbs_grams_per_hour,
  nutrition_fluid_ml_per_hour,
  nutrition_sodium_mg_per_hour,
  nutrition_notes,
  crew_has_crew,
  crew_members,
  crew_notes,
  mental_mantras,
  mental_notes,
  notes
) values (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20
)
returning id;

-- name: GetRacePlan :one
select * from race_plans
where id = $1 and user_id = $2;

-- name: GetRacePlanByShareToken :one
select * from race_plans
where share_token = $1;

-- name: ListRacePlans :many
select * from race_plans
where user_id = $1
order by create_time, id;

-- name: TouchRacePlan :execrows
update race_plans set
  update_time = now()
where id = $1 and user_id = $2;

-- name: UpdateRacePlanGoals :exec
update race_plans set
  goals_a_finish_seconds = $2,
  goals_b_finish_seconds = $3,
  goals_c_finish_seconds = $4,
  goals_notes = $5
where id = $1;

-- name: UpdateRacePlanPacing :exec
update race_plans set
  pacing_strategy = $2,
  pacing_hike_grade_percent = $3,
  pacing_heart_rate_cap_bpm = $4,
  pacing_notes = $5
where id = $1;

-- name: UpdateRacePlanNutrition :exec
update race_plans set
  nutrition_carbs_grams_per_hour = $2,
  nutrition_fluid_ml_per_hour = $3,
  nutrition_sodium_mg_per_hour = $4,
  nutrition_notes = $5
where id = $1;

-- name: UpdateRacePlanCrew :exec
update race_plans set
  crew_has_crew = $2,
  crew_members = $3,
  crew_notes = $4
where id = $1;

-- name: UpdateRacePlanMental :exec
update race_plans set
  mental_mantras = $2,
  mental_notes = $3
where id = $1;

-- name: UpdateRacePlanNotes :exec
update race_plans set
  notes = $2
where id = $1;

-- name: ShareRacePlan :one
update race_plans set
  share_token = coalesce(share_token, sqlc.arg(new_token)::text)
where id = $1 and user_id = $2
returning share_token;

-- name: UnshareRacePlan :execrows
update race_plans set
  share_token = null
where id = $1 and user_id = $2;

-- name: DeleteRacePlan :execrows
delete from race_plans
where id = $1 and user_id = $2;

-- name: UpsertRacePlanCheckpoints :exec
insert into race_plan_checkpoints (
  plan_id,
  id,
  position,
  name,
  distance_meters,
  elevation_gain_meters,
  elevation_loss_meters,
  cutoff_seconds,
  crew_access,
  drop_bag,
  aid,
  planned_segment_seconds,
  planned_stop_seconds,
  segment_strategy,
  tasks,
  crew_instructions,
  drop_bag_contents
)
select
  sqlc.arg(plan_id)::uuid,
  unnest(sqlc.arg(ids)::uuid[]),
  unnest(sqlc.arg(positions)::integer[]),
  unnest(sqlc.arg(names)::text[]),
  unnest(sqlc.arg(distances_meters)::double precision[]),
  unnest(sqlc.arg(elevation_gains_meters)::double precision[]),
  unnest(sqlc.arg(elevation_losses_meters)::double precision[]),
  unnest(sqlc.arg(cutoffs_seconds)::integer[]),
  unnest(sqlc.arg(crew_access)::boolean[]),
  unnest(sqlc.arg(drop_bags)::boolean[]),
  unnest(sqlc.arg(aid)::text[]),
  unnest(sqlc.arg(planned_segments_seconds)::integer[]),
  unnest(sqlc.arg(planned_stops_seconds)::integer[]),
  unnest(sqlc.arg(segment_strategies)::text[]),
  unnest(sqlc.arg(tasks)::text[]),
  unnest(sqlc.arg(crew_instructions)::text[]),
  unnest(sqlc.arg(drop_bag_contents)::text[])
on conflict (id) do update set
  position = excluded.position,
  name = excluded.name,
  distance_meters = excluded.distance_meters,
  elevation_gain_meters = excluded.elevation_gain_meters,
  elevation_loss_meters = excluded.elevation_loss_meters,
  cutoff_seconds = excluded.cutoff_seconds,
  crew_access = excluded.crew_access,
  drop_bag = excluded.drop_bag,
  aid = excluded.aid,
  planned_segment_seconds = excluded.planned_segment_seconds,
  planned_stop_seconds = excluded.planned_stop_seconds,
  segment_strategy = excluded.segment_strategy,
  tasks = excluded.tasks,
  crew_instructions = excluded.crew_instructions,
  drop_bag_contents = excluded.drop_bag_contents;

-- name: DeleteOtherRacePlanCheckpoints :exec
delete from race_plan_checkpoints
-- A nil array is null, which would keep every checkpoint.
where plan_id = sqlc.arg(plan_id) and id <> all(coalesce(sqlc.arg(keep_ids)::uuid[], '{}'));

-- name: ListRacePlanCheckpointIDs :many
select id from race_plan_checkpoints
where plan_id = $1;

-- name: ListRacePlanCheckpoints :many
select * from race_plan_checkpoints
where plan_id = any(sqlc.arg(plan_ids)::uuid[])
order by plan_id, position;

-- name: CreateRacePlanIntakes :exec
insert into race_plan_intakes (
  plan_id,
  checkpoint_id,
  position,
  product,
  quantity
)
select
  sqlc.arg(plan_id)::uuid,
  unnest(sqlc.arg(checkpoint_ids)::uuid[]),
  unnest(sqlc.arg(positions)::integer[]),
  unnest(sqlc.arg(products)::text[]),
  unnest(sqlc.arg(quantities)::double precision[]);

-- name: DeleteRacePlanIntakes :exec
delete from race_plan_intakes
where plan_id = $1;

-- name: ListRacePlanIntakes :many
select * from race_plan_intakes
where plan_id = any(sqlc.arg(plan_ids)::uuid[])
order by plan_id, checkpoint_id, position;

-- name: CreateRacePlanNutritionProducts :exec
insert into race_plan_nutrition_products (
  plan_id,
  position,
  name,
  carbs_grams,
  sodium_mg,
  fluid_ml,
  caffeine_mg
)
select
  sqlc.arg(plan_id)::uuid,
  unnest(sqlc.arg(positions)::integer[]),
  unnest(sqlc.arg(names)::text[]),
  unnest(sqlc.arg(carbs_grams)::double precision[]),
  unnest(sqlc.arg(sodium_mg)::double precision[]),
  unnest(sqlc.arg(fluid_ml)::double precision[]),
  unnest(sqlc.arg(caffeine_mg)::double precision[]);

-- name: DeleteRacePlanNutritionProducts :exec
delete from race_plan_nutrition_products
where plan_id = $1;

-- name: ListRacePlanNutritionProducts :many
select * from race_plan_nutrition_products
where plan_id = any(sqlc.arg(plan_ids)::uuid[])
order by plan_id, position;

-- name: CreateRacePlanGear :exec
insert into race_plan_gear (
  plan_id,
  position,
  name,
  mandatory,
  location,
  packed,
  notes
)
select
  sqlc.arg(plan_id)::uuid,
  unnest(sqlc.arg(positions)::integer[]),
  unnest(sqlc.arg(names)::text[]),
  unnest(sqlc.arg(mandatory)::boolean[]),
  unnest(sqlc.arg(locations)::text[]),
  unnest(sqlc.arg(packed)::boolean[]),
  unnest(sqlc.arg(notes)::text[]);

-- name: DeleteRacePlanGear :exec
delete from race_plan_gear
where plan_id = $1;

-- name: ListRacePlanGear :many
select * from race_plan_gear
where plan_id = any(sqlc.arg(plan_ids)::uuid[])
order by plan_id, position;

-- name: CreateRacePlanContingencies :exec
insert into race_plan_contingencies (
  plan_id,
  position,
  scenario,
  trigger,
  response
)
select
  sqlc.arg(plan_id)::uuid,
  unnest(sqlc.arg(positions)::integer[]),
  unnest(sqlc.arg(scenarios)::text[])::contingency_scenario,
  unnest(sqlc.arg(triggers)::text[]),
  unnest(sqlc.arg(responses)::text[]);

-- name: DeleteRacePlanContingencies :exec
delete from race_plan_contingencies
where plan_id = $1;

-- name: ListRacePlanContingencies :many
select * from race_plan_contingencies
where plan_id = any(sqlc.arg(plan_ids)::uuid[])
order by plan_id, position;

-- name: CreateRacePlanRaceMorning :exec
insert into race_plan_race_morning (
  plan_id,
  position,
  time,
  description
)
select
  sqlc.arg(plan_id)::uuid,
  unnest(sqlc.arg(positions)::integer[]),
  unnest(sqlc.arg(times)::text[]),
  unnest(sqlc.arg(descriptions)::text[]);

-- name: DeleteRacePlanRaceMorning :exec
delete from race_plan_race_morning
where plan_id = $1;

-- name: ListRacePlanRaceMorning :many
select * from race_plan_race_morning
where plan_id = any(sqlc.arg(plan_ids)::uuid[])
order by plan_id, position;

-- name: GetRaceByID :one
select * from races
where id = $1;

-- name: GetRacePlanByID :one
select * from race_plans
where id = $1;

-- name: LockRacePlan :one
select id from race_plans
where id = $1
for update;

-- name: SetRaceDayLivetrackURL :exec
update race_plans set
  race_day_livetrack_url = $2
where id = $1;

-- name: SetRaceDayStartTime :exec
update race_plans set
  race_day_start_time = $2
where id = $1;

-- name: UpsertRacePlanSplits :exec
-- The splits are given by checkpoint position. A position without a
-- checkpoint leaves checkpoint_id null, which fails the insert.
insert into race_plan_splits (
  plan_id,
  checkpoint_id,
  arrival_time,
  departure_time
)
select
  sqlc.arg(plan_id)::uuid,
  c.id,
  s.arrival_time,
  s.departure_time
from race_plan_checkpoints c
right join (
  select
    unnest(sqlc.arg(checkpoint_positions)::integer[]) as checkpoint_position,
    unnest(sqlc.arg(arrival_times)::timestamptz[]) as arrival_time,
    unnest(sqlc.arg(departure_times)::timestamptz[]) as departure_time
) s on c.plan_id = sqlc.arg(plan_id)::uuid and c.position = s.checkpoint_position
on conflict (plan_id, checkpoint_id) do update set
  arrival_time = excluded.arrival_time,
  departure_time = excluded.departure_time;

-- name: DeleteRacePlanSplits :exec
delete from race_plan_splits
where plan_id = $1;

-- name: DeleteRacePlanSplit :exec
delete from race_plan_splits s
using race_plan_checkpoints c
where s.plan_id = sqlc.arg(plan_id)
  and c.plan_id = s.plan_id
  and c.id = s.checkpoint_id
  and c.position = sqlc.arg(checkpoint_position);

-- name: ListRacePlanSplits :many
select * from race_plan_splits
where plan_id = any(sqlc.arg(plan_ids)::uuid[]);
