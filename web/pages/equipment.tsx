import { create } from "@bufbuild/protobuf";
import Loading from "@components/loading";
import Card from "@components/ui/card";
import { PageHeader } from "@components/ui/misc";
import QueryError, { ErrorText } from "@components/ui/query-error";
import { inputClass, primaryButton } from "@components/ui/styles";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { XMarkIcon } from "@heroicons/react/24/outline";
import { useQueryClient } from "@tanstack/react-query";
import type { Weight } from "gen/plans/v1/equipment_pb";
import { Equipment, WeightSchema, WeightUnit } from "gen/plans/v1/equipment_pb";
import {
  getHomeEquipment,
  setHomeEquipment,
} from "gen/plans/v1/exercises-ExerciseService_connectquery";
import type { GetHomeEquipmentResponse } from "gen/plans/v1/exercises_pb";
import { ExerciseService } from "gen/plans/v1/exercises_pb";
import { TrainingPlanService } from "gen/plans/v1/plans_pb";
import { invalidateServices } from "helpers/query";
import { EQUIPMENT_LABELS, formatWeight } from "helpers/strength";
import React, { useState } from "react";

// Owned is what the user has of a kind of equipment, as edited.
interface Owned {
  weights: Weight[];
  notes: string;
}

// WeightInput adds a weight in kilograms or pounds.
const WeightInput: React.FC<{ onAdd: (weight: Weight) => void }> = ({
  onAdd,
}) => {
  const [value, setValue] = useState("");
  const [unit, setUnit] = useState(WeightUnit.KILOGRAMS);
  const add = () => {
    const parsed = Number(value.replace(",", "."));
    if (!Number.isFinite(parsed) || parsed <= 0) {
      return;
    }
    onAdd(create(WeightSchema, { unit, value: parsed }));
    setValue("");
  };
  return (
    <span className="flex items-center gap-1">
      <span className="w-24">
        <input
          className={`${inputClass} text-base tabular-nums`}
          inputMode="decimal"
          aria-label="Weight to add"
          placeholder="Weight"
          value={value}
          onChange={(event) => {
            setValue(event.target.value);
          }}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              add();
            }
          }}
        />
      </span>
      <button
        type="button"
        aria-label="Switch between kilograms and pounds"
        title="kg or lb"
        className="min-h-9 rounded-md px-2 text-sm font-medium text-gray-700 ring-1 ring-gray-300 ring-inset"
        onClick={() => {
          setUnit((current) =>
            current === WeightUnit.POUNDS
              ? WeightUnit.KILOGRAMS
              : WeightUnit.POUNDS
          );
        }}
      >
        {unit === WeightUnit.POUNDS ? "lb" : "kg"}
      </button>
      <button
        type="button"
        className="min-h-9 rounded-md px-2.5 text-sm font-medium text-brand-600 hover:bg-brand-50"
        onClick={add}
      >
        Add
      </button>
    </span>
  );
};

interface EquipmentFormProps {
  initial: GetHomeEquipmentResponse;
}

// EquipmentForm edits the checklist of what the user has.
const EquipmentForm: React.FC<EquipmentFormProps> = ({ initial }) => {
  const queryClient = useQueryClient();
  const [owned, setOwned] = useState(
    () =>
      new Map<Equipment, Owned>(
        initial.equipment.map((item) => [
          item.equipment,
          { notes: item.notes, weights: item.weights },
        ])
      )
  );
  const [saved, setSaved] = useState(false);
  const save = useMutation(setHomeEquipment, {
    onSuccess: async () => {
      setSaved(true);
      // The equipment checks of strength workouts change too.
      await invalidateServices(queryClient, [
        ExerciseService,
        TrainingPlanService,
      ]);
    },
  });
  const update = (equipment: Equipment, change: Owned | undefined) => {
    setSaved(false);
    setOwned((current) => {
      const next = new Map(current);
      if (change === undefined) {
        next.delete(equipment);
      } else {
        next.set(equipment, change);
      }
      return next;
    });
  };
  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate({
          equipment: EQUIPMENT_LABELS.filter((item) =>
            owned.has(item.equipment)
          ).map((item) => ({
            equipment: item.equipment,
            notes: owned.get(item.equipment)?.notes ?? "",
            weights: owned.get(item.equipment)?.weights ?? [],
          })),
        });
      }}
    >
      <Card>
        <ul className="-m-4 divide-y divide-gray-100">
          {EQUIPMENT_LABELS.map((item) => {
            const have = owned.get(item.equipment);
            return (
              <li
                key={item.equipment}
                className="flex flex-col gap-2 px-4 py-3"
              >
                <label className="flex min-h-7 items-center gap-3 font-medium text-gray-900">
                  <input
                    type="checkbox"
                    className="size-5 accent-ink"
                    checked={have !== undefined}
                    onChange={(event) => {
                      update(
                        item.equipment,
                        event.target.checked
                          ? { notes: "", weights: [] }
                          : undefined
                      );
                    }}
                  />
                  {item.label}
                </label>
                {have !== undefined && item.weighted && (
                  <div className="flex flex-col gap-2 pl-8">
                    <div className="flex flex-wrap items-center gap-1.5">
                      {have.weights.map((weight) => (
                        <span
                          key={`${weight.value}-${weight.unit}`}
                          className="inline-flex items-center gap-1 rounded-full bg-violet-100 py-1 pr-1 pl-2.5 text-sm font-medium text-violet-900 tabular-nums"
                        >
                          {formatWeight(weight)}
                          <button
                            type="button"
                            aria-label={`Remove ${formatWeight(weight)}`}
                            title="Remove"
                            className="rounded-full p-0.5 hover:bg-violet-200"
                            onClick={() => {
                              update(item.equipment, {
                                ...have,
                                weights: have.weights.filter(
                                  (other) => other !== weight
                                ),
                              });
                            }}
                          >
                            <XMarkIcon className="size-4" />
                          </button>
                        </span>
                      ))}
                      <WeightInput
                        onAdd={(weight) => {
                          update(item.equipment, {
                            ...have,
                            weights: [...have.weights, weight],
                          });
                        }}
                      />
                    </div>
                    <p className="text-xs text-gray-500">
                      {item.equipment === Equipment.DUMBBELLS
                        ? "The weight of each dumbbell. "
                        : ""}
                      Leave it empty if they adjust to any weight.
                    </p>
                  </div>
                )}
                {have !== undefined && !item.weighted && (
                  <span className="pl-8">
                    <input
                      className={`${inputClass} text-sm`}
                      aria-label={`Notes on the ${item.label.toLowerCase()}`}
                      placeholder="Notes, e.g. light and medium"
                      value={have.notes}
                      onChange={(event) => {
                        update(item.equipment, {
                          ...have,
                          notes: event.target.value,
                        });
                      }}
                    />
                  </span>
                )}
              </li>
            );
          })}
        </ul>
      </Card>
      <ErrorText error={save.error} />
      <div className="flex items-center gap-3">
        <button
          type="submit"
          className={`${primaryButton} h-11 px-5`}
          disabled={save.isPending}
        >
          {save.isPending ? "Saving…" : "Save"}
        </button>
        {saved && <span className="text-sm text-emerald-700">Saved.</span>}
      </div>
    </form>
  );
};

/**
 * EquipmentPage lists the equipment the user has at home, which strength
 * workouts are planned with and checked against.
 */
const EquipmentPage: React.FC = () => {
  const { isPending, isError, data, error, refetch } = useQuery(
    getHomeEquipment,
    {}
  );
  return (
    <div className="mx-auto flex w-full max-w-2xl flex-col">
      <PageHeader
        title="Home equipment"
        subtitle="What you have to train with. Strength workouts are planned with it, and warn you when an exercise needs something you don't have."
      />
      {isPending && <Loading />}
      {isError && (
        <QueryError
          error={error}
          onRetry={() => {
            void refetch();
          }}
        />
      )}
      {data !== undefined && <EquipmentForm initial={data} />}
    </div>
  );
};

export default EquipmentPage;
