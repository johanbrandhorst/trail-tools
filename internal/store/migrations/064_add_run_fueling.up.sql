-- What to eat and drink during a run, as rates per hour, like a race plan's
-- nutrition. Null for runs without a fueling plan and other workouts.
alter table workouts add column run_fueling jsonb;
