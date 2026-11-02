-- Garmin Connect, alongside Strava. An activity can come from either or
-- both: the watch uploads it to Garmin, which may pass it on to Strava. It's
-- then one activity, with both IDs, whichever arrives first. The first
-- source with the activity's samples keeps them, so a source arriving later
-- doesn't overwrite them, and Strava's names, which users edit there, win.

create table garmin_connections (
  user_id uuid primary key references users(id) on delete cascade,
  -- Garmin's ID of the user, which notifications identify them by.
  garmin_user_id text not null unique,
  access_token text not null,
  refresh_token text not null,
  expiry timestamptz not null,
  -- The permissions the user granted, e.g. ACTIVITY_EXPORT and
  -- WORKOUT_IMPORT.
  permissions text[] not null default '{}',
  -- When the user's workouts were last put on their Garmin calendar, null
  -- if never.
  workouts_sync_time timestamptz,
  -- What the last sync did or why it failed.
  sync_message text not null default '',
  create_time timestamptz not null default current_timestamp,
  update_time timestamptz not null default current_timestamp
);

alter table strava_activities
  alter column strava_id drop not null,
  -- Garmin's ID of the activity, null for activities only on Strava.
  add column garmin_activity_id bigint,
  -- Strava's ID of the upload the activity came from, like
  -- "garmin_ping_123" for an activity from Garmin, to match the two.
  add column strava_external_id text not null default '',
  -- Where the activity's stats and samples come from: 'strava' or
  -- 'garmin'.
  add column data_source text not null default 'strava',
  -- Whether the activity has Garmin's samples, from its FIT file. Its
  -- streams_synced is only about Strava's details.
  add column garmin_samples boolean not null default false,
  add constraint strava_activities_source check (strava_id is not null or garmin_activity_id is not null),
  add constraint strava_activities_data_source check (data_source in ('strava', 'garmin'));

create unique index strava_activities_user_id_garmin_activity_id on strava_activities (user_id, garmin_activity_id);

-- The activity files Garmin offered, to download. Garmin's links expire
-- after a day.
create table garmin_activity_files (
  user_id uuid not null references users(id) on delete cascade,
  garmin_activity_id bigint not null,
  callback_url text not null,
  expire_time timestamptz not null,
  attempts integer not null default 0,
  create_time timestamptz not null default current_timestamp,
  primary key (user_id, garmin_activity_id)
);

-- The workouts put on users' Garmin calendars.
create table garmin_workouts (
  id uuid primary key default gen_random_uuid(),
  user_id uuid not null references users(id) on delete cascade,
  -- Null once the workout is deleted, so it's removed from Garmin too.
  workout_id uuid unique references workouts(id) on delete set null,
  garmin_workout_id bigint not null,
  garmin_schedule_id bigint,
  -- A hash of what was sent, to send the workout again only when it
  -- changes.
  content_hash text not null,
  create_time timestamptz not null default current_timestamp
);

create index garmin_workouts_user_id on garmin_workouts (user_id);
