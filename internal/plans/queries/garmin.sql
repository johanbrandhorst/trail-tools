-- name: ListGarminWorkouts :many
-- Lists the workouts on the user's Garmin calendar, with the date of those
-- still in a plan.
select gw.*, w.date as workout_date
from garmin_workouts gw
left join workouts w on w.id = gw.workout_id
where gw.user_id = $1;

-- name: InsertGarminWorkout :exec
insert into garmin_workouts (
  user_id,
  workout_id,
  garmin_workout_id,
  garmin_schedule_id,
  content_hash
) values (
  $1, $2, $3, $4, $5
);

-- name: DeleteGarminWorkout :exec
delete from garmin_workouts
where id = $1 and user_id = $2;

-- name: DeleteGarminWorkouts :exec
delete from garmin_workouts
where user_id = $1;

-- name: GetWorkoutByActivity :one
-- Gets the user's workout an activity completed.
select w.id from workouts w
join training_plans p on p.id = w.plan_id
where p.user_id = $1 and w.completed_activity_id = $2
order by w.date
limit 1;

-- name: ListGarminCourses :many
-- Lists the courses on the user's Garmin account.
select * from garmin_courses
where user_id = $1;

-- name: InsertGarminCourse :exec
insert into garmin_courses (
  user_id,
  route_id,
  garmin_course_id,
  content_hash,
  sent
) values (
  $1, $2, $3, $4, $5
);

-- name: SetGarminCourseSent :execrows
update garmin_courses set sent = $3
where user_id = $1 and route_id = $2;

-- name: DeleteGarminCourse :exec
delete from garmin_courses
where id = $1 and user_id = $2;

-- name: DeleteGarminCourses :exec
delete from garmin_courses
where user_id = $1;
