-- A random secret per user, from which public pages derive circles to hide
-- that are larger than the user's privacy zones and centered away from them.
-- Tracks are cut where they cross a circle, so several tracks from home would
-- otherwise give away its center, the user's home. The offset must be the
-- same on every page, or averaging the circles would find the center anyway.
alter table users add column privacy_seed uuid not null default gen_random_uuid();
