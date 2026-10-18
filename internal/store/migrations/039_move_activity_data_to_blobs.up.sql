-- Activity streams and Strava's raw responses move to Cloud Storage, where
-- they cost much less than in the database: they're written once, read whole
-- by key and never queried. New data isn't stored in these columns. The
-- server moves the data already in them, emptying them as it goes, and a
-- later migration drops them once they're empty.
alter table strava_activity_streams alter column streams drop not null;
alter table strava_activities alter column summary_json drop not null;
alter table strava_activity_photos alter column photo_json drop not null;

