-- The number of photos stored when the activity's details were last synced,
-- null until they are. Strava's activity list sometimes reports no photos for
-- an activity whose details have them, so photo_count stays what the list
-- reported, to notice photos changing on Strava, and this is the number of
-- photos the activity has once synced.
alter table strava_activities add column synced_photo_count integer;

-- Activities waiting for their details again, as their photos changed on
-- Strava, keep the photos of the last sync.
update strava_activities a set synced_photo_count = (
  select count(*) from strava_activity_photos p where p.activity_id = a.id
)
where a.streams_synced or exists (select from strava_activity_photos p where p.activity_id = a.id);
