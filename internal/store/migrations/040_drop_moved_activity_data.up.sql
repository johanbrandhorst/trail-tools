-- Activity streams and Strava's raw responses are stored in the blob store,
-- and the server moved the data these columns held there after migration
-- 039. Dropping a column that still holds data would lose it, so this fails
-- instead if any is left. The server then doesn't start, and the previous
-- revision keeps serving; once the data has moved, mark the migration as not
-- applied with `update schema_migrations set version = 39, dirty = false` and
-- deploy again. The check and the drops are one statement, so the drops never
-- run without the check, however the file is run.
do $$
begin
  if exists (select from strava_activity_streams where streams is not null)
    or exists (select from strava_activities where summary_json is not null or detail_json is not null)
    or exists (select from strava_activity_photos where photo_json is not null) then
    raise exception 'activity data is left to move to the blob store';
  end if;
  alter table strava_activity_streams drop column streams;
  alter table strava_activities drop column summary_json, drop column detail_json;
  alter table strava_activity_photos drop column photo_json;
end
$$;
