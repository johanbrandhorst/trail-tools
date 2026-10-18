-- A photo of a race report, placed like the photos of trip reports. The
-- images are stored in the blob store under <object_key>/large.jpg and
-- <object_key>/thumb.jpg.
create table race_report_photos (
  id uuid primary key default gen_random_uuid(),
  report_id uuid not null references race_reports(id) on delete cascade,
  -- The order of the photos: as they were taken, when they were added.
  position integer not null check (position >= 0),
  object_key text not null unique,
  -- The size of the large image.
  width integer not null check (width > 0),
  height integer not null check (height > 0),
  caption text not null,
  lat double precision,
  lng double precision,
  location_source photo_location_source,
  -- The distance along the report's track of the point nearest to the
  -- location.
  track_distance_meters double precision,
  taken_time timestamptz,
  create_time timestamptz not null default current_timestamp,
  check ((lat is null) = (lng is null)),
  check ((lat is null) = (location_source is null))
);

create index race_report_photos_report on race_report_photos (report_id, position);
