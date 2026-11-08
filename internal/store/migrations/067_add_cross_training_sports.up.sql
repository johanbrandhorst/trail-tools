-- The sport of a cross-training workout, so a ski tour can be planned as
-- one: its forecast and linked activity depend on it. Empty for other
-- cross-training and for other workouts, whose title says what they are.
alter table workouts add column cross_training_sport text not null default ''
  check (cross_training_sport in ('', 'ski_touring'));
