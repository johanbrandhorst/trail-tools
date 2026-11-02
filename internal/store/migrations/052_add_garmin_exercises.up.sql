-- The exercise of Garmin's catalog an exercise of the library is, like
-- "SQUAT/GOBLET_SQUAT", so the watch shows and counts it as that exercise.
-- Garmin's Training API only takes exercises of its catalog. Empty if not
-- set, when one is matched by the exercise's name, if any.
alter table exercises add column garmin_exercise text not null default '';
