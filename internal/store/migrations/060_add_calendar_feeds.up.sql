-- The secret of the user's calendar feed of upcoming workouts, for calendar
-- apps that can only subscribe to a URL. Null if they have no feed. It only
-- gives access to the feed, so it's kept to show the URL again.
alter table users add column calendar_token text unique;
