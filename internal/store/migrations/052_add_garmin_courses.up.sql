-- The courses put on users' Garmin Connect accounts, of the routes of their
-- upcoming workouts, which Garmin syncs to their watches. A route used by
-- several workouts is one course.
create table garmin_courses (
  id uuid primary key default gen_random_uuid(),
  user_id uuid not null references users(id) on delete cascade,
  -- Null once the route is deleted, so its course is removed from Garmin
  -- too.
  route_id uuid references routes(id) on delete set null,
  garmin_course_id bigint not null,
  -- A hash of what was sent, to send the course again only when the route
  -- changes.
  content_hash text not null,
  create_time timestamptz not null default current_timestamp
);

create unique index garmin_courses_user_id_route_id on garmin_courses (user_id, route_id);
