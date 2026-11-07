import {
  ActivityThumbnail,
  hasDistance,
  useActivityMuscles,
} from "@components/activities/activity-bits";
import VolumeChart from "@components/charts/volume-chart";
import GarminCard from "@components/garmin/garmin-card";
import Loading from "@components/loading";
import {
  groupActivitiesByDate,
  groupWorkoutsByDate,
  mostRelevantPlan,
  racesByDate,
  weekPeriods,
  workoutTotals,
} from "@components/plans/plan-utils";
import Targets from "@components/plans/targets";
import WeekDays from "@components/plans/week-days";
import { useWorkoutWeather } from "@components/plans/workout-weather";
import StravaCard from "@components/strava/strava-card";
import Card from "@components/ui/card";
import { Badge, EmptyState } from "@components/ui/misc";
import QueryError from "@components/ui/query-error";
import { linkClass } from "@components/ui/styles";
import type { ConnectError } from "@connectrpc/connect";
import { useQuery } from "@connectrpc/connect-query";
import { TrophyIcon } from "@heroicons/react/24/outline";
import type { UseQueryResult } from "@tanstack/react-query";
import {
  listActivities,
  summarizeActivities,
} from "gen/activities/v1/activities-ActivityService_connectquery";
import type {
  ListActivitiesResponse,
  SummarizeActivitiesResponse,
} from "gen/activities/v1/activities_pb";
import { SummaryInterval } from "gen/activities/v1/activities_pb";
import {
  getTrainingPlan,
  listTrainingPlans,
  listWorkouts,
} from "gen/plans/v1/plans-TrainingPlanService_connectquery";
import type { TrainingPlan } from "gen/plans/v1/plans_pb";
import { listRaces } from "gen/races/v1/races-RaceService_connectquery";
import type { Race } from "gen/races/v1/races_pb";
import type { User } from "gen/users/v1/users_pb";
import {
  addDays,
  daysOfWeek,
  endOfWeek,
  startOfWeek,
  today,
} from "helpers/dates";
import {
  formatDate,
  formatDaysUntil,
  formatHoursMinutes,
  formatKm,
  formatShortDate,
} from "helpers/format";
import {
  FOOT_SPORT_TYPES,
  priorityClass,
  priorityLabel,
  sportLabel,
} from "helpers/labels";
import React from "react";
import { Link, useNavigate } from "react-router";

const VolumeCard: React.FC = () => {
  const end = today();
  const start = addDays(startOfWeek(end), -15 * 7);
  const { isPending, isError, data, error } = useQuery(summarizeActivities, {
    endDate: end,
    interval: SummaryInterval.WEEK,
    sportTypes: FOOT_SPORT_TYPES,
    startDate: start,
  });
  let content: React.ReactNode = null;
  if (isPending) {
    content = <Loading />;
  } else if (isError) {
    content = <QueryError error={error} />;
  } else {
    let total = 0;
    let gain = 0;
    for (const summary of data.summaries) {
      total += summary.distanceMeters;
      gain += summary.elevationGainMeters;
    }
    const weeks = Math.max(data.summaries.length, 1);
    content = (
      <div className="flex flex-col gap-2">
        <p className="text-sm text-gray-600">
          Average {formatKm(total / weeks)} and {Math.round(gain / weeks)} m
          gain per week.
        </p>
        <VolumeChart summaries={data.summaries} />
      </div>
    );
  }
  return <Card title="Weekly volume · last 16 weeks">{content}</Card>;
};

const UpcomingRacesCard: React.FC = () => {
  const { isPending, isError, data, error } = useQuery(listRaces, {
    onCalendarOnly: true,
  });
  let content: React.ReactNode = null;
  if (isPending) {
    content = <Loading />;
  } else if (isError) {
    content = <QueryError error={error} />;
  } else {
    const now = today();
    const upcoming = data.races.filter((race) => race.date >= now).slice(0, 5);
    content =
      upcoming.length === 0 ? (
        <p className="text-sm text-gray-500">
          No races on your calendar.{" "}
          <Link to="/races" className={linkClass}>
            Find a race
          </Link>
        </p>
      ) : (
        <ul className="flex flex-col divide-y divide-gray-100">
          {upcoming.map((race) => (
            <li
              key={race.id}
              className="flex items-center justify-between gap-2 py-2"
            >
              <div className="min-w-0">
                <p className="truncate font-medium text-gray-900">
                  {race.name}
                </p>
                <p className="text-xs text-gray-500">
                  {formatDate(race.date)}
                  {race.distanceMeters > 0 &&
                    ` · ${formatKm(race.distanceMeters)}`}
                  {race.elevationGainMeters > 0 &&
                    ` · ${Math.round(race.elevationGainMeters)} m`}
                </p>
              </div>
              <div className="flex shrink-0 flex-col items-end gap-1">
                <Badge className={priorityClass(race.priority)}>
                  {priorityLabel(race.priority)}
                </Badge>
                <span className="text-xs font-medium text-gray-700">
                  {formatDaysUntil(race.date)}
                </span>
              </div>
            </li>
          ))}
        </ul>
      );
  }
  return (
    <Card
      title="Upcoming races"
      actions={
        <Link to="/calendar" className={`${linkClass} text-sm`}>
          Calendar
        </Link>
      }
    >
      {content}
    </Card>
  );
};

interface ThisWeekProps {
  plan: TrainingPlan;
  races: Race[];
  /**
   * This week's activities and their summary, which PlanCard loads alongside
   * the plans as they don't depend on the plan.
   */
  activities: UseQueryResult<ListActivitiesResponse, ConnectError>;
  summaries: UseQueryResult<SummarizeActivitiesResponse, ConnectError>;
}

const ThisWeek: React.FC<ThisWeekProps> = ({
  plan,
  races,
  activities,
  summaries,
}) => {
  const now = today();
  const monday = startOfWeek(now);
  const sunday = endOfWeek(now);
  const planQuery = useQuery(getTrainingPlan, { id: plan.id });
  const workouts = useQuery(listWorkouts, {
    endDate: sunday,
    planId: plan.id,
    startDate: monday,
  });
  const weather = useWorkoutWeather(monday, sunday);
  const navigate = useNavigate();
  if (workouts.isPending || activities.isPending) {
    return <Loading />;
  }
  if (workouts.isError) {
    return <QueryError error={workouts.error} />;
  }
  if (activities.isError) {
    return <QueryError error={activities.error} />;
  }
  const period = weekPeriods(planQuery.data?.periods ?? []).get(monday);
  const [summary] = summaries.data?.summaries ?? [];
  return (
    <div className="flex flex-col gap-3">
      {period !== undefined && (period.title !== "" || period.notes !== "") && (
        <p className="text-sm text-gray-700">
          <span className="font-semibold">{period.title}</span>
          {period.title !== "" && period.notes !== "" && " · "}
          {period.notes}
        </p>
      )}
      <Targets
        actual={{
          distanceMeters: summary?.distanceMeters ?? 0,
          durationSeconds: summary?.movingTimeSeconds ?? 0,
          elevationGainMeters: summary?.elevationGainMeters ?? 0,
        }}
        target={{
          distanceMeters: period?.targetDistanceMeters ?? 0,
          durationSeconds: period?.targetDurationSeconds ?? 0,
          elevationGainMeters: period?.targetElevationGainMeters ?? 0,
        }}
        planned={workoutTotals(workouts.data.workouts)}
      />
      <WeekDays
        days={daysOfWeek(monday)}
        workoutsByDate={groupWorkoutsByDate(workouts.data.workouts)}
        activitiesByDate={groupActivitiesByDate(activities.data.activities)}
        goalRacesByDate={racesByDate(races, plan.goalRaceIds)}
        rangeStart={plan.startDate}
        rangeEnd={plan.endDate}
        weather={weather}
        onOpenWorkout={(workout) => {
          void navigate(`/workouts/${workout.id}`);
        }}
      />
    </div>
  );
};

const PlanCard: React.FC = () => {
  const now = today();
  const monday = startOfWeek(now);
  const sunday = endOfWeek(now);
  const plans = useQuery(listTrainingPlans);
  const races = useQuery(listRaces);
  const activities = useQuery(listActivities, {
    endDate: sunday,
    pageSize: 100,
    startDate: monday,
  });
  const summaries = useQuery(summarizeActivities, {
    endDate: sunday,
    interval: SummaryInterval.WEEK,
    sportTypes: FOOT_SPORT_TYPES,
    startDate: monday,
  });
  let content: React.ReactNode = null;
  let actions: React.ReactNode = undefined;
  if (plans.isPending) {
    content = <Loading />;
  } else if (plans.isError) {
    content = <QueryError error={plans.error} />;
  } else {
    const plan = mostRelevantPlan(plans.data.plans);
    if (plan === undefined) {
      content = (
        <EmptyState title="No active training plan">
          <Link to="/plans" className={linkClass}>
            Create a training plan
          </Link>{" "}
          to see this week's workouts here.
        </EmptyState>
      );
    } else {
      actions = (
        <Link to={`/plans/${plan.id}`} className={`${linkClass} text-sm`}>
          Open plan
        </Link>
      );
      content =
        plan.startDate > today() ? (
          <p className="text-sm text-gray-600">
            <span className="font-medium">{plan.name}</span> starts{" "}
            {formatDaysUntil(plan.startDate)} ({formatDate(plan.startDate)}).
          </p>
        ) : (
          <ThisWeek
            plan={plan}
            races={races.data?.races ?? []}
            activities={activities}
            summaries={summaries}
          />
        );
    }
  }
  const title = (
    <span className="flex items-center gap-2">
      This week
      <span className="text-sm font-normal text-gray-500">
        {formatShortDate(monday)} – {formatShortDate(sunday)}
      </span>
    </span>
  );
  return (
    <Card title={title} actions={actions}>
      {content}
    </Card>
  );
};

const RecentActivitiesCard: React.FC = () => {
  const { isPending, isError, data, error } = useQuery(listActivities, {
    pageSize: 8,
  });
  const muscles = useActivityMuscles(data?.activities ?? []);
  let content: React.ReactNode = null;
  if (isPending) {
    content = <Loading />;
  } else if (isError) {
    content = <QueryError error={error} />;
  } else if (data.activities.length === 0) {
    content = (
      <EmptyState title="No activities yet">
        Connect Strava in{" "}
        <Link to="/settings" className={linkClass}>
          Settings
        </Link>{" "}
        to sync your activities.
      </EmptyState>
    );
  } else {
    content = (
      <ul className="flex flex-col divide-y divide-gray-100">
        {data.activities.map((activity) => (
          <li key={activity.id}>
            <Link
              to={`/activities/${activity.id}`}
              className="flex items-center gap-3 py-2 hover:bg-gray-50"
            >
              <ActivityThumbnail
                activity={activity}
                muscles={muscles.get(activity.id)}
                size={40}
              />
              <div className="min-w-0 grow">
                <p className="truncate text-sm font-medium text-gray-900">
                  {activity.name}
                </p>
                <p className="text-xs text-gray-500">
                  {formatShortDate(activity.startDateLocal)} ·{" "}
                  {sportLabel(activity.sportType)}
                </p>
              </div>
              <div className="shrink-0 text-right text-xs text-gray-700 tabular-nums">
                {hasDistance(activity) ? (
                  <>
                    <p>{formatKm(activity.distanceMeters)}</p>
                    <p className="text-gray-500">
                      {Math.round(activity.elevationGainMeters)} m ·{" "}
                      {formatHoursMinutes(activity.movingTimeSeconds)}
                    </p>
                  </>
                ) : (
                  <p>{formatHoursMinutes(activity.movingTimeSeconds)}</p>
                )}
              </div>
            </Link>
          </li>
        ))}
      </ul>
    );
  }
  return (
    <Card
      title="Recent activities"
      actions={
        <Link to="/activities" className={`${linkClass} text-sm`}>
          All activities
        </Link>
      }
    >
      {content}
    </Card>
  );
};

interface DashboardPageProps {
  user: User;
}

const DashboardPage: React.FC<DashboardPageProps> = ({ user }) => (
  <div className="flex flex-col gap-4">
    <h1 className="flex items-center gap-2 text-2xl font-bold text-gray-900">
      <TrophyIcon className="size-6 text-brand-600" />
      Hi {user.givenName === "" ? user.name : user.givenName}
    </h1>
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
      <div className="flex flex-col gap-4 lg:col-span-2">
        <PlanCard />
        <VolumeCard />
      </div>
      <div className="flex flex-col gap-4">
        <StravaCard />
        <GarminCard hideUnconfigured />
        <UpcomingRacesCard />
        <RecentActivitiesCard />
      </div>
    </div>
  </div>
);

export default DashboardPage;
