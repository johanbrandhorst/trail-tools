import { create } from "@bufbuild/protobuf";
import { CheckboxField, SelectField } from "@components/ui/fields";
import type { SelectOption } from "@components/ui/fields";
import { Badge } from "@components/ui/misc";
import { inputClass } from "@components/ui/styles";
import type {
  Contingency,
  GearItem,
  TimelineItem,
} from "gen/races/v1/race_plans_pb";
import {
  ContingencyScenario,
  ContingencySchema,
  GearItemSchema,
  TimelineItemSchema,
} from "gen/races/v1/race_plans_pb";
import { CONTINGENCY_OPTIONS, contingencyLabel } from "helpers/labels";
import { parseEnum } from "helpers/parse";
import React, { useState } from "react";

import ListControls, { AddButton } from "./list-controls";
import type { SectionComponentProps } from "./section";
import Section, { EmptyText, nextKey, useSectionEdit } from "./section";

interface GearDraft {
  key: number;
  item: GearItem;
}

interface GearGroup {
  location: string;
  items: GearItem[];
}

const groupGear = (gear: GearItem[]): GearGroup[] => {
  const groups: GearGroup[] = [];
  for (const item of gear) {
    const location = item.location === "" ? "Unassigned" : item.location;
    const group = groups.find((candidate) => candidate.location === location);
    if (group === undefined) {
      groups.push({ items: [item], location });
    } else {
      group.items.push(item);
    }
  }
  return groups;
};

export const GearSection: React.FC<SectionComponentProps> = ({
  plan,
  rules,
  onSave,
}) => {
  const edit = useSectionEdit(onSave);
  const [drafts, setDrafts] = useState<GearDraft[]>([]);
  const [toggling, setToggling] = useState(false);

  const startEdit = () => {
    setDrafts(plan.gear.map((item) => ({ item, key: nextKey() })));
    edit.start();
  };

  const update = (key: number, item: GearItem) => {
    setDrafts(
      drafts.map((draft) => (draft.key === key ? { item, key } : draft))
    );
  };

  // TogglePacked saves the packed state straight away, like a checklist.
  const togglePacked = (target: GearItem, packed: boolean) => {
    if (onSave === undefined) {
      return;
    }
    setToggling(true);
    const gear = plan.gear.map((item) =>
      item === target ? { ...item, packed } : item
    );
    onSave({ ...plan, gear }, "gear")
      .catch((error: unknown) => {
        console.error("failed to update gear:", error);
      })
      .finally(() => {
        setToggling(false);
      });
  };

  const packedCount = plan.gear.filter((item) => item.packed).length;

  return (
    <Section
      section="gear"
      title="Gear"
      rules={rules}
      editable={onSave !== undefined}
      edit={edit}
      onStartEdit={startEdit}
      extra={
        plan.gear.length > 0 && !edit.editing ? (
          <span className="text-xs text-gray-500 tabular-nums">
            {packedCount}/{plan.gear.length} packed
          </span>
        ) : undefined
      }
      onSubmit={() => {
        edit.save(
          {
            ...plan,
            gear: drafts
              .map((draft) => draft.item)
              .filter((item) => item.name.trim() !== ""),
          },
          "gear"
        );
      }}
    >
      {edit.editing && (
        <div className="flex flex-col gap-2">
          {drafts.map((draft, index) => (
            <div
              key={draft.key}
              className="grid grid-cols-2 items-center gap-2 rounded-md border border-gray-200 p-2 sm:grid-cols-[2fr_1fr_auto_auto_2fr_auto]"
            >
              <input
                value={draft.item.name}
                className={`${inputClass} col-span-2 sm:col-span-1`}
                placeholder="Waterproof jacket with taped seams"
                aria-label="Item"
                onChange={(event) => {
                  update(draft.key, {
                    ...draft.item,
                    name: event.target.value,
                  });
                }}
              />
              <input
                value={draft.item.location}
                className={inputClass}
                placeholder="vest, worn, drop bag CP3"
                aria-label="Location"
                onChange={(event) => {
                  update(draft.key, {
                    ...draft.item,
                    location: event.target.value,
                  });
                }}
              />
              <CheckboxField
                label="Mandatory"
                checked={draft.item.mandatory}
                onChange={(checked) => {
                  update(draft.key, { ...draft.item, mandatory: checked });
                }}
              />
              <CheckboxField
                label="Packed"
                checked={draft.item.packed}
                onChange={(checked) => {
                  update(draft.key, { ...draft.item, packed: checked });
                }}
              />
              <input
                value={draft.item.notes}
                className={inputClass}
                placeholder="Notes"
                aria-label="Notes"
                onChange={(event) => {
                  update(draft.key, {
                    ...draft.item,
                    notes: event.target.value,
                  });
                }}
              />
              <ListControls
                index={index}
                count={drafts.length}
                onRemove={(position) => {
                  setDrafts(
                    drafts.filter((_draft, other) => other !== position)
                  );
                }}
              />
            </div>
          ))}
          <AddButton
            label="Add item"
            onClick={() => {
              setDrafts([
                ...drafts,
                { item: create(GearItemSchema), key: nextKey() },
              ]);
            }}
          />
        </div>
      )}
      {!edit.editing && plan.gear.length === 0 && (
        <EmptyText>
          No gear yet. Start with the race's mandatory kit list.
        </EmptyText>
      )}
      {!edit.editing && plan.gear.length > 0 && (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          {groupGear(plan.gear).map((group) => (
            <div key={group.location} className="break-inside-avoid">
              <h3 className="mb-1 text-xs font-semibold tracking-wide text-gray-500 uppercase">
                {group.location}
              </h3>
              <ul className="flex flex-col gap-1">
                {group.items.map((item) => (
                  <li
                    key={`${item.name}-${item.notes}`}
                    className="flex items-start gap-2 text-sm"
                  >
                    <input
                      type="checkbox"
                      checked={item.packed}
                      disabled={onSave === undefined || toggling}
                      aria-label={`Packed ${item.name}`}
                      className="mt-0.5 size-4 accent-brand-700"
                      onChange={(event) => {
                        togglePacked(item, event.target.checked);
                      }}
                    />
                    <span className={item.packed ? "text-gray-500" : ""}>
                      {item.name}
                      {item.mandatory && (
                        <Badge className="ml-1 bg-red-50 text-red-700 ring-red-200">
                          mandatory
                        </Badge>
                      )}
                      {item.notes !== "" && (
                        <span className="block text-xs text-gray-500">
                          {item.notes}
                        </span>
                      )}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      )}
    </Section>
  );
};

const SCENARIO_SELECT: SelectOption[] = CONTINGENCY_OPTIONS.map((option) => ({
  label: option.label,
  value: String(option.value),
}));

interface ContingencyDraft {
  key: number;
  item: Contingency;
}

export const ContingenciesSection: React.FC<SectionComponentProps> = ({
  plan,
  rules,
  onSave,
}) => {
  const edit = useSectionEdit(onSave);
  const [drafts, setDrafts] = useState<ContingencyDraft[]>([]);

  const update = (key: number, item: Contingency) => {
    setDrafts(
      drafts.map((draft) => (draft.key === key ? { item, key } : draft))
    );
  };

  return (
    <Section
      section="contingencies"
      title="Contingencies"
      rules={rules}
      editable={onSave !== undefined}
      edit={edit}
      onStartEdit={() => {
        setDrafts(plan.contingencies.map((item) => ({ item, key: nextKey() })));
        edit.start();
      }}
      onSubmit={() => {
        edit.save(
          { ...plan, contingencies: drafts.map((draft) => draft.item) },
          "contingencies"
        );
      }}
    >
      {edit.editing && (
        <div className="flex flex-col gap-2">
          {drafts.map((draft, index) => (
            <div
              key={draft.key}
              className="flex flex-col gap-2 rounded-md border border-gray-200 p-2"
            >
              <div className="flex items-end gap-2">
                <SelectField
                  label="Scenario"
                  className="grow"
                  value={String(draft.item.scenario)}
                  options={SCENARIO_SELECT}
                  onChange={(value) => {
                    const scenario = parseEnum(
                      ContingencyScenario,
                      value,
                      ContingencyScenario.UNSPECIFIED
                    );
                    update(draft.key, { ...draft.item, scenario });
                  }}
                />
                <ListControls
                  index={index}
                  count={drafts.length}
                  onRemove={(position) => {
                    setDrafts(
                      drafts.filter((_draft, other) => other !== position)
                    );
                  }}
                />
              </div>
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                <textarea
                  value={draft.item.trigger}
                  rows={2}
                  className={inputClass}
                  placeholder="Trigger: how to recognise it"
                  aria-label="Trigger"
                  onChange={(event) => {
                    update(draft.key, {
                      ...draft.item,
                      trigger: event.target.value,
                    });
                  }}
                />
                <textarea
                  value={draft.item.response}
                  rows={2}
                  className={inputClass}
                  placeholder="Response: what to do about it"
                  aria-label="Response"
                  onChange={(event) => {
                    update(draft.key, {
                      ...draft.item,
                      response: event.target.value,
                    });
                  }}
                />
              </div>
            </div>
          ))}
          <AddButton
            label="Add contingency"
            onClick={() => {
              setDrafts([
                ...drafts,
                {
                  item: create(ContingencySchema, {
                    scenario: ContingencyScenario.OTHER,
                  }),
                  key: nextKey(),
                },
              ]);
            }}
          />
        </div>
      )}
      {!edit.editing && plan.contingencies.length === 0 && (
        <EmptyText>No contingency plans yet.</EmptyText>
      )}
      {!edit.editing && plan.contingencies.length > 0 && (
        <dl className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          {plan.contingencies.map((item) => (
            <div
              key={`${item.scenario}-${item.trigger}`}
              className="rounded-md border border-gray-200 p-3 break-inside-avoid"
            >
              <dt className="text-sm font-semibold text-gray-900">
                {contingencyLabel(item.scenario)}
              </dt>
              <dd className="mt-1 text-sm text-gray-700">
                {item.trigger === "" && item.response === "" ? (
                  <span className="text-gray-400 italic">Not planned yet</span>
                ) : (
                  <>
                    {item.trigger !== "" && (
                      <p>
                        <span className="text-xs font-medium text-gray-500 uppercase">
                          If{" "}
                        </span>
                        {item.trigger}
                      </p>
                    )}
                    {item.response !== "" && (
                      <p className="whitespace-pre-line">
                        <span className="text-xs font-medium text-gray-500 uppercase">
                          Then{" "}
                        </span>
                        {item.response}
                      </p>
                    )}
                  </>
                )}
              </dd>
            </div>
          ))}
        </dl>
      )}
    </Section>
  );
};

interface TimelineDraft {
  key: number;
  item: TimelineItem;
}

export const RaceMorningSection: React.FC<SectionComponentProps> = ({
  plan,
  rules,
  onSave,
}) => {
  const edit = useSectionEdit(onSave);
  const [drafts, setDrafts] = useState<TimelineDraft[]>([]);

  const update = (key: number, item: TimelineItem) => {
    setDrafts(
      drafts.map((draft) => (draft.key === key ? { item, key } : draft))
    );
  };

  return (
    <Section
      section="race_morning"
      title="Race morning"
      rules={rules}
      editable={onSave !== undefined}
      edit={edit}
      onStartEdit={() => {
        setDrafts(plan.raceMorning.map((item) => ({ item, key: nextKey() })));
        edit.start();
      }}
      onSubmit={() => {
        const items = drafts
          .map((draft) => draft.item)
          .filter((item) => item.time !== "" || item.description !== "");
        items.sort((first, second) => first.time.localeCompare(second.time));
        edit.save({ ...plan, raceMorning: items }, "race_morning");
      }}
    >
      {edit.editing && (
        <div className="flex flex-col gap-2">
          {drafts.map((draft, index) => (
            <div key={draft.key} className="flex items-center gap-2">
              <input
                type="time"
                value={draft.item.time}
                className={`${inputClass} w-32 shrink-0`}
                aria-label="Time"
                onChange={(event) => {
                  update(draft.key, {
                    ...draft.item,
                    time: event.target.value,
                  });
                }}
              />
              <input
                value={draft.item.description}
                className={inputClass}
                placeholder="Wake up, breakfast: oats and coffee"
                aria-label="Description"
                onChange={(event) => {
                  update(draft.key, {
                    ...draft.item,
                    description: event.target.value,
                  });
                }}
              />
              <ListControls
                index={index}
                count={drafts.length}
                onRemove={(position) => {
                  setDrafts(
                    drafts.filter((_draft, other) => other !== position)
                  );
                }}
              />
            </div>
          ))}
          <AddButton
            label="Add step"
            onClick={() => {
              setDrafts([
                ...drafts,
                { item: create(TimelineItemSchema), key: nextKey() },
              ]);
            }}
          />
          <p className="text-xs text-gray-500">Steps are sorted by time.</p>
        </div>
      )}
      {!edit.editing && plan.raceMorning.length === 0 && (
        <EmptyText>No race morning timeline yet.</EmptyText>
      )}
      {!edit.editing && plan.raceMorning.length > 0 && (
        <ol className="relative flex flex-col gap-2 border-l-2 border-brand-200 pl-4">
          {plan.raceMorning.map((item) => (
            <li
              key={`${item.time}-${item.description}`}
              className="relative text-sm"
            >
              <span className="absolute top-1.5 -left-[1.3rem] size-2.5 rounded-full bg-ink" />
              <span className="mr-2 font-semibold text-gray-900 tabular-nums">
                {item.time}
              </span>
              <span className="text-gray-700">{item.description}</span>
            </li>
          ))}
        </ol>
      )}
    </Section>
  );
};
