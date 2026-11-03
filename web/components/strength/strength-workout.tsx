import Card from "@components/ui/card";
import ConfirmButton from "@components/ui/confirm-button";
import { Badge, EmptyState } from "@components/ui/misc";
import { ErrorText } from "@components/ui/query-error";
import {
  ghostDangerButton,
  linkClass,
  primaryButton,
} from "@components/ui/styles";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { ChevronRightIcon, PlayIcon } from "@heroicons/react/24/solid";
import { useQueryClient } from "@tanstack/react-query";
import { getGarminConnection } from "gen/garmin/v1/garmin-GarminService_connectquery";
import type { Exercise } from "gen/plans/v1/exercises_pb";
import { deleteStrengthLog } from "gen/plans/v1/plans-TrainingPlanService_connectquery";
import type { StrengthWorkout, Workout } from "gen/plans/v1/plans_pb";
import { SessionEffort, TrainingPlanService } from "gen/plans/v1/plans_pb";
import { invalidateServices } from "helpers/query";
import {
  feedbackStyles,
  formatDose,
  formatSet,
  groupExercises,
  muscleLabel,
  sessionEffortLabel,
  workoutMuscles,
} from "helpers/strength";
import type { MuscleSet } from "helpers/strength";
import React from "react";
import { Link } from "react-router";

import BodyMap, { MuscleChips } from "./body-map";
import EquipmentCheck from "./equipment-check";

interface StrengthWorkoutViewProps {
  workout: Workout;
  strength: StrengthWorkout;
  // The user's exercises by ID.
  exercises: Map<string, Exercise>;
}

/**
 * StrengthWorkoutView shows a strength workout's exercises and the muscles
 * they work, with a button to start it on the phone, and its log once done.
 */
const StrengthWorkoutView: React.FC<StrengthWorkoutViewProps> = ({
  workout,
  strength,
  exercises,
}) => {
  const queryClient = useQueryClient();
  const garmin = useQuery(getGarminConnection);
  /*
   * With Garmin syncing workouts, the workout is put on the user's Garmin
   * calendar, so the file is only for other watches.
   */
  const garminSyncs =
    garmin.data?.connection?.connected === true &&
    garmin.data.connection.workoutsAllowed;
  const removeLog = useMutation(deleteStrengthLog, {
    onSuccess: async () => {
      await invalidateServices(queryClient, [TrainingPlanService]);
    },
  });
  const planned = strength.exercises;
  const library = planned
    .map((item) => exercises.get(item.exerciseId))
    .filter((exercise) => exercise !== undefined);
  const muscles = workoutMuscles(library);
  const sessionPath = `/workouts/${workout.id}/session`;
  const { log } = strength;

  if (planned.length === 0) {
    return (
      <EmptyState title="No exercises yet">
        Plan the exercises of this workout through the MCP server, from your
        exercise library.
      </EmptyState>
    );
  }
  return (
    <div className="flex flex-col gap-4">
      <Link
        to={sessionPath}
        className={`${primaryButton} h-12 text-base sm:self-start sm:px-6`}
      >
        <PlayIcon className="size-5" />
        {log === undefined ? "Start workout" : "Edit the log"}
      </Link>
      {garminSyncs ? (
        <p className="-mt-2 text-sm text-gray-600">
          Or do it on your watch: it's put on your Garmin calendar two weeks
          ahead, with the sets, reps, weights and rest.
        </p>
      ) : (
        <p className="-mt-2 text-sm text-gray-600">
          Or{" "}
          <a
            href={`/workouts/${workout.id}.fit`}
            download
            className={linkClass}
            title="A FIT workout with the sets, reps, weights and rest"
          >
            download it for your watch
          </a>
          : import it in Garmin Connect, or copy it to the GARMIN/NewFiles
          folder of the watch. Or connect Garmin in the settings to put your
          workouts on your Garmin calendar.
        </p>
      )}
      <EquipmentCheck
        workout={workout}
        strength={strength}
        exercises={exercises}
      />
      {log !== undefined && (
        <Card
          title="Workout log"
          actions={
            <ConfirmButton
              className={ghostDangerButton}
              title="Delete the log?"
              message="The workout will no longer be complete, and its sets are removed from the history of its exercises."
              disabled={removeLog.isPending}
              onConfirm={() => {
                removeLog.mutate({ workoutId: workout.id });
              }}
            >
              Delete log
            </ConfirmButton>
          }
        >
          <div className="flex flex-col gap-3">
            {log.effort !== SessionEffort.UNSPECIFIED && (
              <p className="text-sm text-gray-700">
                The session felt{" "}
                <span className="font-semibold">
                  {sessionEffortLabel(log.effort).toLowerCase()}
                </span>
                .
              </p>
            )}
            {log.notes !== "" && (
              <p className="text-sm whitespace-pre-line text-gray-700">
                {log.notes}
              </p>
            )}
            <ul className="divide-y divide-gray-100">
              {log.exercises.map((logged, index) => (
                /*
                 * A log may list an exercise twice, so the index is part of
                 * the key.
                 */
                <li
                  // oxlint-disable-next-line no-array-index-key
                  key={`${logged.exerciseId}-${index}`}
                  className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1 py-2"
                >
                  <span className="text-sm font-medium text-gray-900">
                    {logged.exerciseName}
                  </span>
                  <span className="text-sm text-gray-700 tabular-nums">
                    {logged.sets.length === 0
                      ? "No sets"
                      : logged.sets.map(formatSet).join(", ")}
                  </span>
                  {feedbackStyles(logged.effort, logged.volume).length > 0 && (
                    <span className="flex w-full flex-wrap gap-1">
                      {feedbackStyles(logged.effort, logged.volume).map(
                        (style) => (
                          <Badge key={style.label} className={style.badgeClass}>
                            {style.label}
                          </Badge>
                        )
                      )}
                    </span>
                  )}
                  {logged.notes !== "" && (
                    <span className="w-full text-xs text-gray-600">
                      {logged.notes}
                    </span>
                  )}
                </li>
              ))}
            </ul>
            <ErrorText error={removeLog.error} />
          </div>
        </Card>
      )}
      <Card title="Muscles worked">
        <div className="flex flex-col gap-3">
          <BodyMap muscles={muscles} />
          <MuscleChips muscles={muscles} />
          <p className="text-xs text-gray-500">
            Dark: worked hardest. Light: also worked.
          </p>
        </div>
      </Card>
      {groupExercises(planned).map((group) => (
        <Card
          key={group.items[0]?.index ?? 0}
          title={group.name === "" ? "Exercises" : group.name}
        >
          <ul className="-m-4 divide-y divide-gray-100">
            {group.items.map(({ item, index }) => {
              const exercise = exercises.get(item.exerciseId);
              const muscleNames =
                exercise === undefined ? undefined : workoutMuscles([exercise]);
              return (
                <li key={index}>
                  <Link
                    to={`${sessionPath}?step=${index}`}
                    className="flex min-h-11 items-center gap-3 px-4 py-3 hover:bg-gray-50"
                  >
                    <span className="flex size-7 shrink-0 items-center justify-center rounded-full bg-gray-100 text-xs font-semibold text-gray-700">
                      {index + 1}
                    </span>
                    <span className="flex min-w-0 grow flex-col gap-0.5">
                      <span className="font-medium text-gray-900">
                        {item.exerciseName}
                      </span>
                      <span className="text-sm text-gray-700 tabular-nums">
                        {formatDose(item, exercise?.eachSide ?? false)}
                      </span>
                      {muscleNames !== undefined && (
                        <MuscleLine muscles={muscleNames} />
                      )}
                    </span>
                    <ChevronRightIcon className="size-5 shrink-0 text-gray-400" />
                  </Link>
                </li>
              );
            })}
          </ul>
        </Card>
      ))}
    </div>
  );
};

// MuscleLine lists an exercise's muscles in a line.
const MuscleLine: React.FC<{ muscles: MuscleSet }> = ({ muscles }) => {
  const parts = [muscles.primary, muscles.secondary]
    .filter((group) => group.length > 0)
    .map((group) => group.map(muscleLabel).join(", "));
  if (parts.length === 0) {
    return null;
  }
  return <span className="text-xs text-gray-500">{parts.join(" · ")}</span>;
};

export default StrengthWorkoutView;
