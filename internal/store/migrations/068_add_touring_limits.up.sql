-- The user's own limits for ski touring, which forecasts for ski tours are
-- checked against for a go, caution or no-go verdict. Null means no limit.
-- The highest avalanche danger they'll tour in, per elevation band, on the
-- 1 (low) to 5 (extreme) danger scale.
create table touring_limits (
  user_id uuid primary key references users(id) on delete cascade,
  max_avalanche_danger_alpine smallint check (max_avalanche_danger_alpine between 1 and 5),
  max_avalanche_danger_treeline smallint check (max_avalanche_danger_treeline between 1 and 5),
  max_avalanche_danger_below_treeline smallint check (max_avalanche_danger_below_treeline between 1 and 5),
  -- The strongest gusts they'll tour in.
  max_wind_gusts_kmh double precision,
  -- The highest freezing level they'll tour under: warming makes wet
  -- avalanches and loose snow likely.
  max_freezing_level_meters double precision,
  -- The most snow fallen in the 72 hours before they'll tour in.
  max_snowfall_past_72_hours_cm double precision,
  update_time timestamptz not null default current_timestamp
);
