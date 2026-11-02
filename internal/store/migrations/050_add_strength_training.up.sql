-- Strength training: the user's library of exercises, the exercises planned
-- in strength workouts, and logs of what the user did in them. Logs are kept
-- when their workout is deleted, as the history of each exercise.

create type muscle as enum (
  'neck',
  'upper_back',
  'lats',
  'lower_back',
  'shoulders',
  'chest',
  'biceps',
  'triceps',
  'forearms',
  'abs',
  'obliques',
  'hip_flexors',
  'glutes',
  'hip_abductors',
  'adductors',
  'quads',
  'hamstrings',
  'calves',
  'shins',
  'feet'
);

-- The kinds of equipment exercises use.
create type equipment as enum (
  'dumbbells',
  'kettlebell',
  'barbell',
  'squat_rack',
  'medicine_ball',
  'bench',
  'step',
  'mat',
  'resistance_band',
  'mini_band',
  'pull_up_bar',
  'suspension_trainer',
  'foam_roller',
  'sliders',
  'balance_board',
  'jump_rope'
);

-- Weights keep the unit they're marked in, as dumbbells and kettlebells
-- come in kilograms or pounds.
create type weight_unit as enum (
  'kilograms',
  'pounds'
);

-- How hard a whole strength session felt.
create type session_effort as enum (
  'very_easy',
  'easy',
  'about_right',
  'hard',
  'very_hard'
);

-- How hard an exercise felt compared to what was planned.
create type exercise_effort as enum (
  'too_easy',
  'just_right',
  'too_hard'
);

-- How the number of reps of an exercise felt.
create type exercise_volume as enum (
  'too_few',
  'about_right',
  'too_many'
);

-- The user's exercises, defined once and reused by their strength workouts.
create table exercises (
  id uuid primary key default gen_random_uuid(),
  user_id uuid not null references users(id) on delete cascade,
  name text not null,
  -- How to do the exercise, step by step.
  instructions text[] not null default '{}',
  -- How the exercise should feel and where.
  feel text not null default '',
  primary_muscles muscle[] not null default '{}',
  secondary_muscles muscle[] not null default '{}',
  equipment equipment[] not null default '{}',
  equipment_notes text not null default '',
  -- Whether the exercise is done on each side in turn, so its reps and
  -- hold times are per side.
  each_side boolean not null default false,
  -- The stick figure animations of the exercise: a JSON array of
  -- plans.v1.ExerciseAnimation messages in their JSON form.
  animations jsonb not null default '[]',
  create_time timestamptz not null default current_timestamp,
  update_time timestamptz not null default current_timestamp
);

-- Names are unique per user, ignoring case, so agents find the exercise
-- rather than creating a duplicate.
create unique index exercises_user_id_name on exercises (user_id, lower(name));

-- The exercises of strength workouts, in order. An exercise can't be
-- deleted while a workout uses it.
create table workout_exercises (
  workout_id uuid not null references workouts(id) on delete cascade,
  position integer not null,
  exercise_id uuid not null references exercises(id),
  -- The part of the workout, e.g. "Warm-up" or "Superset A". Empty if none.
  group_name text not null default '',
  sets integer not null,
  -- Per side for exercises done on each side. 0 for exercises held for a
  -- time.
  reps integer not null default 0,
  -- 0 for exercises done for reps.
  hold_seconds integer not null default 0,
  -- Of each dumbbell for exercises with two, in the unit. 0, with no unit,
  -- for bodyweight.
  weight_value double precision not null default 0,
  weight_unit weight_unit,
  rest_seconds integer not null default 0,
  tempo text not null default '',
  -- From 1 to 10, 0 if not set.
  target_effort integer not null default 0,
  notes text not null default '',
  primary key (workout_id, position),
  check (sets > 0 and reps >= 0 and hold_seconds >= 0 and weight_value >= 0 and rest_seconds >= 0),
  check (weight_value = 0 or weight_unit is not null),
  check (target_effort between 0 and 10)
);

create index workout_exercises_exercise_id on workout_exercises (exercise_id);

-- What the user did in a strength workout. Saving it completes the workout.
-- The workout is null once it's deleted: the log is kept as history.
create table strength_logs (
  id uuid primary key default gen_random_uuid(),
  user_id uuid not null references users(id) on delete cascade,
  workout_id uuid unique references workouts(id) on delete set null,
  -- The date of the workout, kept for the history of deleted workouts.
  date date not null,
  -- Null if not rated.
  effort session_effort,
  notes text not null default '',
  -- When the log was first saved.
  complete_time timestamptz not null default current_timestamp,
  update_time timestamptz not null default current_timestamp
);

create index strength_logs_user_id_date on strength_logs (user_id, date);

-- The exercises of a strength log, in the order they were done, with what
-- was planned for them when the log was saved, so the history compares the
-- two even after the workout changes or is deleted.
create table strength_log_exercises (
  log_id uuid not null references strength_logs(id) on delete cascade,
  position integer not null,
  exercise_id uuid not null references exercises(id),
  -- Null if not rated.
  effort exercise_effort,
  volume exercise_volume,
  notes text not null default '',
  -- What was planned, null if the exercise wasn't planned.
  planned_group_name text,
  planned_sets integer,
  planned_reps integer,
  planned_hold_seconds integer,
  planned_weight_value double precision,
  planned_weight_unit weight_unit,
  planned_rest_seconds integer,
  planned_tempo text,
  planned_target_effort integer,
  planned_notes text,
  primary key (log_id, position)
);

create index strength_log_exercises_exercise_id on strength_log_exercises (exercise_id);

-- The sets done of each exercise of a strength log, a row per set for
-- analysis.
create table strength_log_sets (
  log_id uuid not null,
  position integer not null,
  set_number integer not null,
  -- Per side for exercises done on each side. 0 for exercises held for a
  -- time.
  reps integer not null default 0,
  -- 0 for exercises done for reps.
  hold_seconds integer not null default 0,
  -- 0, with no unit, for bodyweight.
  weight_value double precision not null default 0,
  weight_unit weight_unit,
  primary key (log_id, position, set_number),
  foreign key (log_id, position) references strength_log_exercises (log_id, position) on delete cascade,
  check (reps >= 0 and hold_seconds >= 0 and weight_value >= 0),
  check (weight_value = 0 or weight_unit is not null)
);

-- The equipment each user has at home, to plan strength workouts with: a
-- JSON array of plans.v1.OwnedEquipment messages in their JSON form. Users
-- without a row haven't said what they have.
create table home_equipment (
  user_id uuid primary key references users(id) on delete cascade,
  equipment jsonb not null,
  update_time timestamptz not null default current_timestamp
);

-- When workouts can start: from start_time, for start_window_minutes.
-- Workouts without a start time can be done any time of the day.
alter table workouts
  add column start_time time,
  add column start_window_minutes integer not null default 0;

-- Strength workouts are linked to the strength activity synced from Strava
-- on their day.
create index workouts_completed_activity_id on workouts (completed_activity_id);
