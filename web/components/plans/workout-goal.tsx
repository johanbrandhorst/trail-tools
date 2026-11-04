import ElevationProfile from "@components/charts/elevation-profile";
import type { AerobicThreshold } from "@components/lactate/lactate-utils";
import { FitBounds, PointMarker, PolylineLayer } from "@components/map/layers";
import MapView from "@components/map/map-view";
import { toTuples } from "@components/routes/route-geometry";
import Card from "@components/ui/card";
import { linkClass } from "@components/ui/styles";
import { Terrain } from "gen/geo/v1/geo_pb";
import type { Workout } from "gen/plans/v1/plans_pb";
import { WorkoutType } from "gen/plans/v1/plans_pb";
import type { RoutePoint, RouteStats } from "gen/routes/v1/routes_pb";
import { formatDate, formatHoursMinutes, formatKm } from "helpers/format";
import { terrainStyle } from "helpers/labels";
import { workoutTargets } from "helpers/workouts";
import React, { useMemo, useState } from "react";
import { Link } from "react-router";

// Paragraphs splits text into paragraphs at blank lines.
const Paragraphs: React.FC<{ text: string }> = ({ text }) =>
  text
    .split(/\n\s*\n/u)
    .filter((paragraph) => paragraph.trim() !== "")
    .map((paragraph) => (
      <p key={paragraph} className="whitespace-pre-line">
        {paragraph}
      </p>
    ));

interface GoalProps {
  workout: Workout;
  /**
   * The aerobic threshold base workouts stay below, from the user's latest
   * lactate test. Undefined if they have none, or for a shared workout,
   * whose viewer isn't the user.
   */
  threshold?: AerobicThreshold | undefined;
}

// Goal shows what the workout is for and its targets.
export const Goal: React.FC<GoalProps> = ({ workout, threshold }) => {
  const capped = workout.type === WorkoutType.BASE && threshold !== undefined;
  const targets: { label: string; value: string }[] = [];
  const planned = workoutTargets(workout);
  if (planned.distanceMeters > 0) {
    targets.push({
      label: "Distance",
      value: formatKm(planned.distanceMeters),
    });
  }
  if (planned.elevationGainMeters > 0) {
    targets.push({
      label: "Elevation gain",
      value: `${Math.round(planned.elevationGainMeters).toLocaleString()} m`,
    });
  }
  if (planned.durationSeconds > 0) {
    targets.push({
      label: "Time",
      value: formatHoursMinutes(planned.durationSeconds),
    });
  }
  if (workout.details.case === "strength") {
    const count = workout.details.value.exercises.length;
    if (count > 0) {
      targets.push({ label: "Exercises", value: String(count) });
    }
  }
  if (planned.terrain !== Terrain.UNSPECIFIED) {
    targets.push({
      label: "Terrain",
      value: terrainStyle(planned.terrain).label,
    });
  }
  if (capped) {
    targets.push({
      label: "Heart rate",
      value: `Under ${threshold.heartRateBpm} bpm`,
    });
  }
  return (
    <Card title="Goal">
      <div className="flex flex-col gap-4">
        {targets.length > 0 && (
          <dl className="grid grid-cols-2 gap-3 sm:grid-cols-4">
            {targets.map((target) => (
              <div key={target.label}>
                <dt className="text-xs font-medium tracking-wide text-gray-500 uppercase">
                  {target.label}
                </dt>
                <dd className="text-lg font-semibold text-gray-900 tabular-nums">
                  {target.value}
                </dd>
              </div>
            ))}
          </dl>
        )}
        {capped && (
          <p className="text-sm text-gray-600">
            Your aerobic threshold, from your{" "}
            <Link
              to={`/lactate-tests/${threshold.test.id}`}
              className={linkClass}
            >
              lactate test on {formatDate(threshold.test.date)}
            </Link>
            . Keeping below it keeps the workout easy.
          </p>
        )}
        {workout.description.trim() === "" ? (
          targets.length === 0 && (
            <p className="text-sm text-gray-500">No goal set.</p>
          )
        ) : (
          <div className="flex flex-col gap-3 leading-7 text-gray-800">
            <Paragraphs text={workout.description} />
          </div>
        )}
      </div>
    </Card>
  );
};

interface RouteCardProps {
  name: string;
  points: RoutePoint[];
  stats?: RouteStats | undefined;
  // Where the route's name links to. Empty for no link.
  link: string;
  // Changes when the route does, to fit the map to it again.
  fitKey: string;
  // Buttons shown with the route, like downloads.
  children?: React.ReactNode;
}

// RouteCard shows a workout's route on a map with its elevation profile.
export const RouteCard: React.FC<RouteCardProps> = ({
  name,
  points,
  stats,
  link,
  fitKey,
  children,
}) => {
  const tuples = useMemo(() => toTuples(points), [points]);
  const [start] = tuples;
  const [hoverIndex, setHoverIndex] = useState<number | null>(null);
  const hover = hoverIndex === null ? undefined : points.at(hoverIndex);
  const total = stats?.distanceMeters ?? points.at(-1)?.distanceMeters ?? 0;
  return (
    <Card
      title="Route"
      actions={
        link === "" ? (
          <span className="text-sm text-gray-600">{name}</span>
        ) : (
          <Link to={link} className={`${linkClass} text-sm`}>
            {name}
          </Link>
        )
      }
    >
      <div className="flex flex-col gap-3">
        <div className="overflow-hidden rounded-lg border border-gray-200">
          <MapView className="h-80">
            {tuples.length > 1 && (
              <PolylineLayer positions={tuples} color="#d2601a" weight={4} />
            )}
            {start !== undefined && (
              <PointMarker position={start} color="#111111" tooltip="Start" />
            )}
            {hover !== undefined && (
              <PointMarker
                position={[hover.lat, hover.lng]}
                color="#d2601a"
                radius={6}
              />
            )}
            <FitBounds positions={tuples} fitKey={fitKey} />
          </MapView>
        </div>
        <p className="text-sm text-gray-600 tabular-nums">
          {formatKm(total)}
          {stats !== undefined &&
            ` · ${Math.round(stats.elevationGainMeters).toLocaleString()} m up`}
        </p>
        {points.length > 1 && (
          <ElevationProfile points={points} onHover={setHoverIndex} />
        )}
        {children}
      </div>
    </Card>
  );
};
