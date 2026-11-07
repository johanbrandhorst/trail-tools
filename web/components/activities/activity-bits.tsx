import BodyMap from "@components/strength/body-map";
import { useQuery } from "@connectrpc/connect-query";
import { skipToken } from "@tanstack/react-query";
import type { Activity } from "gen/activities/v1/activities_pb";
import { listExercises } from "gen/plans/v1/exercises-ExerciseService_connectquery";
import { listWorkouts } from "gen/plans/v1/plans-TrainingPlanService_connectquery";
import { formatHoursMinutes, formatKm } from "helpers/format";
import { sportLabel } from "helpers/labels";
import { decodePolyline } from "helpers/polyline";
import type { MuscleSet } from "helpers/strength";
import { workoutMuscles } from "helpers/strength";
import React, { useMemo } from "react";
import { Link } from "react-router";

/*
 * STRENGTH_SPORTS are the sport types of strength training, as the server
 * links them to strength workouts.
 */
const STRENGTH_SPORTS = new Set([
  "WeightTraining",
  "Workout",
  "Crossfit",
  "HighIntensityIntervalTraining",
]);

// IsStrength reports whether an activity is strength training.
export const isStrength = (activity: Activity): boolean =>
  STRENGTH_SPORTS.has(activity.sportType);

/*
 * MIN_DISTANCE_METERS is the distance below which an activity, like strength
 * training, is shown by its time rather than its distance.
 */
const MIN_DISTANCE_METERS = 50;

// HasDistance reports whether an activity went anywhere.
export const hasDistance = (activity: Activity): boolean =>
  activity.distanceMeters >= MIN_DISTANCE_METERS;

interface ActivityChipProps {
  activity: Activity;
}

// ActivityChip is a compact link to an activity, used in calendars.
export const ActivityChip: React.FC<ActivityChipProps> = ({ activity }) => (
  <Link
    to={`/activities/${activity.id}`}
    title={`${activity.name} (${sportLabel(activity.sportType)}, ${formatHoursMinutes(activity.movingTimeSeconds)})`}
    className="flex flex-col rounded border border-gray-300 bg-white px-1.5 py-0.5 text-[11px] leading-tight text-gray-700 hover:border-brand-500 hover:bg-brand-50"
  >
    <span className="truncate font-medium">{activity.name}</span>
    <span className="tabular-nums text-gray-500">
      {hasDistance(activity) ? (
        <>
          {formatKm(activity.distanceMeters)}
          {activity.elevationGainMeters > 0 &&
            ` · ${Math.round(activity.elevationGainMeters)} m`}
        </>
      ) : (
        formatHoursMinutes(activity.movingTimeSeconds)
      )}
    </span>
  </Link>
);

interface RouteThumbnailProps {
  polyline: string;
  size?: number;
}

// RouteThumbnail draws an encoded polyline as a small SVG.
export const RouteThumbnail: React.FC<RouteThumbnailProps> = ({
  polyline,
  size = 48,
}) => {
  const points = decodePolyline(polyline);
  if (points.length < 2) {
    return (
      <div
        className="rounded bg-gray-100"
        style={{ height: size, width: size }}
      />
    );
  }
  let minLat = Infinity;
  let maxLat = -Infinity;
  let minLng = Infinity;
  let maxLng = -Infinity;
  for (const [lat, lng] of points) {
    minLat = Math.min(minLat, lat);
    maxLat = Math.max(maxLat, lat);
    minLng = Math.min(minLng, lng);
    maxLng = Math.max(maxLng, lng);
  }
  /**
   * Scale longitude by the cosine of the latitude for a rough equal-area
   * projection.
   */
  const scaleLng = Math.cos((((minLat + maxLat) / 2) * Math.PI) / 180);
  const spanX = Math.max((maxLng - minLng) * scaleLng, 1e-6);
  const spanY = Math.max(maxLat - minLat, 1e-6);
  const span = Math.max(spanX, spanY);
  // Room for the start dot at the edges.
  const padding = 4;
  const inner = size - padding * 2;
  const offsetX = (inner - (spanX / span) * inner) / 2;
  const offsetY = (inner - (spanY / span) * inner) / 2;
  const coords = points.map(([lat, lng]): [number, number] => [
    padding + offsetX + (((lng - minLng) * scaleLng) / span) * inner,
    padding + offsetY + ((maxLat - lat) / span) * inner,
  ]);
  const path = coords
    .map(([xValue, yValue]) => `${xValue.toFixed(1)},${yValue.toFixed(1)}`)
    .join(" ");
  const [startX, startY] = coords[0] ?? [0, 0];
  return (
    <svg
      width={size}
      height={size}
      className="shrink-0 rounded bg-stone-100"
      aria-hidden="true"
    >
      <polyline
        points={path}
        fill="none"
        stroke="#292524"
        strokeWidth={1.5}
        strokeLinejoin="round"
      />
      <circle cx={startX} cy={startY} r={2.5} className="fill-ember" />
    </svg>
  );
};

/**
 * UseActivityMuscles returns the muscles the strength workouts completed by
 * some activities worked, by activity ID. Activities aren't linked to
 * workouts, but workouts are to the activity that completed them, so it
 * lists the workouts of the days of the strength activities.
 */
export const useActivityMuscles = (
  activities: Activity[]
): Map<string, MuscleSet> => {
  const dates = activities
    .filter((activity) => isStrength(activity))
    .map((activity) => activity.startDateLocal);
  // Dates are YYYY-MM-DD, which sort as text.
  dates.sort();
  const [first] = dates;
  const last = dates.at(-1);
  const workouts = useQuery(
    listWorkouts,
    first === undefined || last === undefined
      ? skipToken
      : { endDate: last, startDate: first }
  );
  const exercises = useQuery(
    listExercises,
    first === undefined ? skipToken : {}
  );
  return useMemo(() => {
    const result = new Map<string, MuscleSet>();
    const library = new Map(
      (exercises.data?.exercises ?? []).map((exercise) => [
        exercise.id,
        exercise,
      ])
    );
    for (const workout of workouts.data?.workouts ?? []) {
      if (
        workout.completedActivityId === "" ||
        workout.details.case !== "strength"
      ) {
        continue;
      }
      const worked = workout.details.value.exercises.flatMap((planned) => {
        const exercise = library.get(planned.exerciseId);
        return exercise === undefined ? [] : [exercise];
      });
      result.set(workout.completedActivityId, workoutMuscles(worked));
    }
    return result;
  }, [workouts.data, exercises.data]);
};

interface ActivityThumbnailProps {
  activity: Activity;
  // The muscles the activity's strength workout worked, if known.
  muscles?: MuscleSet | undefined;
  size?: number;
}

/**
 * ActivityThumbnail shows an activity at a glance: its route, or for
 * strength training, the muscles its workout worked on a small figure.
 */
export const ActivityThumbnail: React.FC<ActivityThumbnailProps> = ({
  activity,
  muscles,
  size = 48,
}) => {
  if (
    !isStrength(activity) ||
    decodePolyline(activity.summaryPolyline).length > 1
  ) {
    return <RouteThumbnail polyline={activity.summaryPolyline} size={size} />;
  }
  return (
    <div
      className="flex shrink-0 items-center justify-center rounded bg-violet-50 p-0.5"
      style={{ height: size, width: size }}
    >
      <BodyMap
        compact
        className="h-full"
        muscles={muscles ?? { primary: [], secondary: [] }}
      />
    </div>
  );
};
