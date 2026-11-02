import Loading from "@components/loading";
import MonthStrip from "@components/plans/month-strip";
import PeriodDialog from "@components/plans/period-dialog";
import PlanFormDialog from "@components/plans/plan-form-dialog";
import {
  groupActivitiesByDate,
  groupWorkoutsByDate,
  monthPeriods,
  racesByDate,
  summariesByStart,
  weekPeriods,
  workoutTotals,
} from "@components/plans/plan-utils";
import Targets from "@components/plans/targets";
import WeekDays from "@components/plans/week-days";
import WorkoutDialog from "@components/plans/workout-dialog";
import { useWorkoutWeather } from "@components/plans/workout-weather";
import ConfirmButton from "@components/ui/confirm-button";
import { Badge } from "@components/ui/misc";
import QueryError, { ErrorText } from "@components/ui/query-error";
import {
  cardClass,
  ghostButton,
  iconButton,
  secondaryButton,
} from "@components/ui/styles";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import {
  ChevronLeftIcon,
  ChevronRightIcon,
  PencilSquareIcon,
  TrophyIcon,
} from "@heroicons/react/24/outline";
import { useQueryClient } from "@tanstack/react-query";
import {
  listActivities,
  summarizeActivities,
} from "gen/activities/v1/activities-ActivityService_connectquery";
import { SummaryInterval } from "gen/activities/v1/activities_pb";
import {
  deleteTrainingPlan,
  getTrainingPlan,
  listWorkouts,
} from "gen/plans/v1/plans-TrainingPlanService_connectquery";
import type { PlanPeriod, TrainingPlan, Workout } from "gen/plans/v1/plans_pb";
import { PeriodGranularity, TrainingPlanService } from "gen/plans/v1/plans_pb";
import { listRaces } from "gen/races/v1/races-RaceService_connectquery";
import type { Race } from "gen/races/v1/races_pb";
import {
  daysBetween,
  daysOfWeek,
  endOfMonth,
  endOfWeek,
  isWithin,
  maxDate,
  minDate,
  monthsBetween,
  startOfMonth,
  startOfWeek,
  today,
  weeksBetween,
} from "helpers/dates";
import {
  formatDate,
  formatDaysUntil,
  formatMonth,
  formatShortDate,
} from "helpers/format";
import {
  FOOT_SPORT_TYPES,
  WORKOUT_STYLES,
  priorityClass,
} from "helpers/labels";
import { invalidateServices } from "helpers/query";
import React, { useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";

interface WorkoutEdit {
  workout: Workout | undefined;
  date: string;
}

interface PeriodEdit {
  granularity: PeriodGranularity;
  startDate: string;
  period: PlanPeriod | undefined;
  title: string;
}

const defaultMonth = (plan: TrainingPlan, months: string[]): string => {
  const now = today();
  if (isWithin(now, plan.startDate, plan.endDate)) {
    return startOfMonth(now);
  }
  if (now > plan.endDate) {
    return months.at(-1) ?? startOfMonth(plan.startDate);
  }
  return startOfMonth(plan.startDate);
};

const WorkoutLegend: React.FC = () => (
  <div className="flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-gray-600">
    {WORKOUT_STYLES.map((style) => (
      <span key={style.type} className="inline-flex items-center gap-1">
        <span
          className={`inline-block size-2.5 rounded-sm ${style.dotClass}`}
        />
        {style.label}
      </span>
    ))}
  </div>
);

interface PlanDetailProps {
  plan: TrainingPlan;
  periods: PlanPeriod[];
  races: Race[];
}

const PlanDetail: React.FC<PlanDetailProps> = ({ plan, periods, races }) => {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const [workoutEdit, setWorkoutEdit] = useState<WorkoutEdit | null>(null);
  const [periodEdit, setPeriodEdit] = useState<PeriodEdit | null>(null);
  const [editingPlan, setEditingPlan] = useState(false);

  const months = monthsBetween(plan.startDate, plan.endDate);
  // The month is "YYYY-MM-01"; "YYYY-MM" is accepted too.
  let monthParam = searchParams.get("month") ?? "";
  if (monthParam.length === 7) {
    monthParam = `${monthParam}-01`;
  }
  const month = months.includes(monthParam)
    ? monthParam
    : defaultMonth(plan, months);
  const monthIndex = months.indexOf(month);
  const monthEnd = endOfMonth(month);
  const viewStart = startOfWeek(maxDate(month, plan.startDate));
  const viewEnd = endOfWeek(minDate(monthEnd, plan.endDate));
  const weather = useWorkoutWeather(viewStart, viewEnd);

  const selectMonth = (value: string) => {
    setSearchParams({ month: value }, { replace: true });
  };

  const monthSummaries = useQuery(summarizeActivities, {
    endDate: plan.endDate,
    interval: SummaryInterval.MONTH,
    sportTypes: FOOT_SPORT_TYPES,
    startDate: plan.startDate,
  });
  const weekSummaries = useQuery(summarizeActivities, {
    endDate: viewEnd,
    interval: SummaryInterval.WEEK,
    sportTypes: FOOT_SPORT_TYPES,
    startDate: viewStart,
  });
  const workouts = useQuery(listWorkouts, {
    endDate: viewEnd,
    planId: plan.id,
    startDate: viewStart,
  });
  const activities = useQuery(listActivities, {
    endDate: viewEnd,
    pageSize: 500,
    startDate: viewStart,
  });
  const deletePlan = useMutation(deleteTrainingPlan, {
    onSuccess: async () => {
      await invalidateServices(queryClient, [TrainingPlanService]);
      void navigate("/plans");
    },
  });

  const goalRaces = races.filter((race) => plan.goalRaceIds.includes(race.id));
  const goalRacesByDate = racesByDate(goalRaces, plan.goalRaceIds);
  const monthPeriodMap = monthPeriods(periods);
  const weekPeriodMap = weekPeriods(periods);
  const monthSummaryMap = summariesByStart(
    monthSummaries.data?.summaries ?? []
  );
  const weekSummaryMap = summariesByStart(weekSummaries.data?.summaries ?? []);
  const workoutList = workouts.data?.workouts ?? [];
  const activityList = activities.data?.activities ?? [];
  const workoutsByDate = groupWorkoutsByDate(workoutList);
  const activitiesByDate = groupActivitiesByDate(activityList);
  const monthPeriod = monthPeriodMap.get(month);
  const monthSummary = monthSummaryMap.get(month);
  const monthWorkouts = workoutList.filter((workout) =>
    isWithin(workout.date, month, monthEnd)
  );
  const weeks = weeksBetween(
    maxDate(month, plan.startDate),
    minDate(monthEnd, plan.endDate)
  );
  const planWeekStart = startOfWeek(plan.startDate);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <Link to="/plans" className="text-sm text-gray-500 hover:underline">
            Training plans
          </Link>
          <h1 className="text-2xl font-bold text-gray-900">{plan.name}</h1>
          <p className="text-sm text-gray-600">
            {formatDate(plan.startDate)} – {formatDate(plan.endDate)} ·{" "}
            {Math.ceil((daysBetween(plan.startDate, plan.endDate) + 1) / 7)}{" "}
            weeks
          </p>
          {goalRaces.length > 0 && (
            <div className="mt-2 flex flex-wrap gap-2">
              {goalRaces.map((race) => (
                <Badge key={race.id} className={priorityClass(race.priority)}>
                  <TrophyIcon className="size-3.5" />
                  {race.name} · {formatShortDate(race.date)}
                  {race.dateUnconfirmed && " (date TBC)"} ·{" "}
                  {formatDaysUntil(race.date)}
                </Badge>
              ))}
            </div>
          )}
        </div>
        <div className="flex gap-2">
          <button
            type="button"
            className={secondaryButton}
            onClick={() => {
              setEditingPlan(true);
            }}
          >
            <PencilSquareIcon className="size-4" />
            Edit plan
          </button>
          <ConfirmButton
            title="Delete training plan?"
            message={`This deletes "${plan.name}" with all its periods and workouts.`}
            disabled={deletePlan.isPending}
            onConfirm={() => {
              deletePlan.mutate({ id: plan.id });
            }}
          >
            Delete
          </ConfirmButton>
        </div>
      </div>
      <ErrorText error={deletePlan.error} />
      {plan.description !== "" && (
        <p className="max-w-4xl text-sm whitespace-pre-line text-gray-700">
          {plan.description}
        </p>
      )}

      <MonthStrip
        months={months}
        selected={month}
        onSelect={selectMonth}
        periods={monthPeriodMap}
        summaries={monthSummaryMap}
        goalRaces={goalRaces}
      />

      <section className={`${cardClass} flex flex-col gap-3 p-4`}>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-1">
            <button
              type="button"
              className={iconButton}
              disabled={monthIndex <= 0}
              aria-label="Previous month"
              onClick={() => {
                selectMonth(months[monthIndex - 1] ?? month);
              }}
            >
              <ChevronLeftIcon className="size-5" />
            </button>
            <h2 className="text-lg font-semibold">
              {formatMonth(month)}
              {monthPeriod !== undefined && monthPeriod.title !== "" && (
                <span className="font-normal text-gray-500">
                  {" "}
                  · {monthPeriod.title}
                </span>
              )}
            </h2>
            <button
              type="button"
              className={iconButton}
              disabled={monthIndex >= months.length - 1}
              aria-label="Next month"
              onClick={() => {
                selectMonth(months[monthIndex + 1] ?? month);
              }}
            >
              <ChevronRightIcon className="size-5" />
            </button>
          </div>
          <button
            type="button"
            className={ghostButton}
            onClick={() => {
              setPeriodEdit({
                granularity: PeriodGranularity.MONTH,
                period: monthPeriod,
                startDate: month,
                title: `${formatMonth(month)} focus and targets`,
              });
            }}
          >
            <PencilSquareIcon className="size-4" />
            Edit month
          </button>
        </div>
        {monthPeriod !== undefined && monthPeriod.notes !== "" && (
          <p className="text-sm whitespace-pre-line text-gray-700">
            {monthPeriod.notes}
          </p>
        )}
        <Targets
          actual={{
            distanceMeters: monthSummary?.distanceMeters ?? 0,
            durationSeconds: monthSummary?.movingTimeSeconds ?? 0,
            elevationGainMeters: monthSummary?.elevationGainMeters ?? 0,
          }}
          target={{
            distanceMeters: monthPeriod?.targetDistanceMeters ?? 0,
            durationSeconds: monthPeriod?.targetDurationSeconds ?? 0,
            elevationGainMeters: monthPeriod?.targetElevationGainMeters ?? 0,
          }}
          planned={workoutTotals(monthWorkouts)}
        />
        <WorkoutLegend />
      </section>

      {workouts.isError && (
        <QueryError
          error={workouts.error}
          onRetry={() => {
            void workouts.refetch();
          }}
        />
      )}
      {activities.isError && <QueryError error={activities.error} />}
      {workouts.isPending && <Loading />}

      <div className="flex flex-col gap-4">
        {weeks.map((monday) => {
          const days = daysOfWeek(monday);
          const period = weekPeriodMap.get(monday);
          const summary = weekSummaryMap.get(monday);
          const weekNumber =
            Math.floor(daysBetween(planWeekStart, monday) / 7) + 1;
          const weekWorkouts = workoutList.filter((workout) =>
            isWithin(workout.date, monday, endOfWeek(monday))
          );
          return (
            <section
              key={monday}
              className={`${cardClass} flex flex-col gap-3 p-3`}
            >
              <div className="flex flex-wrap items-start justify-between gap-2">
                <div className="min-w-0">
                  <h3 className="font-semibold text-gray-900">
                    Week {weekNumber}
                    <span className="font-normal text-gray-500">
                      {" "}
                      · {formatShortDate(monday)} –{" "}
                      {formatShortDate(endOfWeek(monday))}
                    </span>
                    {period !== undefined && period.title !== "" && (
                      <span className="text-brand-700"> · {period.title}</span>
                    )}
                  </h3>
                  {period !== undefined && period.notes !== "" && (
                    <p className="text-sm whitespace-pre-line text-gray-600">
                      {period.notes}
                    </p>
                  )}
                </div>
                <button
                  type="button"
                  className={ghostButton}
                  onClick={() => {
                    setPeriodEdit({
                      granularity: PeriodGranularity.WEEK,
                      period,
                      startDate: monday,
                      title: `Week ${weekNumber} focus and targets`,
                    });
                  }}
                >
                  <PencilSquareIcon className="size-4" />
                  Edit week
                </button>
              </div>
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
                planned={workoutTotals(weekWorkouts)}
              />
              <WeekDays
                days={days}
                workoutsByDate={workoutsByDate}
                activitiesByDate={activitiesByDate}
                goalRacesByDate={goalRacesByDate}
                rangeStart={plan.startDate}
                rangeEnd={plan.endDate}
                monthStart={month}
                monthEnd={monthEnd}
                onAddWorkout={(date) => {
                  setWorkoutEdit({ date, workout: undefined });
                }}
                weather={weather}
                onOpenWorkout={(workout) => {
                  void navigate(`/workouts/${workout.id}`);
                }}
              />
            </section>
          );
        })}
      </div>

      {workoutEdit !== null && (
        <WorkoutDialog
          planId={plan.id}
          planStart={plan.startDate}
          planEnd={plan.endDate}
          workout={workoutEdit.workout}
          date={workoutEdit.date}
          activities={activityList}
          onClose={() => {
            setWorkoutEdit(null);
          }}
        />
      )}
      {periodEdit !== null && (
        <PeriodDialog
          planId={plan.id}
          granularity={periodEdit.granularity}
          startDate={periodEdit.startDate}
          period={periodEdit.period}
          title={periodEdit.title}
          onClose={() => {
            setPeriodEdit(null);
          }}
        />
      )}
      {editingPlan && (
        <PlanFormDialog
          plan={plan}
          onClose={() => {
            setEditingPlan(false);
          }}
        />
      )}
    </div>
  );
};

const PlanPage: React.FC = () => {
  const { id = "" } = useParams();
  const { isPending, isError, data, error, refetch } = useQuery(
    getTrainingPlan,
    { id }
  );
  // The races don't depend on the plan, so they load alongside it.
  const races = useQuery(listRaces);
  if (isPending) {
    return <Loading />;
  }
  if (isError) {
    return (
      <QueryError
        error={error}
        onRetry={() => {
          void refetch();
        }}
      />
    );
  }
  if (data.plan === undefined) {
    return <p>Plan not found.</p>;
  }
  return (
    <PlanDetail
      plan={data.plan}
      periods={data.periods}
      races={races.data?.races ?? []}
    />
  );
};

export default PlanPage;
