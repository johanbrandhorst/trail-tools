import { Terrain } from "gen/geo/v1/geo_pb";
import type { Workout } from "gen/plans/v1/plans_pb";
import { CrossTrainingSport, WorkoutType } from "gen/plans/v1/plans_pb";
import { ContingencyScenario, RuleStatus } from "gen/races/v1/race_plans_pb";
import { RacePriority } from "gen/races/v1/races_pb";
import { RoutingProfile, RouteSource } from "gen/routes/v1/routes_pb";

// Human readable labels and colors for API enums.

export interface WorkoutStyle {
  type: WorkoutType;
  label: string;
  // Tailwind classes for a workout card.
  cardClass: string;
  // Tailwind class for a small color swatch.
  dotClass: string;
}

export const WORKOUT_STYLES: WorkoutStyle[] = [
  {
    cardClass: "bg-stone-100 border-stone-300 text-stone-900",
    dotClass: "bg-stone-500",
    label: "Base",
    type: WorkoutType.BASE,
  },
  {
    cardClass: "bg-brand-50 border-brand-300 text-brand-800",
    dotClass: "bg-brand-500",
    label: "Intensity",
    type: WorkoutType.INTENSITY,
  },
  {
    cardClass: "bg-sky-50 border-sky-300 text-sky-900",
    dotClass: "bg-sky-500",
    label: "Cross training",
    type: WorkoutType.CROSS_TRAINING,
  },
  {
    cardClass: "bg-violet-50 border-violet-300 text-violet-900",
    dotClass: "bg-violet-500",
    label: "Strength",
    type: WorkoutType.STRENGTH,
  },
  {
    cardClass: "bg-ink border-ink text-white",
    dotClass: "bg-ink",
    label: "Race",
    type: WorkoutType.RACE,
  },
  {
    cardClass: "bg-white border-dashed border-gray-300 text-gray-500",
    dotClass: "bg-gray-300",
    label: "Rest",
    type: WorkoutType.REST,
  },
];

/**
 * SKI_TOUR_STYLE is the style of ski tours, cross-training in the cold
 * colors of snow and ice.
 */
const SKI_TOUR_STYLE: WorkoutStyle = {
  cardClass: "bg-cyan-50 border-cyan-400 text-cyan-950",
  dotClass: "bg-cyan-600",
  label: "Ski tour",
  type: WorkoutType.CROSS_TRAINING,
};

// CROSS_TRAINING_SPORT_OPTIONS are the sports of cross-training.
export const CROSS_TRAINING_SPORT_OPTIONS = [
  { label: "Other (see title)", value: String(CrossTrainingSport.UNSPECIFIED) },
  { label: "Ski touring", value: String(CrossTrainingSport.SKI_TOURING) },
];

/**
 * StyleOfWorkout returns the style of a workout: of its type, or of its
 * sport for cross-training with one.
 */
export const styleOfWorkout = (workout: Workout): WorkoutStyle =>
  workout.details.case === "crossTraining" &&
  workout.details.value.sport === CrossTrainingSport.SKI_TOURING
    ? SKI_TOUR_STYLE
    : workoutStyle(workout.type);

export const workoutStyle = (type: WorkoutType): WorkoutStyle => {
  for (const style of WORKOUT_STYLES) {
    if (style.type === type) {
      return style;
    }
  }
  return {
    cardClass: "bg-slate-50 border-slate-300 text-slate-800",
    dotClass: "bg-slate-400",
    label: "Workout",
    type,
  };
};

export interface TerrainStyle {
  terrain: Terrain;
  label: string;
  // A hex color, used on the map and in charts.
  color: string;
}

export const TERRAIN_STYLES: TerrainStyle[] = [
  { color: "#64748b", label: "Paved", terrain: Terrain.PAVED },
  { color: "#d97706", label: "Gravel", terrain: Terrain.GRAVEL },
  { color: "#059669", label: "Trail", terrain: Terrain.TRAIL },
  { color: "#7c3aed", label: "Technical", terrain: Terrain.TECHNICAL },
  { color: "#cbd5e1", label: "Unknown", terrain: Terrain.UNSPECIFIED },
];

export const terrainStyle = (terrain: Terrain): TerrainStyle => {
  for (const style of TERRAIN_STYLES) {
    if (style.terrain === terrain) {
      return style;
    }
  }
  return { color: "#cbd5e1", label: "Unknown", terrain };
};

export interface PriorityOption {
  value: RacePriority;
  label: string;
}

export const PRIORITY_OPTIONS: PriorityOption[] = [
  { label: "A race (goal)", value: RacePriority.A },
  { label: "B race (important)", value: RacePriority.B },
  { label: "C race (training)", value: RacePriority.C },
];

export const priorityLabel = (priority: RacePriority): string => {
  switch (priority) {
    case RacePriority.A: {
      return "A";
    }
    case RacePriority.B: {
      return "B";
    }
    case RacePriority.C: {
      return "C";
    }
    default: {
      return "–";
    }
  }
};

export const priorityClass = (priority: RacePriority): string => {
  switch (priority) {
    case RacePriority.A: {
      return "bg-brand-50 text-brand-800 ring-brand-300";
    }
    case RacePriority.B: {
      return "bg-stone-100 text-stone-800 ring-stone-300";
    }
    case RacePriority.C: {
      return "bg-white text-gray-700 ring-gray-300";
    }
    default: {
      return "bg-gray-100 text-gray-700 ring-gray-300";
    }
  }
};

export interface ProfileOption {
  value: RoutingProfile;
  label: string;
  description: string;
}

// PROFILE_OPTIONS are the route editor's routing modes.
export const PROFILE_OPTIONS: ProfileOption[] = [
  {
    description: "Snap to trails and paths, preferring unpaved ones",
    label: "Trail",
    value: RoutingProfile.TRAIL,
  },
  {
    description: "Snap to the shortest path on any way, including roads",
    label: "Road",
    value: RoutingProfile.SHORTEST,
  },
  {
    description: "Draw straight lines, for off-trail sections",
    label: "Manual",
    value: RoutingProfile.STRAIGHT,
  },
  {
    description:
      "Snap to trails, preferring the paths you have run before (your heatmap)",
    label: "My routes",
    value: RoutingProfile.HEATMAP,
  },
];

export const routeSourceLabel = (source: RouteSource): string => {
  switch (source) {
    case RouteSource.WAYPOINTS: {
      return "Designed";
    }
    case RouteSource.GPX: {
      return "GPX import";
    }
    case RouteSource.ACTIVITY: {
      return "From activity";
    }
    default: {
      return "";
    }
  }
};

export interface ContingencyOption {
  value: ContingencyScenario;
  label: string;
}

export const CONTINGENCY_OPTIONS: ContingencyOption[] = [
  { label: "GI distress", value: ContingencyScenario.GI_DISTRESS },
  { label: "Behind schedule", value: ContingencyScenario.BEHIND_SCHEDULE },
  { label: "Low energy", value: ContingencyScenario.LOW_ENERGY },
  { label: "Injury", value: ContingencyScenario.INJURY },
  { label: "Weather", value: ContingencyScenario.WEATHER },
  { label: "Gear failure", value: ContingencyScenario.GEAR_FAILURE },
  { label: "Low motivation", value: ContingencyScenario.LOW_MOTIVATION },
  { label: "Navigation", value: ContingencyScenario.NAVIGATION },
  { label: "Other", value: ContingencyScenario.OTHER },
];

export const contingencyLabel = (scenario: ContingencyScenario): string => {
  for (const option of CONTINGENCY_OPTIONS) {
    if (option.value === scenario) {
      return option.label;
    }
  }
  return "Unspecified";
};

export const ruleStatusLabel = (status: RuleStatus): string => {
  switch (status) {
    case RuleStatus.PASS: {
      return "Pass";
    }
    case RuleStatus.WARN: {
      return "Warning";
    }
    case RuleStatus.FAIL: {
      return "Fail";
    }
    default: {
      return "Unknown";
    }
  }
};

// Sport types counted as running volume (Strava sport types).
export const FOOT_SPORT_TYPES = ["Run", "TrailRun", "VirtualRun", "Hike"];

export const SPORT_TYPE_OPTIONS = [
  "Run",
  "TrailRun",
  "Hike",
  "Walk",
  "VirtualRun",
  "Ride",
  "GravelRide",
  "MountainBikeRide",
  "Swim",
  "NordicSki",
  "BackcountrySki",
  "WeightTraining",
  "Workout",
  "Yoga",
];

// SportLabel splits a Strava sport type into words, e.g. "Trail Run".
export const sportLabel = (sportType: string): string =>
  sportType === SKI_TOUR_SPORT_TYPE
    ? "Ski tour"
    : sportType.replaceAll(/([a-z])([A-Z])/gu, "$1 $2");

// SKI_TOUR_SPORT_TYPE is Strava's sport type of ski tours.
export const SKI_TOUR_SPORT_TYPE = "BackcountrySki";

// IsSkiTourSport reports whether a Strava sport type is ski touring.
export const isSkiTourSport = (sportType: string): boolean =>
  sportType === SKI_TOUR_SPORT_TYPE;

// TRIP_SPORT_TYPES are the sports offered for trip reports.
export const TRIP_SPORT_TYPES = [
  "TrailRun",
  "Run",
  "Hike",
  SKI_TOUR_SPORT_TYPE,
  "Snowshoe",
];
