-- How the exercises of a group of a strength workout are done: empty for
-- straight sets or no group, or back to back for rounds, as a superset or
-- circuit. The exercises of a superset or circuit all have the group's
-- rounds as their sets, and the rest between rounds is the rest of its last
-- exercise.
alter table workout_exercises
  add column group_kind text not null default '',
  add constraint workout_exercises_group_kind check (group_kind in ('', 'superset', 'circuit'));

-- Groups were supersets or circuits by their name.
update workout_exercises set group_kind = case
  when group_name ilike '%circuit%' then 'circuit'
  when group_name ilike '%superset%' then 'superset'
  else ''
end;

-- They were done for the most sets of their exercises.
update workout_exercises we set sets = g.rounds
from (
  select workout_id, group_name, max(sets) as rounds
  from workout_exercises
  where group_kind <> ''
  group by workout_id, group_name
) g
where we.workout_id = g.workout_id and we.group_name = g.group_name and we.sets <> g.rounds;
