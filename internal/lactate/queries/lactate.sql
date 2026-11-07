-- name: CreateLactateTest :one
insert into lactate_tests (
  user_id,
  date,
  notes,
  resting_lactate_mmol_per_liter,
  stage_heart_rates_bpm,
  stage_lactates_mmol_per_liter,
  workout_id
) values (
  $1, $2, $3, $4, $5, $6, $7
)
returning *;

-- name: GetLactateTest :one
select * from lactate_tests
where id = $1 and user_id = $2;

-- name: ListLactateTests :many
select * from lactate_tests
where user_id = $1
order by date desc, create_time desc;

-- name: UpdateLactateTest :one
update lactate_tests set
  date = $3,
  notes = $4,
  resting_lactate_mmol_per_liter = $5,
  stage_heart_rates_bpm = $6,
  stage_lactates_mmol_per_liter = $7,
  workout_id = $8,
  update_time = now()
where id = $1 and user_id = $2
returning *;

-- name: DeleteLactateTest :execrows
delete from lactate_tests
where id = $1 and user_id = $2;

-- name: CreateLactateTestShare :one
insert into lactate_test_shares (user_id, token_sha256, expire_time, workout_id)
values ($1, $2, $3, $4)
returning *;

-- name: ListLactateTestShares :many
select * from lactate_test_shares
where user_id = $1 and expire_time > now()
order by create_time desc;

-- name: DeleteLactateTestShare :execrows
delete from lactate_test_shares
where id = $1 and user_id = $2;

-- name: GetLactateTestShareByToken :one
select * from lactate_test_shares
where token_sha256 = $1 and expire_time > now();

-- name: OpenLactateTestShare :execrows
-- Records the browser that opened a link, unless another one did first.
update lactate_test_shares set recorder_sha256 = $2
where id = $1 and recorder_sha256 is null and expire_time > now();

-- name: GetLactateTestRecording :one
select s.*, u.name as owner_name from lactate_test_shares s
join passkey_users u on u.id = s.user_id
where s.recorder_sha256 = $1 and s.expire_time > now();

-- name: SetLactateTestShareTest :exec
update lactate_test_shares set test_id = $2
where id = $1;

-- name: OwnsWorkout :one
-- Reports whether the workout exists and is in one of the user's plans.
select exists (
  select 1 from workouts w
  join training_plans p on p.id = w.plan_id
  where w.id = $1 and p.user_id = $2
);
