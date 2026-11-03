-- Where a workout starts, for workouts without a route, like a group run's
-- meeting point, so its weather forecast is for the right place. Null if
-- unset; the latitude and longitude are both set or both null.
alter table workouts
  add column location_lat double precision,
  add column location_lng double precision,
  add column location_name text not null default '',
  add constraint workouts_location_both check ((location_lat is null) = (location_lng is null));
