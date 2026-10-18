-- Links AI agents upload the user's photos to a trip or race report with,
-- as MCP tools can't take files. Only the hash of the link's secret token is
-- stored.
create table photo_upload_links (
  id uuid primary key default gen_random_uuid(),
  user_id uuid not null references users(id) on delete cascade,
  token_sha256 bytea unique not null,
  -- The report the link uploads to: a trip report or a race report.
  trip_report_id uuid references trip_reports(id) on delete cascade,
  race_report_id uuid references race_reports(id) on delete cascade,
  expire_time timestamptz not null,
  create_time timestamptz not null default current_timestamp,
  check (num_nonnulls(trip_report_id, race_report_id) = 1)
);

create index photo_upload_links_user on photo_upload_links (user_id);
