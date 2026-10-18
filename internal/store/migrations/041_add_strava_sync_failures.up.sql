-- A sync that fails is retried with backoff. This is the number of syncs of
-- the connection that failed in a row, reset when one succeeds or Strava is
-- reconnected. While the sync state is failed, resume_time is when the sync
-- is retried, or null if it waits for Strava to be reconnected, as when the
-- athlete revoked access.
alter table strava_connections
  add column sync_failures integer not null default 0;

-- Failed syncs used to wait for the user, so retry them now.
update strava_connections set
  resume_time = current_timestamp
where sync_state = 'failed';

-- When the latest sync of the connection started, or null if it never
-- synced. Scheduled syncs serve the connections that waited longest first,
-- so one long backlog doesn't starve the others.
alter table strava_connections
  add column sync_start_time timestamptz;

-- The number of times syncing the activity's details failed. Activities that
-- fail are synced after the others, so they don't hold up the backlog, and
-- are skipped after a few failures, until they change on Strava or a user
-- asks for them.
alter table strava_activities
  add column sync_failures integer not null default 0;
