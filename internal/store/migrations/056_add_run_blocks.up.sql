-- The structure of a run, like a warm-up, intervals and a cool-down, as
-- JSON: a list of blocks, each with its steps and how often they're
-- repeated. Null for runs without structure, and other workouts.
alter table workouts add column run_blocks jsonb;
