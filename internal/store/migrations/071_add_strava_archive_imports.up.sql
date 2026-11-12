-- The activities of a Strava archive being imported, as its activities.csv
-- lists them, until each is imported with its file, which the browser sends
-- one at a time. Each is deleted once imported, and an unfinished import's
-- after a day.
create table strava_archive_activities (
  user_id uuid not null references users(id) on delete cascade,
  strava_id bigint not null,
  name text not null,
  description text not null,
  sport_type text not null,
  start_time timestamptz not null,
  elapsed_time_seconds integer not null,
  moving_time_seconds integer not null,
  distance_meters double precision not null,
  elevation_gain_meters double precision not null,
  average_speed_mps double precision not null,
  max_speed_mps double precision not null,
  average_heartrate_bpm double precision not null,
  max_heartrate_bpm double precision not null,
  -- In steps per minute.
  average_cadence_spm double precision not null,
  relative_effort integer not null,
  -- The path of the activity's file in the archive, empty if it has none.
  filename text not null,
  create_time timestamptz not null default current_timestamp,
  primary key (user_id, strava_id)
);
