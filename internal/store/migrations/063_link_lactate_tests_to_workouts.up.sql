-- A run can be a planned lactate test, and the test run in it links back
-- to it, so its workout page can start the test, send a recording link, and
-- show the result. Deleting the workout keeps the test.
alter table workouts add column lactate_test boolean not null default false;

alter table lactate_tests
  add column workout_id uuid references workouts(id) on delete set null;

create index lactate_tests_workout on lactate_tests (workout_id) where workout_id is not null;

-- A recording link for a workout links the test it records to it.
alter table lactate_test_shares
  add column workout_id uuid references workouts(id) on delete set null;
