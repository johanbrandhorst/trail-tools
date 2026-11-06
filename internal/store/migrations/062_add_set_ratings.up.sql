-- How each set felt, as sets of an exercise can feel different: the first
-- too easy and the last too hard. Null and empty if not rated.
alter table strength_log_sets
  add column effort exercise_effort,
  add column stiffness text not null default '',
  add constraint strength_log_sets_stiffness check (stiffness in ('', 'loose', 'a_bit_stiff', 'stiff', 'sore'));
