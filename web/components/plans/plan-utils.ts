import type {
  Activity,
  ActivitySummary,
} from "gen/activities/v1/activities_pb";
import type { PlanPeriod, TrainingPlan, Workout } from "gen/plans/v1/plans_pb";
import { PeriodGranularity } from "gen/plans/v1/plans_pb";
import type { Race } from "gen/races/v1/races_pb";
import { isWithin, today } from "helpers/dates";
import { workoutTargets } from "helpers/workouts";

export const groupWorkoutsByDate = (
  workouts: Workout[]
): Map<string, Workout[]> => {
  const groups = new Map<string, Workout[]>();
  for (const workout of workouts) {
    const group = groups.get(workout.date);
    if (group === undefined) {
      groups.set(workout.date, [workout]);
    } else {
      group.push(workout);
    }
  }
  return groups;
};

// StartSeconds returns when an activity started, in seconds since the epoch.
const startSeconds = (activity: Activity): number =>
  Number(activity.startTime?.seconds ?? 0n);

/**
 * GroupActivitiesByDate groups activities by their local date, each day's in
 * the order they were done. The API lists the most recent first.
 */
export const groupActivitiesByDate = (
  activities: Activity[]
): Map<string, Activity[]> => {
  const groups = new Map<string, Activity[]>();
  for (const activity of activities) {
    const group = groups.get(activity.startDateLocal);
    if (group === undefined) {
      groups.set(activity.startDateLocal, [activity]);
    } else {
      group.push(activity);
    }
  }
  // The groups are this function's own, so they're sorted in place.
  for (const group of groups.values()) {
    group.sort((first, second) => startSeconds(first) - startSeconds(second));
  }
  return groups;
};

export const summariesByStart = (
  summaries: ActivitySummary[]
): Map<string, ActivitySummary> => {
  const result = new Map<string, ActivitySummary>();
  for (const summary of summaries) {
    result.set(summary.startDate, summary);
  }
  return result;
};

export const periodsByStart = (
  periods: PlanPeriod[],
  granularity: PeriodGranularity
): Map<string, PlanPeriod> => {
  const result = new Map<string, PlanPeriod>();
  for (const period of periods) {
    if (period.granularity === granularity) {
      result.set(period.startDate, period);
    }
  }
  return result;
};

export const monthPeriods = (periods: PlanPeriod[]): Map<string, PlanPeriod> =>
  periodsByStart(periods, PeriodGranularity.MONTH);

export const weekPeriods = (periods: PlanPeriod[]): Map<string, PlanPeriod> =>
  periodsByStart(periods, PeriodGranularity.WEEK);

export const racesByDate = (
  races: Race[],
  ids: string[]
): Map<string, Race> => {
  const result = new Map<string, Race>();
  for (const race of races) {
    if (ids.includes(race.id)) {
      result.set(race.date, race);
    }
  }
  return result;
};

export interface Totals {
  distanceMeters: number;
  elevationGainMeters: number;
  durationSeconds: number;
}

export const workoutTotals = (workouts: Workout[]): Totals => {
  const totals: Totals = {
    distanceMeters: 0,
    durationSeconds: 0,
    elevationGainMeters: 0,
  };
  for (const workout of workouts) {
    const targets = workoutTargets(workout);
    totals.distanceMeters += targets.distanceMeters;
    totals.elevationGainMeters += targets.elevationGainMeters;
    totals.durationSeconds += targets.durationSeconds;
  }
  return totals;
};

export type PlanStatus = "active" | "upcoming" | "finished";

export const planStatus = (plan: TrainingPlan): PlanStatus => {
  const now = today();
  if (isWithin(now, plan.startDate, plan.endDate)) {
    return "active";
  }
  if (plan.startDate > now) {
    return "upcoming";
  }
  return "finished";
};

/**
 * MostRelevantPlan picks the plan to show on the dashboard: an active plan
 * ending soonest, otherwise the next upcoming plan.
 */
export const mostRelevantPlan = (
  plans: TrainingPlan[]
): TrainingPlan | undefined => {
  let best: TrainingPlan | undefined = undefined;
  for (const plan of plans) {
    if (planStatus(plan) !== "active") {
      continue;
    }
    if (best === undefined || plan.endDate < best.endDate) {
      best = plan;
    }
  }
  if (best !== undefined) {
    return best;
  }
  for (const plan of plans) {
    if (planStatus(plan) !== "upcoming") {
      continue;
    }
    if (best === undefined || plan.startDate < best.startDate) {
      best = plan;
    }
  }
  return best;
};
