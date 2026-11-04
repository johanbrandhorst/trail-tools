-- The token of a workout's share link, which lets anyone with it view the
-- workout and download its route and watch files without signing in. Null
-- while the workout isn't shared.
alter table workouts add column share_token text unique;
