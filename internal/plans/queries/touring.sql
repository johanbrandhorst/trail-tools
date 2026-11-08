-- name: GetTouringLimits :many
-- Gets the user's touring limits: no rows if they haven't set any. It's a
-- :many query so a missing row isn't an error.
select * from touring_limits
where user_id = $1;

-- name: SetTouringLimits :one
insert into touring_limits (
  user_id, max_avalanche_danger_alpine, max_avalanche_danger_treeline, max_avalanche_danger_below_treeline,
  max_wind_gusts_kmh, max_freezing_level_meters, max_snowfall_past_72_hours_cm
)
values ($1, $2, $3, $4, $5, $6, $7)
on conflict (user_id) do update set
  max_avalanche_danger_alpine = excluded.max_avalanche_danger_alpine,
  max_avalanche_danger_treeline = excluded.max_avalanche_danger_treeline,
  max_avalanche_danger_below_treeline = excluded.max_avalanche_danger_below_treeline,
  max_wind_gusts_kmh = excluded.max_wind_gusts_kmh,
  max_freezing_level_meters = excluded.max_freezing_level_meters,
  max_snowfall_past_72_hours_cm = excluded.max_snowfall_past_72_hours_cm,
  update_time = now()
returning *;
