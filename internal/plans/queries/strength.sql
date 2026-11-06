-- name: CreateExercise :one
insert into exercises (
  user_id,
  name,
  instructions,
  feel,
  primary_muscles,
  secondary_muscles,
  equipment,
  equipment_notes,
  each_side,
  animations,
  garmin_exercise
) values (
  $1, $2, $3, $4,
  -- Enum arrays are sent as text, as they can't be encoded.
  sqlc.arg(primary_muscles)::text[]::muscle[],
  sqlc.arg(secondary_muscles)::text[]::muscle[],
  sqlc.arg(equipment)::text[]::equipment[],
  $5, $6, $7, $8
)
returning *;

-- name: ListExercises :many
select * from exercises
where user_id = $1
order by lower(name), create_time;

-- name: GetExercise :one
select * from exercises
where id = $1 and user_id = $2;

-- name: FindExercises :many
-- Finds which of the exercises exist and belong to the user.
select * from exercises
where user_id = $1 and id = any(sqlc.arg(ids)::uuid[]);

-- name: UpdateExercise :one
update exercises set
  name = $3,
  instructions = $4,
  feel = $5,
  primary_muscles = sqlc.arg(primary_muscles)::text[]::muscle[],
  secondary_muscles = sqlc.arg(secondary_muscles)::text[]::muscle[],
  equipment = sqlc.arg(equipment)::text[]::equipment[],
  equipment_notes = $6,
  each_side = $7,
  animations = $8,
  garmin_exercise = $9,
  update_time = now()
where id = $1 and user_id = $2
returning *;

-- name: DeleteExercise :execrows
delete from exercises
where id = $1 and user_id = $2;

-- name: ListWorkoutExercises :many
-- Lists the exercises of the user's workouts, optionally of a plan, of
-- some workouts or within dates, by workout and position.
select we.*, e.name as exercise_name, e.equipment::text[] as exercise_equipment, e.each_side as exercise_each_side,
  e.garmin_exercise as exercise_garmin_exercise
from workout_exercises we
join workouts w on w.id = we.workout_id
join training_plans p on p.id = w.plan_id
join exercises e on e.id = we.exercise_id
where p.user_id = sqlc.arg(user_id)::uuid
  and (sqlc.narg(plan_id)::uuid is null or w.plan_id = sqlc.narg(plan_id)::uuid)
  and (sqlc.narg(workout_ids)::uuid[] is null or w.id = any(sqlc.narg(workout_ids)::uuid[]))
  and (sqlc.narg(start_date)::date is null or w.date >= sqlc.narg(start_date)::date)
  and (sqlc.narg(end_date)::date is null or w.date <= sqlc.narg(end_date)::date)
order by we.workout_id, we.position;

-- name: DeleteWorkoutExercises :exec
delete from workout_exercises
where workout_id = any(sqlc.arg(workout_ids)::uuid[]);

-- name: InsertWorkoutExercises :exec
insert into workout_exercises (
  workout_id,
  position,
  exercise_id,
  group_name,
  group_kind,
  sets,
  reps,
  hold_seconds,
  weight_value,
  weight_unit,
  rest_seconds,
  tempo,
  target_effort,
  notes
)
select
  e.workout_id, e.position, e.exercise_id, e.group_name, e.group_kind, e.sets, e.reps, e.hold_seconds,
  e.weight_value, nullif(e.weight_unit, '')::weight_unit,
  e.rest_seconds, e.tempo, e.target_effort, e.notes
from (
  select
    unnest(sqlc.arg(workout_ids)::uuid[]) as workout_id,
    unnest(sqlc.arg(positions)::integer[]) as position,
    unnest(sqlc.arg(exercise_ids)::uuid[]) as exercise_id,
    unnest(sqlc.arg(group_names)::text[]) as group_name,
    -- Empty for straight sets.
    unnest(sqlc.arg(group_kinds)::text[]) as group_kind,
    unnest(sqlc.arg(sets)::integer[]) as sets,
    unnest(sqlc.arg(reps)::integer[]) as reps,
    unnest(sqlc.arg(hold_seconds)::integer[]) as hold_seconds,
    unnest(sqlc.arg(weight_values)::double precision[]) as weight_value,
    -- Empty for bodyweight.
    unnest(sqlc.arg(weight_units)::text[]) as weight_unit,
    unnest(sqlc.arg(rest_seconds)::integer[]) as rest_seconds,
    unnest(sqlc.arg(tempos)::text[]) as tempo,
    unnest(sqlc.arg(target_efforts)::integer[]) as target_effort,
    unnest(sqlc.arg(notes)::text[]) as notes
) e;

-- name: ListStrengthLogs :many
-- Lists the logs of the user's workouts, with the same filters as
-- ListWorkoutExercises.
select l.*
from strength_logs l
join workouts w on w.id = l.workout_id
where l.user_id = sqlc.arg(user_id)::uuid
  and (sqlc.narg(plan_id)::uuid is null or w.plan_id = sqlc.narg(plan_id)::uuid)
  and (sqlc.narg(workout_ids)::uuid[] is null or w.id = any(sqlc.narg(workout_ids)::uuid[]))
  and (sqlc.narg(start_date)::date is null or w.date >= sqlc.narg(start_date)::date)
  and (sqlc.narg(end_date)::date is null or w.date <= sqlc.narg(end_date)::date);

-- name: ListStrengthLogExercises :many
-- Lists the exercises of the logs ListStrengthLogs lists, by log and
-- position.
select le.*, e.name as exercise_name
from strength_log_exercises le
join strength_logs l on l.id = le.log_id
join workouts w on w.id = l.workout_id
join exercises e on e.id = le.exercise_id
where l.user_id = sqlc.arg(user_id)::uuid
  and (sqlc.narg(plan_id)::uuid is null or w.plan_id = sqlc.narg(plan_id)::uuid)
  and (sqlc.narg(workout_ids)::uuid[] is null or w.id = any(sqlc.narg(workout_ids)::uuid[]))
  and (sqlc.narg(start_date)::date is null or w.date >= sqlc.narg(start_date)::date)
  and (sqlc.narg(end_date)::date is null or w.date <= sqlc.narg(end_date)::date)
order by le.log_id, le.position;

-- name: ListStrengthLogSets :many
-- Lists the sets of the logs ListStrengthLogs lists, by log, position and
-- set.
select s.*
from strength_log_sets s
join strength_logs l on l.id = s.log_id
join workouts w on w.id = l.workout_id
where l.user_id = sqlc.arg(user_id)::uuid
  and (sqlc.narg(plan_id)::uuid is null or w.plan_id = sqlc.narg(plan_id)::uuid)
  and (sqlc.narg(workout_ids)::uuid[] is null or w.id = any(sqlc.narg(workout_ids)::uuid[]))
  and (sqlc.narg(start_date)::date is null or w.date >= sqlc.narg(start_date)::date)
  and (sqlc.narg(end_date)::date is null or w.date <= sqlc.narg(end_date)::date)
order by s.log_id, s.position, s.set_number;

-- name: UpsertStrengthLog :one
insert into strength_logs (
  user_id,
  workout_id,
  date,
  effort,
  notes
) values (
  $1, $2, $3, $4, $5
)
on conflict (workout_id) do update set
  date = excluded.date,
  effort = excluded.effort,
  notes = excluded.notes,
  update_time = now()
returning *;

-- name: DeleteStrengthLogExercises :exec
delete from strength_log_exercises
where log_id = $1;

-- name: InsertStrengthLogExercises :exec
-- Inserts the exercises of a log. What was planned is only set where
-- planned is true.
insert into strength_log_exercises (
  log_id,
  position,
  exercise_id,
  effort,
  volume,
  stiffness,
  notes,
  planned_group_name,
  planned_sets,
  planned_reps,
  planned_hold_seconds,
  planned_weight_value,
  planned_weight_unit,
  planned_rest_seconds,
  planned_tempo,
  planned_target_effort,
  planned_notes
)
select
  sqlc.arg(log_id)::uuid,
  e.position,
  e.exercise_id,
  nullif(e.effort, '')::exercise_effort,
  nullif(e.volume, '')::exercise_volume,
  e.stiffness,
  e.notes,
  case when e.planned then e.planned_group_name end,
  case when e.planned then e.planned_sets end,
  case when e.planned then e.planned_reps end,
  case when e.planned then e.planned_hold_seconds end,
  case when e.planned then e.planned_weight_value end,
  case when e.planned then nullif(e.planned_weight_unit, '')::weight_unit end,
  case when e.planned then e.planned_rest_seconds end,
  case when e.planned then e.planned_tempo end,
  case when e.planned then e.planned_target_effort end,
  case when e.planned then e.planned_notes end
from (
  select
    unnest(sqlc.arg(positions)::integer[]) as position,
    unnest(sqlc.arg(exercise_ids)::uuid[]) as exercise_id,
    -- Empty if not rated.
    unnest(sqlc.arg(efforts)::text[]) as effort,
    unnest(sqlc.arg(volumes)::text[]) as volume,
    unnest(sqlc.arg(stiffnesses)::text[]) as stiffness,
    unnest(sqlc.arg(notes)::text[]) as notes,
    unnest(sqlc.arg(planned)::boolean[]) as planned,
    unnest(sqlc.arg(planned_group_names)::text[]) as planned_group_name,
    unnest(sqlc.arg(planned_sets)::integer[]) as planned_sets,
    unnest(sqlc.arg(planned_reps)::integer[]) as planned_reps,
    unnest(sqlc.arg(planned_hold_seconds)::integer[]) as planned_hold_seconds,
    unnest(sqlc.arg(planned_weight_values)::double precision[]) as planned_weight_value,
    unnest(sqlc.arg(planned_weight_units)::text[]) as planned_weight_unit,
    unnest(sqlc.arg(planned_rest_seconds)::integer[]) as planned_rest_seconds,
    unnest(sqlc.arg(planned_tempos)::text[]) as planned_tempo,
    unnest(sqlc.arg(planned_target_efforts)::integer[]) as planned_target_effort,
    unnest(sqlc.arg(planned_notes)::text[]) as planned_notes
) e;

-- name: InsertStrengthLogSets :exec
insert into strength_log_sets (
  log_id,
  position,
  set_number,
  reps,
  hold_seconds,
  weight_value,
  weight_unit
)
select
  sqlc.arg(log_id)::uuid, s.position, s.set_number, s.reps, s.hold_seconds,
  s.weight_value, nullif(s.weight_unit, '')::weight_unit
from (
  select
    unnest(sqlc.arg(positions)::integer[]) as position,
    unnest(sqlc.arg(set_numbers)::integer[]) as set_number,
    unnest(sqlc.arg(reps)::integer[]) as reps,
    unnest(sqlc.arg(hold_seconds)::integer[]) as hold_seconds,
    unnest(sqlc.arg(weight_values)::double precision[]) as weight_value,
    -- Empty for bodyweight.
    unnest(sqlc.arg(weight_units)::text[]) as weight_unit
) s;

-- name: DeleteStrengthLog :execrows
delete from strength_logs
where workout_id = $1 and user_id = $2;

-- name: ListExerciseHistory :many
-- Lists the logged exercises of the user, optionally of some exercises and
-- within dates, in date order.
select
  le.*,
  e.name as exercise_name,
  l.date,
  l.workout_id,
  l.effort as session_effort,
  coalesce(w.title, '')::text as workout_title
from strength_log_exercises le
join strength_logs l on l.id = le.log_id
join exercises e on e.id = le.exercise_id
left join workouts w on w.id = l.workout_id
where l.user_id = sqlc.arg(user_id)::uuid
  and (cardinality(sqlc.arg(exercise_ids)::uuid[]) = 0 or le.exercise_id = any(sqlc.arg(exercise_ids)::uuid[]))
  and (sqlc.narg(start_date)::date is null or l.date >= sqlc.narg(start_date)::date)
  and (sqlc.narg(end_date)::date is null or l.date <= sqlc.narg(end_date)::date)
order by l.date, l.complete_time, le.log_id, le.position;

-- name: ListExerciseHistorySets :many
-- Lists the sets of the exercises ListExerciseHistory lists, by log,
-- position and set.
select s.*
from strength_log_sets s
join strength_log_exercises le on le.log_id = s.log_id and le.position = s.position
join strength_logs l on l.id = s.log_id
where l.user_id = sqlc.arg(user_id)::uuid
  and (cardinality(sqlc.arg(exercise_ids)::uuid[]) = 0 or le.exercise_id = any(sqlc.arg(exercise_ids)::uuid[]))
  and (sqlc.narg(start_date)::date is null or l.date >= sqlc.narg(start_date)::date)
  and (sqlc.narg(end_date)::date is null or l.date <= sqlc.narg(end_date)::date)
order by s.log_id, s.position, s.set_number;

-- name: GetHomeEquipment :many
-- Gets the user's home equipment: no rows if they haven't said what they
-- have. It's a :many query so a batch can read it without failing.
select * from home_equipment
where user_id = $1;

-- name: SetHomeEquipment :one
insert into home_equipment (user_id, equipment)
values ($1, $2)
on conflict (user_id) do update set
  equipment = excluded.equipment,
  update_time = now()
returning *;
