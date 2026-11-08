import AvalancheForecastView from "@components/forecast/avalanche-forecast";
import { useAerobicThreshold } from "@components/lactate/use-aerobic-threshold";
import WorkoutLactateTest from "@components/lactate/workout-lactate-test";
import Loading from "@components/loading";
import RunFuelingCard from "@components/plans/run-fueling";
import RunSteps from "@components/plans/run-steps";
import WorkoutDialog from "@components/plans/workout-dialog";
import { Goal, RouteCard } from "@components/plans/workout-goal";
import WorkoutShareCard from "@components/plans/workout-share";
import { WeatherForecast } from "@components/plans/workout-weather";
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
import { listExercises } from "gen/plans/v1/exercises-ExerciseService_connectquery";
import {
  getTrainingPlan,
  getWorkout,
  getWorkoutWeather,
} from "gen/plans/v1/plans-TrainingPlanService_connectquery";
import type { Workout } from "gen/plans/v1/plans_pb";
import { WorkoutType } from "gen/plans/v1/plans_pb";
import { getRoute } from "gen/routes/v1/routes-RouteService_connectquery";
import { addDays, today } from "helpers/dates";
import { formatDate } from "helpers/format";
import { styleOfWorkout } from "helpers/labels";
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

/**
 * WeatherCard shows the forecast for the workout's day, and the avalanche
 * forecast of ski tours.
 */
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
  const avalanche = weather.data?.weather?.avalancheForecast;
  return (
    <>
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
      {avalanche !== undefined && (
        <Card title="Avalanche forecast">
          <AvalancheForecastView forecast={avalanche} date={workout.date} />
        </Card>
      )}
    </>
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
  const threshold = useAerobicThreshold();
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
    title = styleOfWorkout(workout).label;
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
  const style = styleOfWorkout(workout);
  const planPath = `/plans/${workout.planId}?month=${workout.date.slice(0, 7)}`;

  const lactateTest =
    workout.details.case === "run" && workout.details.value.lactateTest;
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
          {/* A lactate test climbs past the threshold on purpose. */}
          <Goal
            workout={workout}
            threshold={lactateTest ? undefined : threshold}
          />
          {lactateTest && <WorkoutLactateTest workout={workout} />}
          {workout.details.case === "run" &&
            workout.details.value.fueling !== undefined && (
              <RunFuelingCard
                workout={workout}
                fueling={workout.details.value.fueling}
              />
            )}
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
            <RouteCard
              name={route.data.route.name}
              points={route.data.route.points}
              stats={route.data.route.stats}
              link={`/routes/${route.data.route.id}`}
              fitKey={route.data.route.id}
            />
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
          <WorkoutShareCard workout={workout} />
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
