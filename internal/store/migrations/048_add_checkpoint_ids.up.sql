-- Checkpoints get a stable identity, and the intakes and race day splits of a
-- checkpoint reference it rather than its position. Before, inserting,
-- removing or reordering checkpoints moved the recorded arrival times to
-- other checkpoints, or dropped them, because the splits stayed at their
-- positions.
alter table race_plan_checkpoints
  add column id uuid not null default gen_random_uuid();

alter table race_plan_intakes
  add column checkpoint_id uuid;

update race_plan_intakes i
set checkpoint_id = c.id
from race_plan_checkpoints c
where c.plan_id = i.plan_id and c.position = i.checkpoint_position;

alter table race_plan_splits
  add column checkpoint_id uuid;

update race_plan_splits s
set checkpoint_id = c.id
from race_plan_checkpoints c
where c.plan_id = s.plan_id and c.position = s.checkpoint_position;

-- The references by position go before the key they reference.
alter table race_plan_intakes
  drop constraint race_plan_intakes_plan_id_checkpoint_position_fkey,
  drop constraint race_plan_intakes_pkey,
  drop column checkpoint_position;

alter table race_plan_splits
  drop constraint race_plan_splits_plan_id_checkpoint_position_fkey,
  drop constraint race_plan_splits_pkey,
  drop column checkpoint_position;

-- The position stays unique within a plan, but is only checked at the end of
-- each statement, so that a single update can reorder the checkpoints.
-- (plan_id, id) is unique for the composite foreign keys below, which make
-- sure a checkpoint's intakes and splits belong to the same plan.
alter table race_plan_checkpoints
  drop constraint race_plan_checkpoints_pkey,
  add primary key (id),
  add constraint race_plan_checkpoints_plan_id_id_key unique (plan_id, id),
  add constraint race_plan_checkpoints_plan_id_position_key unique (plan_id, position) deferrable initially immediate;

alter table race_plan_intakes
  alter column checkpoint_id set not null,
  add primary key (plan_id, checkpoint_id, position),
  add foreign key (plan_id, checkpoint_id) references race_plan_checkpoints (plan_id, id) on delete cascade;

alter table race_plan_splits
  alter column checkpoint_id set not null,
  add primary key (plan_id, checkpoint_id),
  add foreign key (plan_id, checkpoint_id) references race_plan_checkpoints (plan_id, id) on delete cascade;
