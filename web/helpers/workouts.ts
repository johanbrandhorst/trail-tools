import { create } from "@bufbuild/protobuf";
import { Terrain } from "gen/geo/v1/geo_pb";
import type { Workout } from "gen/plans/v1/plans_pb";
import {
  CrossTrainingWorkoutSchema,
  RaceWorkoutSchema,
  RestDaySchema,
  RunIntensity,
  RunWorkoutSchema,
  StrengthWorkoutSchema,
  WorkoutType,
} from "gen/plans/v1/plans_pb";

// WorkoutTargets are the targets of a workout, whatever its kind.
export interface WorkoutTargets {
  distanceMeters: number;
  elevationGainMeters: number;
  durationSeconds: number;
  terrain: Terrain;
  // Empty if the workout has no route.
  routeId: string;
}

const NO_TARGETS: WorkoutTargets = {
  distanceMeters: 0,
  durationSeconds: 0,
  elevationGainMeters: 0,
  routeId: "",
  terrain: Terrain.UNSPECIFIED,
};

// WorkoutTargets returns the targets of a workout from its details.
export const workoutTargets = (workout: Workout): WorkoutTargets => {
  const { details } = workout;
  switch (details.case) {
    case "run": {
      return {
        distanceMeters: details.value.targetDistanceMeters,
        durationSeconds: details.value.targetDurationSeconds,
        elevationGainMeters: details.value.targetElevationGainMeters,
        routeId: details.value.routeId,
        terrain: details.value.targetTerrain,
      };
    }
    case "crossTraining": {
      return {
        ...NO_TARGETS,
        distanceMeters: details.value.targetDistanceMeters,
        durationSeconds: details.value.targetDurationSeconds,
        elevationGainMeters: details.value.targetElevationGainMeters,
      };
    }
    case "race": {
      return {
        ...NO_TARGETS,
        distanceMeters: details.value.targetDistanceMeters,
        durationSeconds: details.value.targetDurationSeconds,
        elevationGainMeters: details.value.targetElevationGainMeters,
        routeId: details.value.routeId,
      };
    }
    case "strength": {
      return {
        ...NO_TARGETS,
        durationSeconds: details.value.targetDurationSeconds,
      };
    }
    default: {
      return NO_TARGETS;
    }
  }
};

/**
 * IsCompleted reports whether a workout is done: it has an activity, or it's
 * a strength workout with a log.
 */
export const isCompleted = (workout: Workout): boolean =>
  workout.completedActivityId !== "" ||
  (workout.details.case === "strength" &&
    workout.details.value.log !== undefined);

// DetailsPath is the update mask path of the details of a workout type.
export const detailsPath = (type: WorkoutType): string => {
  switch (type) {
    case WorkoutType.BASE:
    case WorkoutType.INTENSITY: {
      return "run";
    }
    case WorkoutType.CROSS_TRAINING: {
      return "cross_training";
    }
    case WorkoutType.STRENGTH: {
      return "strength";
    }
    case WorkoutType.RACE: {
      return "race";
    }
    default: {
      return "rest";
    }
  }
};

/**
 * WorkoutDetails returns the details of a workout of the type with the
 * targets, keeping the exercises of an existing strength workout.
 */
export const workoutDetails = (
  type: WorkoutType,
  targets: WorkoutTargets,
  existing: Workout | undefined
): Workout["details"] => {
  switch (type) {
    case WorkoutType.BASE:
    case WorkoutType.INTENSITY: {
      return {
        case: "run",
        value: create(RunWorkoutSchema, {
          intensity:
            type === WorkoutType.BASE ? RunIntensity.BASE : RunIntensity.HARD,
          routeId: targets.routeId,
          targetDistanceMeters: targets.distanceMeters,
          targetDurationSeconds: targets.durationSeconds,
          targetElevationGainMeters: targets.elevationGainMeters,
          targetTerrain: targets.terrain,
        }),
      };
    }
    case WorkoutType.CROSS_TRAINING: {
      return {
        case: "crossTraining",
        value: create(CrossTrainingWorkoutSchema, {
          targetDistanceMeters: targets.distanceMeters,
          targetDurationSeconds: targets.durationSeconds,
          targetElevationGainMeters: targets.elevationGainMeters,
        }),
      };
    }
    case WorkoutType.STRENGTH: {
      const exercises =
        existing?.details.case === "strength"
          ? existing.details.value.exercises
          : [];
      return {
        case: "strength",
        value: create(StrengthWorkoutSchema, {
          exercises,
          targetDurationSeconds: targets.durationSeconds,
        }),
      };
    }
    case WorkoutType.RACE: {
      return {
        case: "race",
        value: create(RaceWorkoutSchema, {
          routeId: targets.routeId,
          targetDistanceMeters: targets.distanceMeters,
          targetDurationSeconds: targets.durationSeconds,
          targetElevationGainMeters: targets.elevationGainMeters,
        }),
      };
    }
    default: {
      return { case: "rest", value: create(RestDaySchema, {}) };
    }
  }
};

// HasRoute reports whether a workout's kind can have a route.
export const hasRoute = (type: WorkoutType): boolean =>
  type === WorkoutType.BASE ||
  type === WorkoutType.INTENSITY ||
  type === WorkoutType.RACE;
