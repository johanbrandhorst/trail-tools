-- Exercises done on each side can have different reps or hold times on the
-- left side, e.g. more on a weaker leg. The left side's are 0 when they're
-- the same as reps and hold_seconds, which are then the right side's.
alter table workout_exercises
  add column left_reps integer not null default 0 check (left_reps >= 0),
  add column left_hold_seconds integer not null default 0 check (left_hold_seconds >= 0);

-- What was planned for the left side when a log was saved, null where
-- nothing was planned.
alter table strength_log_exercises
  add column planned_left_reps integer,
  add column planned_left_hold_seconds integer;

alter table strength_log_sets
  add column left_reps integer not null default 0 check (left_reps >= 0),
  add column left_hold_seconds integer not null default 0 check (left_hold_seconds >= 0);
