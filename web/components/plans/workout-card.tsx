import { activitySummary } from "@components/activities/activity-bits";
import { useAerobicThreshold } from "@components/lactate/use-aerobic-threshold";
import { CheckCircleIcon, MapIcon } from "@heroicons/react/24/solid";
import type { Activity } from "gen/activities/v1/activities_pb";
import type { Workout, WorkoutWeather } from "gen/plans/v1/plans_pb";
import { WorkoutType } from "gen/plans/v1/plans_pb";
import { formatHoursMinutes } from "helpers/format";
import { styleOfWorkout } from "helpers/labels";
import { isCompleted, startWindow, workoutTargets } from "helpers/workouts";
import React from "react";

import { WarningIcon, WeatherBadge } from "./workout-weather";

interface WorkoutCardProps {
  workout: Workout;
  // The forecast for the workout's day, if there is one.
  weather?: WorkoutWeather | undefined;
  /**
   * The activity that completed the workout, shown in its card rather than
   * as an activity of its own, so the plan and what was done read as one.
   */
  activity?: Activity | undefined;
  onClick?: (() => void) | undefined;
}

/**
 * Targets formats the workout's targets, with the aerobic threshold heart
 * rate as the cap of base workouts, if there is one.
 */
const targets = (workout: Workout, capBpm: number | undefined): string => {
  const parts: string[] = [];
  const target = workoutTargets(workout);
  if (target.distanceMeters > 0) {
    parts.push(`${(target.distanceMeters / 1000).toFixed(1)} km`);
  }
  if (target.elevationGainMeters > 0) {
    parts.push(`${Math.round(target.elevationGainMeters)} m`);
  }
  if (target.durationSeconds > 0 && parts.length < 2) {
    parts.push(formatHoursMinutes(target.durationSeconds));
  }
  if (workout.details.case === "strength" && parts.length < 2) {
    const count = workout.details.value.exercises.length;
    if (count > 0) {
      parts.push(`${count} ${count === 1 ? "exercise" : "exercises"}`);
    }
  }
  if (workout.type === WorkoutType.BASE && capBpm !== undefined) {
    parts.push(`<${capBpm} bpm`);
  }
  return parts.join(" · ");
};

// WorkoutCard is a compact, color coded workout in a calendar day.
const WorkoutCard: React.FC<WorkoutCardProps> = ({
  workout,
  weather,
  activity,
  onClick,
}) => {
  const style = styleOfWorkout(workout);
  const threshold = useAerobicThreshold();
  const detail = [
    startWindow(workout),
    targets(workout, threshold?.heartRateBpm),
  ]
    .filter((part) => part !== "")
    .join(" · ");
  // The weather during the workout, if it has a start time.
  const forecastDay = weather?.workout ?? weather?.day;
  const content = (
    <>
      <span className="flex items-start justify-between gap-1">
        <span className="line-clamp-2 font-medium">
          {workout.title === "" ? style.label : workout.title}
        </span>
        <span className="flex shrink-0 items-center gap-0.5">
          {workoutTargets(workout).routeId !== "" && (
            <MapIcon className="size-3.5 opacity-70" aria-label="Has route" />
          )}
          {isCompleted(workout) && activity === undefined && (
            <CheckCircleIcon
              className="size-3.5 text-emerald-600"
              aria-label="Completed"
            />
          )}
        </span>
      </span>
      {detail !== "" && (
        <span className="tabular-nums opacity-80">{detail}</span>
      )}
      {activity === undefined ? (
        forecastDay !== undefined && (
          <span className="flex items-center gap-1">
            <WeatherBadge day={forecastDay} />
            <WarningIcon warnings={weather?.warnings ?? []} />
          </span>
        )
      ) : (
        <span
          className="mt-0.5 flex items-center gap-0.5 font-medium text-emerald-700 tabular-nums"
          title={activity.name}
        >
          <CheckCircleIcon className="size-3.5 shrink-0" aria-label="Done" />
          {activitySummary(activity)}
        </span>
      )}
    </>
  );
  const className = `flex w-full flex-col rounded border-l-4 border px-1.5 py-1 text-left text-[11px] leading-tight ${style.cardClass}`;
  if (onClick === undefined) {
    return (
      <div className={className} title={workout.description}>
        {content}
      </div>
    );
  }
  return (
    <button
      type="button"
      className={`${className} hover:shadow`}
      title={workout.description}
      onClick={onClick}
    >
      {content}
    </button>
  );
};

export default WorkoutCard;
