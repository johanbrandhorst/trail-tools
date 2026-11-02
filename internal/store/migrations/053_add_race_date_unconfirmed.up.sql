-- Whether a race's date is a guess, like last year's date before the
-- organizers announce this year's, so plans built around it are shown as
-- tentative.
alter table races add column date_unconfirmed boolean not null default false;
