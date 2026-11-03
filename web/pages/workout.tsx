import ElevationProfile from "@components/charts/elevation-profile";
import { useAerobicThreshold } from "@components/lactate/use-aerobic-threshold";
import Loading from "@components/loading";
import { FitBounds, PointMarker, PolylineLayer } from "@components/map/layers";
import MapView from "@components/map/map-view";
import RunSteps from "@components/plans/run-steps";
import WorkoutDialog from "@components/plans/workout-dialog";
import { WeatherForecast } from "@components/plans/workout-weather";
import { toTuples } from "@components/routes/route-geometry";
import StrengthWorkoutView from "@components/strength/strength-workout";
import Card from "@components/ui/card";
import QueryError from "@components/ui/query-error";
import { linkClass, secondaryButton } from "@components/ui/styles";
import { Code } from "@connectrpc/connect";
import { useQuery } from "@connectrpc/connect-query";
import {
  ArrowLeftIcon,
  CheckCircleIcon,
  PencilSquareIcon,
} from "@heroicons/react/24/outline";
import { skipToken } from "@tanstack/react-query";
import { listActivities } from "gen/activities/v1/activities-ActivityService_connectquery";
import { Terrain } from "gen/geo/v1/geo_pb";
import { listExercises } from "gen/plans/v1/exercises-ExerciseService_connectquery";
import {
  getTrainingPlan,
  getWorkout,
  getWorkoutWeather,
} from "gen/plans/v1/plans-TrainingPlanService_connectquery";
import type { Workout } from "gen/plans/v1/plans_pb";
import { WorkoutType } from "gen/plans/v1/plans_pb";
import { getRoute } from "gen/routes/v1/routes-RouteService_connectquery";
import type { Route } from "gen/routes/v1/routes_pb";
import { addDays, today } from "helpers/dates";
import { formatDate, formatHoursMinutes, formatKm } from "helpers/format";
import { terrainStyle, workoutStyle } from "helpers/labels";
import { isCompleted, startWindow, workoutTargets } from "helpers/workouts";
import React, { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";

/**
 * Outdoors reports whether a workout is outside, so it has a route and the
 * weather matters: not rest days or strength training.
 */
const outdoors = (workout: Workout): boolean =>
  workout.type !== WorkoutType.REST && workout.type !== WorkoutType.STRENGTH;

// FORECAST_DAYS is how many days ahead workouts have a forecast.
const FORECAST_DAYS = 15;

// Forecasts change slowly, so they're kept for half an hour.
const WEATHER_STALE_MS = 30 * 60 * 1000;

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

/**
 * Goal shows what the workout is for and its targets. Base workouts stay
 * below the user's aerobic threshold, if a lactate test found it.
 */
const Goal: React.FC<{ workout: Workout }> = ({ workout }) => {
  const threshold = useAerobicThreshold();
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

// RouteCard shows the workout's route on a map with its elevation profile.
const RouteCard: React.FC<{ route: Route }> = ({ route }) => {
  const { points } = route;
  const tuples = useMemo(() => toTuples(points), [points]);
  const [start] = tuples;
  const [hoverIndex, setHoverIndex] = useState<number | null>(null);
  const hover = hoverIndex === null ? undefined : points.at(hoverIndex);
  const total = points.at(-1)?.distanceMeters ?? 0;
  return (
    <Card
      title="Route"
      actions={
        <Link to={`/routes/${route.id}`} className={`${linkClass} text-sm`}>
          {route.name}
        </Link>
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
            <FitBounds positions={tuples} fitKey={route.id} />
          </MapView>
        </div>
        <p className="text-sm text-gray-600 tabular-nums">
          {formatKm(total)}
          {route.stats !== undefined &&
            ` · ${Math.round(route.stats.elevationGainMeters).toLocaleString()} m up`}
        </p>
        {points.length > 1 && (
          <ElevationProfile points={points} onHover={setHoverIndex} />
        )}
      </div>
    </Card>
  );
};

// NoForecast explains why a workout has no forecast.
const noForecast = (date: string): string => {
  const now = today();
  if (date < addDays(now, -1)) {
    return "There's no forecast for past days.";
  }
  if (date > addDays(now, FORECAST_DAYS)) {
    return `The forecast is available from ${formatDate(addDays(date, -FORECAST_DAYS))}.`;
  }
  return "Add a route to the workout, or sync an activity from Strava, for a forecast where you run.";
};

// WeatherCard shows the forecast for the workout's day.
const WeatherCard: React.FC<{ workout: Workout }> = ({ workout }) => {
  const outside = outdoors(workout);
  const weather = useQuery(
    getWorkoutWeather,
    outside ? { workoutId: workout.id } : skipToken,
    { retry: false, staleTime: WEATHER_STALE_MS }
  );
  if (!outside) {
    return null;
  }
  let content: React.ReactNode = <Loading />;
  if (weather.isError) {
    content = (
      <p className="text-sm text-gray-500">
        The weather forecast is unavailable right now.
      </p>
    );
  } else if (weather.data !== undefined) {
    content =
      weather.data.weather === undefined ? (
        <p className="text-sm text-gray-500">{noForecast(workout.date)}</p>
      ) : (
        <WeatherForecast weather={weather.data.weather} />
      );
  }
  return (
    <Card
      title="Weather"
      actions={
        workoutTargets(workout).routeId !== "" && (
          <Link
            to={`/forecast?route=${workoutTargets(workout).routeId}&start=${workout.date}T07:00`}
            className={`${linkClass} text-sm`}
          >
            Forecast along the route
          </Link>
        )
      }
    >
      {content}
    </Card>
  );
};

/**
 * WorkoutPage shows a planned workout, at /workouts/<id>: its goal, route
 * and the weather to expect. It's edited in a dialog.
 */
const WorkoutPage: React.FC = () => {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const [editing, setEditing] = useState(false);
  const { isPending, isError, data, error, refetch } = useQuery(getWorkout, {
    id,
  });
  const workout = data?.workout;
  const plan = useQuery(
    getTrainingPlan,
    workout === undefined ? skipToken : { id: workout.planId }
  );
  const routeId = workout === undefined ? "" : workoutTargets(workout).routeId;
  const route = useQuery(
    getRoute,
    routeId === "" ? skipToken : { id: routeId, includePoints: true }
  );
  // The library, for the instructions and muscles of strength exercises.
  const exercises = useQuery(
    listExercises,
    workout?.details.case === "strength" ? {} : skipToken
  );
  const library = useMemo(
    () => new Map((exercises.data?.exercises ?? []).map((ex) => [ex.id, ex])),
    [exercises.data]
  );
  // Activities around the day, to mark the workout as completed.
  const activities = useQuery(
    listActivities,
    workout === undefined || !editing
      ? skipToken
      : {
          endDate: addDays(workout.date, 7),
          pageSize: 100,
          startDate: addDays(workout.date, -7),
        }
  );
  let title = workout?.title.trim() ?? "";
  if (workout !== undefined && title === "") {
    title = workoutStyle(workout.type).label;
  }

  useEffect(() => {
    if (title === "") {
      return undefined;
    }
    const previous = document.title;
    document.title = title;
    return () => {
      document.title = previous;
    };
  }, [title]);

  if (isPending) {
    return <Loading />;
  }
  if (isError) {
    if (error.code === Code.NotFound) {
      return (
        <div className="py-16 text-center">
          <h1 className="text-xl font-semibold">Workout not found</h1>
          <p className="mt-2 text-gray-600">
            It may have been deleted.{" "}
            <Link to="/plans" className={linkClass}>
              See your training plans
            </Link>
            .
          </p>
        </div>
      );
    }
    return (
      <QueryError
        error={error}
        onRetry={() => {
          void refetch();
        }}
      />
    );
  }
  if (workout === undefined) {
    return null;
  }
  const style = workoutStyle(workout.type);
  const planPath = `/plans/${workout.planId}?month=${workout.date.slice(0, 7)}`;

  return (
    <div className="mx-auto flex w-full max-w-6xl flex-col gap-4">
      <Link
        to={planPath}
        className="inline-flex items-center gap-1 self-start text-sm text-gray-600 hover:text-gray-900"
      >
        <ArrowLeftIcon className="size-4" />
        {plan.data?.plan?.name ?? "Training plan"}
      </Link>
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex flex-col gap-1">
          <p className="flex items-center gap-2 text-sm font-medium text-gray-600">
            <span
              className={`inline-block size-2.5 rounded-sm ${style.dotClass}`}
            />
            {/* Lines break between the parts, not within them. */}
            <span className="flex flex-wrap gap-x-1">
              <span className="whitespace-nowrap">
                {style.label} · {formatDate(workout.date)}
              </span>
              {startWindow(workout) !== "" && (
                <span className="whitespace-nowrap">
                  · Start {startWindow(workout)}
                </span>
              )}
              {workout.location !== undefined && (
                <span className="whitespace-nowrap">
                  ·{" "}
                  <a
                    href={`https://www.openstreetmap.org/?mlat=${workout.location.lat}&mlon=${workout.location.lng}#map=16/${workout.location.lat}/${workout.location.lng}`}
                    target="_blank"
                    rel="noreferrer"
                    className="hover:underline"
                  >
                    At{" "}
                    {workout.locationName === ""
                      ? "the meeting point"
                      : workout.locationName}
                  </a>
                </span>
              )}
            </span>
          </p>
          <h1 className="text-2xl font-bold text-balance text-gray-900">
            {title}
          </h1>
          {workout.completedActivityId !== "" && (
            <Link
              to={`/activities/${workout.completedActivityId}`}
              className="inline-flex items-center gap-1 self-start text-sm font-medium text-emerald-700 hover:underline"
            >
              <CheckCircleIcon className="size-4" />
              Completed: see the activity
            </Link>
          )}
          {workout.completedActivityId === "" && isCompleted(workout) && (
            <p className="inline-flex items-center gap-1 self-start text-sm font-medium text-emerald-700">
              <CheckCircleIcon className="size-4" />
              Completed
            </p>
          )}
        </div>
        <button
          type="button"
          className={secondaryButton}
          onClick={() => {
            setEditing(true);
          }}
        >
          <PencilSquareIcon className="size-4" />
          Edit
        </button>
      </header>
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,24rem)]">
        <div className="flex min-w-0 flex-col gap-4">
          <Goal workout={workout} />
          {workout.details.case === "run" &&
            workout.details.value.blocks.length > 0 && (
              <RunSteps blocks={workout.details.value.blocks} />
            )}
          {workout.details.case === "strength" && (
            <StrengthWorkoutView
              workout={workout}
              strength={workout.details.value}
              exercises={library}
            />
          )}
          {route.data?.route !== undefined && (
            <RouteCard route={route.data.route} />
          )}
          {routeId === "" && outdoors(workout) && (
            <Card title="Route">
              <p className="text-sm text-gray-600">
                No route yet.{" "}
                <Link
                  to={`/routes/new?workout=${workout.id}`}
                  className={linkClass}
                >
                  Design a route
                </Link>{" "}
                for the weather where you&apos;ll run.
              </p>
            </Card>
          )}
        </div>
        <div className="flex min-w-0 flex-col gap-4">
          <WeatherCard workout={workout} />
        </div>
      </div>
      {editing && plan.data?.plan !== undefined && (
        <WorkoutDialog
          planId={workout.planId}
          planStart={plan.data.plan.startDate}
          planEnd={plan.data.plan.endDate}
          workout={workout}
          date={workout.date}
          activities={activities.data?.activities ?? []}
          onClose={() => {
            setEditing(false);
          }}
          onDeleted={() => {
            void navigate(planPath);
          }}
        />
      )}
    </div>
  );
};

export default WorkoutPage;
