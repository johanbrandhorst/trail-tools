import { create } from "@bufbuild/protobuf";
import Card from "@components/ui/card";
import { ErrorText } from "@components/ui/query-error";
import {
  linkClass,
  primaryButton,
  secondaryButton,
} from "@components/ui/styles";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import {
  CheckIcon,
  ExclamationTriangleIcon,
} from "@heroicons/react/24/outline";
import { useQueryClient } from "@tanstack/react-query";
import type { Equipment, Weight } from "gen/plans/v1/equipment_pb";
import { getHomeEquipment } from "gen/plans/v1/exercises-ExerciseService_connectquery";
import type { Exercise } from "gen/plans/v1/exercises_pb";
import { updateWorkout } from "gen/plans/v1/plans-TrainingPlanService_connectquery";
import type { StrengthWorkout, Workout } from "gen/plans/v1/plans_pb";
import {
  PlannedExerciseSchema,
  StrengthWorkoutSchema,
  TrainingPlanService,
} from "gen/plans/v1/plans_pb";
import { invalidateServices } from "helpers/query";
import {
  equipmentLabel,
  formatWeight,
  hasWeight,
  isWeighted,
} from "helpers/strength";
import React from "react";
import { Link } from "react-router";

interface EquipmentCheckProps {
  workout: Workout;
  strength: StrengthWorkout;
  // The user's exercises by ID.
  exercises: Map<string, Exercise>;
}

/**
 * Needed is a kind of equipment the workout needs, and whether the user has
 * it.
 */
interface Needed {
  equipment: Equipment;
  // The planned weights of it, e.g. "10 kg, 12 kg".
  weights: string;
  missing: boolean;
}

/**
 * EquipmentCheck lists the equipment a strength workout needs, ticking off
 * what the user has at home, and offers the closest weights they have for
 * weights they don't.
 */
const EquipmentCheck: React.FC<EquipmentCheckProps> = ({
  workout,
  strength,
  exercises,
}) => {
  const queryClient = useQueryClient();
  const home = useQuery(getHomeEquipment, {});
  const swap = useMutation(updateWorkout, {
    onSuccess: async () => {
      await invalidateServices(queryClient, [TrainingPlanService]);
    },
  });

  const needed: Needed[] = [];
  for (const [index, planned] of strength.exercises.entries()) {
    for (const equipment of exercises.get(planned.exerciseId)?.equipment ??
      []) {
      let entry = needed.find((item) => item.equipment === equipment);
      if (entry === undefined) {
        entry = { equipment, missing: false, weights: "" };
        needed.push(entry);
      }
      if (
        strength.equipmentIssues.some(
          (issue) =>
            issue.exerciseIndex === index && issue.equipment === equipment
        )
      ) {
        entry.missing = true;
      }
      // Weights are of the dumbbells, kettlebell or medicine ball.
      if (isWeighted(equipment) && hasWeight(planned.weight)) {
        const weight = formatWeight(planned.weight);
        if (!entry.weights.split(", ").includes(weight)) {
          entry.weights =
            entry.weights === "" ? weight : `${entry.weights}, ${weight}`;
        }
      }
    }
  }
  if (needed.length === 0) {
    return null;
  }
  // Swaps the planned weight of an exercise for one the user has.
  const swapWeight = (index: number, weight: Weight) => {
    swap.mutate({
      updateMask: { paths: ["strength"] },
      workout: {
        details: {
          case: "strength",
          value: create(StrengthWorkoutSchema, {
            exercises: strength.exercises.map((planned, position) =>
              create(PlannedExerciseSchema, {
                ...planned,
                weight: position === index ? weight : planned.weight,
              })
            ),
            groups: strength.groups,
            targetDurationSeconds: strength.targetDurationSeconds,
          }),
        },
        id: workout.id,
      },
    });
  };
  const homeSet = home.data?.updateTime !== undefined;

  return (
    <div className="flex flex-col gap-4">
      {strength.equipmentIssues.length > 0 && (
        <section className="flex flex-col gap-3 rounded-lg border border-amber-300 bg-amber-50 p-4">
          <h2 className="flex items-center gap-2 font-semibold text-amber-900">
            <ExclamationTriangleIcon className="size-5 shrink-0" />
            {strength.equipmentIssues.length === 1
              ? "An exercise needs equipment you don't have"
              : "Some exercises need equipment you don't have"}
          </h2>
          <ul className="flex flex-col gap-3">
            {strength.equipmentIssues.map((issue) => (
              <li
                key={`${issue.exerciseIndex}-${issue.equipment}`}
                className="flex flex-col gap-2 pl-7 text-sm text-amber-900"
              >
                <span>{issue.description}.</span>
                {issue.closestWeights.length > 0 && (
                  <span className="flex flex-wrap gap-2">
                    {issue.closestWeights.map((weight, position) => (
                      <button
                        key={`${weight.value}-${weight.unit}`}
                        type="button"
                        className={`${position === 0 ? primaryButton : secondaryButton} h-10`}
                        disabled={swap.isPending}
                        onClick={() => {
                          swapWeight(issue.exerciseIndex, weight);
                        }}
                      >
                        Use {formatWeight(weight)}
                      </button>
                    ))}
                  </span>
                )}
              </li>
            ))}
          </ul>
          <ErrorText error={swap.error} />
        </section>
      )}
      <Card title="Equipment">
        <div className="flex flex-col gap-3">
          <ul className="flex flex-col gap-2">
            {needed.map((item) => (
              <li
                key={item.equipment}
                className="flex items-center gap-2.5 text-sm"
              >
                {homeSet && (
                  <span
                    className={`flex size-5.5 shrink-0 items-center justify-center rounded-full ${
                      item.missing
                        ? "bg-amber-100 text-amber-700"
                        : "bg-emerald-100 text-emerald-800"
                    }`}
                  >
                    {item.missing ? (
                      <ExclamationTriangleIcon className="size-3.5" />
                    ) : (
                      <CheckIcon className="size-3.5" />
                    )}
                    <span className="sr-only">
                      {item.missing ? "Missing" : "You have it"}
                    </span>
                  </span>
                )}
                <span className="grow text-gray-900">
                  {equipmentLabel(item.equipment)}
                </span>
                <span className="text-xs text-gray-500 tabular-nums">
                  {item.weights}
                </span>
              </li>
            ))}
          </ul>
          {!homeSet && home.data !== undefined && (
            <p className="text-sm text-gray-600">
              <Link to="/equipment" className={linkClass}>
                Add your home equipment
              </Link>{" "}
              to check you have everything.
            </p>
          )}
        </div>
      </Card>
    </div>
  );
};

export default EquipmentCheck;
