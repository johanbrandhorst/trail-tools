-- How the user sweats, for their sodium intake on runs: how much sodium a
-- liter of their sweat has, from a sweat test, and how fast they sweat
-- running easy in mild weather, if they've measured it. Null if unknown.
create table sweat_profiles (
  user_id uuid primary key references users(id) on delete cascade,
  sodium_mg_per_liter double precision,
  rate_ml_per_hour double precision,
  update_time timestamptz not null default current_timestamp
);
