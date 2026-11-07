-- name: GetSweatProfile :many
-- Gets the user's sweat profile: no rows if they haven't set one. It's a
-- :many query so a batch can read it without failing.
select * from sweat_profiles
where user_id = $1;

-- name: SetSweatProfile :one
insert into sweat_profiles (user_id, sodium_mg_per_liter, rate_ml_per_hour)
values ($1, $2, $3)
on conflict (user_id) do update set
  sodium_mg_per_liter = excluded.sodium_mg_per_liter,
  rate_ml_per_hour = excluded.rate_ml_per_hour,
  update_time = now()
returning *;
