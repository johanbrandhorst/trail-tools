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
