import { ActivityChip } from "@components/activities/activity-bits";
import { PlusIcon, TrophyIcon } from "@heroicons/react/24/outline";
import type { Activity } from "gen/activities/v1/activities_pb";
import type { Workout, WorkoutWeather } from "gen/plans/v1/plans_pb";
import type { Race } from "gen/races/v1/races_pb";
import { WEEKDAY_NAMES, isWithin, parseDate, today } from "helpers/dates";
import React from "react";

import WorkoutCard from "./workout-card";

interface WeekDaysProps {
  // The seven dates of the week, Monday first.
  days: string[];
  workoutsByDate: Map<string, Workout[]>;
  activitiesByDate: Map<string, Activity[]>;
  goalRacesByDate: Map<string, Race>;
  // Days outside this range are shown dimmed and cannot have workouts.
  rangeStart: string;
  rangeEnd: string;
  // Days outside the month are dimmed but still editable.
  monthStart?: string;
  monthEnd?: string;
  // The forecasts of workouts by workout ID.
  weather?: Map<string, WorkoutWeather>;
  onAddWorkout?: (date: string) => void;
  onOpenWorkout?: (workout: Workout) => void;
  // Hide days with nothing on them on small screens, where days are stacked.
  hideEmptyDays?: boolean;
}

/**
 * WeekDays shows a week as seven day columns with the planned workouts and
 * the activities actually done. On small screens days are stacked.
 */
const WeekDays: React.FC<WeekDaysProps> = ({
  days,
  workoutsByDate,
  activitiesByDate,
  goalRacesByDate,
  rangeStart,
  rangeEnd,
  monthStart,
  monthEnd,
  weather,
  onAddWorkout,
  onOpenWorkout,
  hideEmptyDays = false,
}) => {
  const now = today();
  return (
    <div className="grid grid-cols-1 gap-1 sm:grid-cols-7">
      {days.map((day, index) => {
        const inPlan = isWithin(day, rangeStart, rangeEnd);
        const inMonth =
          monthStart === undefined ||
          monthEnd === undefined ||
          isWithin(day, monthStart, monthEnd);
        const race = goalRacesByDate.get(day);
        const workouts = workoutsByDate.get(day) ?? [];
        const dayActivities = activitiesByDate.get(day) ?? [];
        // Activities that completed a workout are shown in its card.
        const completing = new Map(
          dayActivities
            .filter((activity) =>
              workouts.some(
                (workout) => workout.completedActivityId === activity.id
              )
            )
            .map((activity) => [activity.id, activity])
        );
        const activities = dayActivities.filter(
          (activity) => !completing.has(activity.id)
        );
        const isToday = day === now;
        const empty =
          race === undefined &&
          workouts.length === 0 &&
          dayActivities.length === 0;
        let cellClass = "border-gray-200 bg-white";
        if (race !== undefined) {
          cellClass = "border-yellow-400 bg-yellow-50";
        } else if (!inPlan) {
          cellClass = "border-gray-100 bg-gray-50";
        }
        return (
          <div
            key={day}
            className={`group min-h-24 flex-col gap-1 rounded-md border p-1 ${cellClass} ${inMonth && inPlan ? "" : "opacity-60"} ${isToday ? "ring-2 ring-brand-500" : ""} ${hideEmptyDays && empty && !isToday ? "hidden sm:flex" : "flex"}`}
          >
            <div className="flex items-center justify-between text-[11px] text-gray-500">
              <span className={isToday ? "font-bold text-brand-600" : ""}>
                {WEEKDAY_NAMES[index]} {parseDate(day).getDate()}
                {isToday && " · Today"}
              </span>
              {onAddWorkout !== undefined && inPlan && (
                <button
                  type="button"
                  className="rounded p-0.5 text-gray-400 hover:bg-gray-100 hover:text-gray-800 sm:opacity-0 sm:group-hover:opacity-100 sm:focus:opacity-100"
                  aria-label={`Add workout on ${day}`}
                  onClick={() => {
                    onAddWorkout(day);
                  }}
                >
                  <PlusIcon className="size-3.5" />
                </button>
              )}
            </div>
            {race !== undefined && (
              <div className="flex items-center gap-1 rounded bg-yellow-200 px-1 py-0.5 text-[11px] font-semibold text-yellow-900">
                <TrophyIcon className="size-3.5 shrink-0" />
                <span className="truncate">{race.name}</span>
              </div>
            )}
            {workouts.map((workout) => (
              <WorkoutCard
                key={workout.id}
                workout={workout}
                weather={weather?.get(workout.id)}
                activity={completing.get(workout.completedActivityId)}
                onClick={
                  onOpenWorkout === undefined
                    ? undefined
                    : () => {
                        onOpenWorkout(workout);
                      }
                }
              />
            ))}
            {activities.length > 0 && (
              <div className="mt-auto flex flex-col gap-0.5 pt-1">
                {activities.map((activity) => (
                  <ActivityChip key={activity.id} activity={activity} />
                ))}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
};

export default WeekDays;
