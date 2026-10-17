-- name: DeleteActivityPhotos :exec
delete from strava_activity_photos
where activity_id = $1;

-- name: InsertActivityPhoto :exec
insert into strava_activity_photos (
  activity_id,
  strava_id,
  position,
  caption,
  url,
  width,
  height,
  lat,
  lng,
  taken_time
) values (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
)
on conflict (activity_id, strava_id) do nothing;

-- name: ListActivityPhotos :many
select p.strava_id, p.caption, p.url, p.width, p.height, p.lat, p.lng, p.taken_time
from strava_activity_photos p
join strava_activities a on a.id = p.activity_id
where p.activity_id = $1 and a.user_id = $2
order by p.position;

-- name: ListUnmovedPhotoResponses :many
-- Strava's responses for the photos of activities, as an array in order.
select p.activity_id, a.user_id, a.strava_id, jsonb_agg(p.photo_json order by p.position)::jsonb as responses
from strava_activity_photos p
join strava_activities a on a.id = p.activity_id
where p.photo_json is not null
group by p.activity_id, a.user_id, a.strava_id
limit $1;

-- name: ClearMovedPhotoResponses :exec
update strava_activity_photos set photo_json = null
where activity_id = any(sqlc.arg(activity_ids)::uuid[]);
