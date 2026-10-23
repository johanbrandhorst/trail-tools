import { create } from "@bufbuild/protobuf";
import { CheckboxField, TextAreaField, TextField } from "@components/ui/fields";
import type { SelectOption } from "@components/ui/fields";
import { Badge } from "@components/ui/misc";
import { iconButton, inputClass } from "@components/ui/styles";
import { XMarkIcon } from "@heroicons/react/24/outline";
import type {
  Checkpoint,
  Intake,
  NutritionStrategy,
} from "gen/races/v1/race_plans_pb";
import { CheckpointSchema, IntakeSchema } from "gen/races/v1/race_plans_pb";
import { formatDuration, formatKm } from "helpers/format";
import {
  hoursMinutesInput,
  kmInput,
  numberInput,
  parseDuration,
  parseEdited,
  parseKm,
  parseMeters,
  parseNumber,
} from "helpers/parse";
import React, { useState } from "react";

import ListControls, { AddButton } from "./list-controls";
import type { SectionComponentProps } from "./section";
import Section, {
  EmptyText,
  TextBlock,
  nextKey,
  useSectionEdit,
} from "./section";

interface IntakeDraft {
  key: number;
  product: string;
  quantity: string;
  // The stored quantity, kept unless edited. 0 for new intakes.
  originalQuantity: number;
}

interface CheckpointDraft {
  key: number;
  /**
   * The stored checkpoint, whose ID and values the draft keeps unless they
   * are edited. Empty for new checkpoints.
   */
  original: Checkpoint;
  name: string;
  distanceKm: string;
  gain: string;
  loss: string;
  cutoff: string;
  crewAccess: boolean;
  dropBag: boolean;
  aid: string;
  segmentTime: string;
  stopMinutes: string;
  strategy: string;
  tasks: string;
  crewInstructions: string;
  dropBagContents: string;
  intake: IntakeDraft[];
}

// StopInput formats a stop time in seconds as minutes for an input.
const stopInput = (seconds: number): string => numberInput(seconds / 60);

// ParseStop parses a stop time in minutes into seconds.
const parseStop = (text: string): number | null => {
  const minutes = parseNumber(text);
  if (minutes === null) {
    return null;
  }
  return Math.round(minutes * 60);
};

const toDraft = (checkpoint: Checkpoint): CheckpointDraft => ({
  aid: checkpoint.aid,
  crewAccess: checkpoint.crewAccess,
  crewInstructions: checkpoint.crewInstructions,
  cutoff: hoursMinutesInput(checkpoint.cutoffSeconds),
  distanceKm: kmInput(checkpoint.distanceMeters),
  dropBag: checkpoint.dropBag,
  dropBagContents: checkpoint.dropBagContents,
  gain: numberInput(checkpoint.elevationGainMeters),
  intake: checkpoint.intake.map((intake) => ({
    key: nextKey(),
    originalQuantity: intake.quantity,
    product: intake.product,
    quantity: numberInput(intake.quantity),
  })),
  key: nextKey(),
  loss: numberInput(checkpoint.elevationLossMeters),
  name: checkpoint.name,
  original: checkpoint,
  segmentTime: hoursMinutesInput(checkpoint.plannedSegmentSeconds),
  stopMinutes: stopInput(checkpoint.plannedStopSeconds),
  strategy: checkpoint.segmentStrategy,
  tasks: checkpoint.tasks,
});

const emptyDraft = (): CheckpointDraft => toDraft(create(CheckpointSchema));

// FromDraft converts a draft into a checkpoint, or returns an error message.
const fromDraft = (draft: CheckpointDraft): Checkpoint | string => {
  const label = draft.name === "" ? "A checkpoint" : draft.name;
  // Unedited inputs keep the stored values, which they show rounded.
  const { original } = draft;
  const distance = parseEdited(
    draft.distanceKm,
    original.distanceMeters,
    kmInput,
    parseKm
  );
  const gain = parseEdited(
    draft.gain,
    original.elevationGainMeters,
    numberInput,
    parseMeters
  );
  const loss = parseEdited(
    draft.loss,
    original.elevationLossMeters,
    numberInput,
    parseMeters
  );
  const stop = parseEdited(
    draft.stopMinutes,
    original.plannedStopSeconds,
    stopInput,
    parseStop
  );
  if (distance === null || gain === null || loss === null) {
    return `${label}: distance and elevation must be positive numbers.`;
  }
  if (stop === null) {
    return `${label}: the stop time must be a number of minutes.`;
  }
  const cutoff = parseEdited(
    draft.cutoff,
    original.cutoffSeconds,
    hoursMinutesInput,
    parseDuration
  );
  const segment = parseEdited(
    draft.segmentTime,
    original.plannedSegmentSeconds,
    hoursMinutesInput,
    parseDuration
  );
  if (cutoff === null || segment === null) {
    return `${label}: times must be in the format h:mm.`;
  }
  const intake: Intake[] = [];
  for (const item of draft.intake) {
    const quantity = parseEdited(
      item.quantity,
      item.originalQuantity,
      numberInput,
      parseNumber
    );
    if (quantity === null) {
      return `${label}: intake quantities must be numbers.`;
    }
    if (item.product !== "" && quantity > 0) {
      intake.push(create(IntakeSchema, { product: item.product, quantity }));
    }
  }
  return create(CheckpointSchema, {
    aid: draft.aid,
    crewAccess: draft.crewAccess,
    crewInstructions: draft.crewInstructions,
    cutoffSeconds: cutoff,
    distanceMeters: distance,
    dropBag: draft.dropBag,
    dropBagContents: draft.dropBagContents,
    elevationGainMeters: gain,
    elevationLossMeters: loss,
    id: original.id,
    intake,
    name: draft.name.trim(),
    plannedSegmentSeconds: segment,
    plannedStopSeconds: stop,
    segmentStrategy: draft.strategy,
    tasks: draft.tasks,
  });
};

interface CheckpointViewProps {
  checkpoint: Checkpoint;
  number: number;
}

const CheckpointView: React.FC<CheckpointViewProps> = ({
  checkpoint,
  number,
}) => {
  const facts: string[] = [];
  if (
    checkpoint.elevationGainMeters > 0 ||
    checkpoint.elevationLossMeters > 0
  ) {
    facts.push(
      `+${Math.round(checkpoint.elevationGainMeters)} / −${Math.round(checkpoint.elevationLossMeters)} m`
    );
  }
  if (checkpoint.plannedSegmentSeconds > 0) {
    facts.push(`segment ${formatDuration(checkpoint.plannedSegmentSeconds)}`);
  }
  if (checkpoint.plannedStopSeconds > 0) {
    facts.push(`stop ${Math.round(checkpoint.plannedStopSeconds / 60)} min`);
  }
  return (
    <li className="flex flex-col gap-2 py-3 break-inside-avoid">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <span className="flex size-6 items-center justify-center rounded-full bg-gray-800 text-xs font-bold text-white">
          {number}
        </span>
        <span className="font-semibold text-gray-900">
          {checkpoint.name === "" ? "Unnamed checkpoint" : checkpoint.name}
        </span>
        <span className="text-sm font-medium text-gray-700 tabular-nums">
          {formatKm(checkpoint.distanceMeters)}
        </span>
        {checkpoint.cutoffSeconds > 0 && (
          <Badge className="bg-red-50 text-red-800 ring-red-200">
            cutoff {formatDuration(checkpoint.cutoffSeconds)}
          </Badge>
        )}
        {checkpoint.crewAccess && (
          <Badge className="bg-sky-50 text-sky-800 ring-sky-200">Crew</Badge>
        )}
        {checkpoint.dropBag && (
          <Badge className="bg-violet-50 text-violet-800 ring-violet-200">
            Drop bag
          </Badge>
        )}
        {facts.length > 0 && (
          <span className="text-xs text-gray-500 tabular-nums">
            {facts.join(" · ")}
          </span>
        )}
      </div>
      <div className="grid grid-cols-1 gap-2 pl-8 sm:grid-cols-2">
        <TextBlock label="Aid" text={checkpoint.aid} />
        <TextBlock label="Segment strategy" text={checkpoint.segmentStrategy} />
        <TextBlock label="Tasks" text={checkpoint.tasks} />
        <TextBlock
          label="Crew instructions"
          text={checkpoint.crewInstructions}
        />
        <TextBlock label="Drop bag" text={checkpoint.dropBagContents} />
        {checkpoint.intake.length > 0 && (
          <div>
            <p className="text-xs font-medium tracking-wide text-gray-500 uppercase">
              Fuel on the way here
            </p>
            <p className="text-sm text-gray-800">
              {checkpoint.intake
                .map(
                  (intake) =>
                    `${numberInput(intake.quantity)} × ${intake.product}`
                )
                .join(", ")}
            </p>
          </div>
        )}
      </div>
    </li>
  );
};

/**
 * The planned fuel of a segment must be within these fractions of its
 * targets, as in the plan's segment fueling rule.
 */
const MIN_FUEL_FRACTION = 0.8;
const MAX_FUEL_FRACTION = 1.3;
// Segments shorter than this aren't checked.
const MIN_FUELED_SEGMENT_SECONDS = 30 * 60;

interface SegmentFuelProps {
  draft: CheckpointDraft;
  nutrition: NutritionStrategy | undefined;
}

/**
 * SegmentFuel compares the fuel planned for a segment with the nutrition
 * targets for its planned time, as it's edited.
 */
const SegmentFuel: React.FC<SegmentFuelProps> = ({ draft, nutrition }) => {
  const seconds = parseDuration(draft.segmentTime) ?? 0;
  if (nutrition === undefined || seconds <= 0) {
    return null;
  }
  const hours = seconds / 3600;
  let carbs = 0;
  let fluid = 0;
  let sodium = 0;
  for (const item of draft.intake) {
    const quantity = parseNumber(item.quantity) ?? 0;
    const product = nutrition.products.find(
      (candidate) =>
        candidate.name.trim().toLowerCase() ===
        item.product.trim().toLowerCase()
    );
    if (product !== undefined) {
      carbs += product.carbsGrams * quantity;
      fluid += product.fluidMl * quantity;
      sodium += product.sodiumMg * quantity;
    }
  }
  const checked = seconds >= MIN_FUELED_SEGMENT_SECONDS;
  const nutrients = [
    {
      label: "Carbs",
      planned: carbs,
      target: nutrition.carbsGramsPerHour * hours,
      unit: "g",
    },
    {
      label: "Fluid",
      planned: fluid,
      target: nutrition.fluidMlPerHour * hours,
      unit: "ml",
    },
    {
      label: "Sodium",
      planned: sodium,
      target: nutrition.sodiumMgPerHour * hours,
      unit: "mg",
    },
  ].filter((nutrient) => nutrient.target > 0);
  if (nutrients.length === 0) {
    return null;
  }
  const chipClass = (planned: number, target: number): string => {
    if (!checked) {
      return "bg-gray-100 text-gray-700";
    }
    if (planned < target * MIN_FUEL_FRACTION) {
      return "bg-red-50 text-red-800 ring-1 ring-red-200";
    }
    if (planned > target * MAX_FUEL_FRACTION) {
      return "bg-amber-50 text-amber-800 ring-1 ring-amber-200";
    }
    return "bg-emerald-50 text-emerald-800 ring-1 ring-emerald-200";
  };
  return (
    <div className="flex flex-wrap items-center gap-1.5 text-xs">
      <span className="text-gray-500">
        For {formatDuration(seconds)} at the targets:
      </span>
      {nutrients.map((nutrient) => (
        <span
          key={nutrient.label}
          className={`rounded px-1.5 py-0.5 tabular-nums ${chipClass(nutrient.planned, nutrient.target)}`}
        >
          {nutrient.label} {Math.round(nutrient.planned)} /{" "}
          {Math.round(nutrient.target)} {nutrient.unit}
        </span>
      ))}
    </div>
  );
};

interface CheckpointEditorProps {
  draft: CheckpointDraft;
  products: string[];
  nutrition: NutritionStrategy | undefined;
  onChange: (draft: CheckpointDraft) => void;
}

const CheckpointEditor: React.FC<CheckpointEditorProps> = ({
  draft,
  products,
  nutrition,
  onChange,
}) => {
  const productOptions: SelectOption[] = [
    { label: "Choose a product", value: "" },
  ];
  for (const product of products) {
    productOptions.push({ label: product, value: product });
  }
  for (const intake of draft.intake) {
    if (intake.product !== "" && !products.includes(intake.product)) {
      productOptions.push({
        label: `${intake.product} (not in nutrition)`,
        value: intake.product,
      });
    }
  }
  const setIntake = (key: number, product: string, quantity: string) => {
    onChange({
      ...draft,
      intake: draft.intake.map((item) =>
        item.key === key ? { ...item, product, quantity } : item
      ),
    });
  };
  return (
    <div className="flex flex-col gap-3">
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-6">
        <TextField
          label="Name"
          value={draft.name}
          className="col-span-2"
          placeholder="CP3 Col de la Seigne"
          onChange={(value) => {
            onChange({ ...draft, name: value });
          }}
        />
        <TextField
          label="Distance (km)"
          value={draft.distanceKm}
          inputMode="decimal"
          onChange={(value) => {
            onChange({ ...draft, distanceKm: value });
          }}
        />
        <TextField
          label="Gain (m)"
          value={draft.gain}
          inputMode="numeric"
          placeholder="from course"
          onChange={(value) => {
            onChange({ ...draft, gain: value });
          }}
        />
        <TextField
          label="Loss (m)"
          value={draft.loss}
          inputMode="numeric"
          placeholder="from course"
          onChange={(value) => {
            onChange({ ...draft, loss: value });
          }}
        />
        <TextField
          label="Cutoff (h:mm)"
          value={draft.cutoff}
          placeholder="elapsed"
          onChange={(value) => {
            onChange({ ...draft, cutoff: value });
          }}
        />
        <TextField
          label="Segment time (h:mm)"
          value={draft.segmentTime}
          onChange={(value) => {
            onChange({ ...draft, segmentTime: value });
          }}
        />
        <TextField
          label="Stop (min)"
          value={draft.stopMinutes}
          inputMode="numeric"
          onChange={(value) => {
            onChange({ ...draft, stopMinutes: value });
          }}
        />
        <TextField
          label="Aid"
          value={draft.aid}
          className="col-span-2"
          placeholder="water, electrolyte, gels, soup"
          onChange={(value) => {
            onChange({ ...draft, aid: value });
          }}
        />
        <div className="col-span-2 flex items-end gap-4 pb-1.5">
          <CheckboxField
            label="Crew access"
            checked={draft.crewAccess}
            onChange={(checked) => {
              onChange({ ...draft, crewAccess: checked });
            }}
          />
          <CheckboxField
            label="Drop bag"
            checked={draft.dropBag}
            onChange={(checked) => {
              onChange({ ...draft, dropBag: checked });
            }}
          />
        </div>
      </div>
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        <TextAreaField
          label="Segment strategy"
          value={draft.strategy}
          rows={2}
          placeholder="Power hike the climb, HR under 150"
          onChange={(value) => {
            onChange({ ...draft, strategy: value });
          }}
        />
        <TextAreaField
          label="Tasks at the checkpoint"
          value={draft.tasks}
          rows={2}
          placeholder="Refill both flasks, swap socks"
          onChange={(value) => {
            onChange({ ...draft, tasks: value });
          }}
        />
        {draft.crewAccess && (
          <TextAreaField
            label="Crew instructions"
            value={draft.crewInstructions}
            rows={2}
            onChange={(value) => {
              onChange({ ...draft, crewInstructions: value });
            }}
          />
        )}
        {draft.dropBag && (
          <TextAreaField
            label="Drop bag contents"
            value={draft.dropBagContents}
            rows={2}
            onChange={(value) => {
              onChange({ ...draft, dropBagContents: value });
            }}
          />
        )}
      </div>
      <div className="flex flex-col gap-1">
        <span className="text-xs font-medium text-gray-600">
          Fuel on the segment to this checkpoint
        </span>
        {draft.intake.map((item) => (
          <div key={item.key} className="flex items-center gap-2">
            <select
              value={item.product}
              className={inputClass}
              aria-label="Product"
              onChange={(event) => {
                setIntake(item.key, event.target.value, item.quantity);
              }}
            >
              {productOptions.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
            <input
              value={item.quantity}
              inputMode="decimal"
              aria-label="Servings"
              placeholder="servings"
              className={`${inputClass} w-24`}
              onChange={(event) => {
                setIntake(item.key, item.product, event.target.value);
              }}
            />
            <button
              type="button"
              className={iconButton}
              aria-label="Remove intake"
              onClick={() => {
                onChange({
                  ...draft,
                  intake: draft.intake.filter(
                    (other) => other.key !== item.key
                  ),
                });
              }}
            >
              <XMarkIcon className="size-4" />
            </button>
          </div>
        ))}
        {products.length === 0 && (
          <p className="text-xs text-gray-500">
            Add products in the nutrition section to plan intake.
          </p>
        )}
        <SegmentFuel draft={draft} nutrition={nutrition} />
        <AddButton
          label="Add intake"
          onClick={() => {
            onChange({
              ...draft,
              intake: [
                ...draft.intake,
                {
                  key: nextKey(),
                  originalQuantity: 0,
                  product: products[0] ?? "",
                  quantity: "1",
                },
              ],
            });
          }}
        />
      </div>
    </div>
  );
};

// CheckpointsSection lists the checkpoints and the segments leading to them.
const CheckpointsSection: React.FC<SectionComponentProps> = ({
  plan,
  rules,
  onSave,
}) => {
  const edit = useSectionEdit(onSave);
  const [drafts, setDrafts] = useState<CheckpointDraft[]>([]);
  const products = (plan.nutrition?.products ?? [])
    .map((product) => product.name)
    .filter((name) => name !== "");

  const startEdit = () => {
    setDrafts(plan.checkpoints.map((checkpoint) => toDraft(checkpoint)));
    edit.start();
  };

  const submit = () => {
    const checkpoints: Checkpoint[] = [];
    for (const draft of drafts) {
      const result = fromDraft(draft);
      if (typeof result === "string") {
        edit.setValidation(result);
        return;
      }
      checkpoints.push(result);
    }
    edit.save({ ...plan, checkpoints }, "checkpoints");
  };

  const move = (index: number, delta: number) => {
    const next = [...drafts];
    const [item] = next.splice(index, 1);
    if (item === undefined) {
      return;
    }
    next.splice(index + delta, 0, item);
    setDrafts(next);
  };

  return (
    <Section
      section="checkpoints"
      title="Course & checkpoints"
      rules={rules}
      editable={onSave !== undefined}
      edit={edit}
      onStartEdit={startEdit}
      onSubmit={submit}
    >
      {edit.editing && (
        <div className="flex flex-col gap-3">
          <p className="text-xs text-gray-500">
            List checkpoints in course order, ending with the finish. Each
            checkpoint describes the segment leading to it. Leave gain and loss
            empty to use the race course.
          </p>
          {drafts.map((draft, index) => (
            <div
              key={draft.key}
              className="flex flex-col gap-2 rounded-md border border-gray-200 bg-gray-50/50 p-3"
            >
              <div className="flex items-center justify-between">
                <span className="text-sm font-semibold text-gray-700">
                  #{index + 1}
                </span>
                <ListControls
                  index={index}
                  count={drafts.length}
                  onMove={move}
                  onRemove={(position) => {
                    setDrafts(
                      drafts.filter((_draft, other) => other !== position)
                    );
                  }}
                />
              </div>
              <CheckpointEditor
                draft={draft}
                products={products}
                nutrition={plan.nutrition}
                onChange={(changed) => {
                  setDrafts(
                    drafts.map((other) =>
                      other.key === changed.key ? changed : other
                    )
                  );
                }}
              />
            </div>
          ))}
          <AddButton
            label="Add checkpoint"
            onClick={() => {
              setDrafts([...drafts, emptyDraft()]);
            }}
          />
        </div>
      )}
      {!edit.editing && plan.checkpoints.length === 0 && (
        <EmptyText>
          No checkpoints yet. Add the aid stations from the race website, ending
          with the finish.
        </EmptyText>
      )}
      {!edit.editing && plan.checkpoints.length > 0 && (
        <ol className="flex flex-col divide-y divide-gray-100">
          {plan.checkpoints.map((checkpoint, index) => (
            <CheckpointView
              key={`${checkpoint.distanceMeters}-${checkpoint.name}`}
              checkpoint={checkpoint}
              number={index + 1}
            />
          ))}
        </ol>
      )}
    </Section>
  );
};

export default CheckpointsSection;
