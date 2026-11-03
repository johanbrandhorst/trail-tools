-- Whether the user sent the course's route to Garmin themselves, rather
-- than it being put there for an upcoming workout. Sent courses stay on
-- Garmin until the user removes them, while the others are removed once
-- their workouts are past.
alter table garmin_courses add column sent boolean not null default false;
