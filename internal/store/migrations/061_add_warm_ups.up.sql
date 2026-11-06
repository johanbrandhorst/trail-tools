-- Warm-ups and cool-downs are groups of exercises like straight sets, rated
-- by how stiff the user felt rather than by effort.
alter table workout_exercises
  drop constraint workout_exercises_group_kind,
  add constraint workout_exercises_group_kind check (group_kind in ('', 'superset', 'circuit', 'warm_up', 'cool_down'));

-- How stiff the user felt in a warm-up or cool-down exercise. Empty if not
-- rated.
alter table strength_log_exercises
  add column stiffness text not null default '',
  add constraint strength_log_exercises_stiffness check (stiffness in ('', 'loose', 'a_bit_stiff', 'stiff', 'sore'));

-- Groups named like warm-ups and cool-downs were straight sets: they're now
-- warm-ups and cool-downs, which have no target effort.
update workout_exercises set
  group_kind = case
    when replace(replace(group_name, '-', ''), ' ', '') ilike '%warmup%' then 'warm_up'
    else 'cool_down'
  end,
  target_effort = 0
where group_kind = ''
  and (replace(replace(group_name, '-', ''), ' ', '') ilike '%warmup%'
    or replace(replace(group_name, '-', ''), ' ', '') ilike '%cooldown%');
