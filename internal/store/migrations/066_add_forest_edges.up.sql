-- Whether an edge runs through forest, from the OpenStreetMap forest and wood
-- areas its midpoint is in, so workouts on it can be warned about high winds,
-- which bring down branches and trees. The areas themselves aren't kept, as
-- they would take more storage than the edges. Regions imported before are
-- taken to have no forest until they are imported again.
alter table routing_edges add column forest boolean not null default false;
