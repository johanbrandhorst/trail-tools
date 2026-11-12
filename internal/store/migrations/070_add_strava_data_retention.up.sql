-- Whether the user agreed to keep their activities from Strava on
-- trailrunn.ing if Strava is disconnected, in the app or by revoking access
-- on Strava. Strava's API agreement requires deleting the data obtained from
-- its API once the user revokes access, unless they agreed to keep it.
alter table strava_connections
  add column keep_activities boolean not null default false;

-- Activities the user imported from their own Strava archive: their data
-- came from the user rather than Strava's API, so they stay when Strava is
-- disconnected.
alter table strava_activities
  add column from_strava_archive boolean not null default false;

-- Users whose activities from Strava's API are being deleted, after they
-- disconnected Strava without agreeing to keep them. A long history takes a
-- while to delete, so the scheduled sync deletes it in batches, in order of
-- the activities' IDs, after_activity_id being the last one done.
create table strava_activity_purges (
  user_id uuid primary key references users(id) on delete cascade,
  after_activity_id uuid,
  create_time timestamptz not null default current_timestamp
);
