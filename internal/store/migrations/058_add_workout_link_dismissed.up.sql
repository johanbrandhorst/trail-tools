-- Workouts are linked to the activity that completes them as activities
-- sync. A workout whose link the user removed isn't linked again, so a wrong
-- guess stays undone.
alter table workouts add column link_dismissed boolean not null default false;
