import type { Weight } from "gen/plans/v1/equipment_pb";
import { Equipment, WeightUnit } from "gen/plans/v1/equipment_pb";
import type { Exercise } from "gen/plans/v1/exercises_pb";
import { Muscle } from "gen/plans/v1/exercises_pb";
import type { LoggedSet, PlannedExercise } from "gen/plans/v1/plans_pb";
import {
  ExerciseEffort,
  ExerciseVolume,
  SessionEffort,
} from "gen/plans/v1/plans_pb";

// MUSCLE_LABELS are the names of the muscles, in the order of the enum.
export const MUSCLE_LABELS: { muscle: Muscle; label: string }[] = [
  { label: "Neck", muscle: Muscle.NECK },
  { label: "Upper back", muscle: Muscle.UPPER_BACK },
  { label: "Lats", muscle: Muscle.LATS },
  { label: "Lower back", muscle: Muscle.LOWER_BACK },
  { label: "Shoulders", muscle: Muscle.SHOULDERS },
  { label: "Chest", muscle: Muscle.CHEST },
  { label: "Biceps", muscle: Muscle.BICEPS },
  { label: "Triceps", muscle: Muscle.TRICEPS },
  { label: "Forearms", muscle: Muscle.FOREARMS },
  { label: "Abs", muscle: Muscle.ABS },
  { label: "Obliques", muscle: Muscle.OBLIQUES },
  { label: "Hip flexors", muscle: Muscle.HIP_FLEXORS },
  { label: "Glutes", muscle: Muscle.GLUTES },
  { label: "Hip abductors", muscle: Muscle.HIP_ABDUCTORS },
  { label: "Adductors", muscle: Muscle.ADDUCTORS },
  { label: "Quads", muscle: Muscle.QUADS },
  { label: "Hamstrings", muscle: Muscle.HAMSTRINGS },
  { label: "Calves", muscle: Muscle.CALVES },
  { label: "Shins", muscle: Muscle.SHINS },
  { label: "Feet", muscle: Muscle.FEET },
];

export const muscleLabel = (muscle: Muscle): string =>
  MUSCLE_LABELS.find((entry) => entry.muscle === muscle)?.label ?? "Other";

// MuscleSet is the muscles a workout or exercise works.
export interface MuscleSet {
  // The muscles worked hardest.
  primary: Muscle[];
  // The muscles also worked, without the primary ones.
  secondary: Muscle[];
}

// ByMuscle orders muscles as the enum does, from head to feet.
const byMuscle = (muscles: Iterable<Muscle>): Muscle[] => {
  const result = [...muscles];
  result.sort((first, second) => first - second);
  return result;
};

/**
 * WorkoutMuscles returns the muscles the exercises work: those any of them
 * works hardest, and the others they also work.
 */
export const workoutMuscles = (exercises: Exercise[]): MuscleSet => {
  const primary = new Set<Muscle>();
  const secondary = new Set<Muscle>();
  for (const exercise of exercises) {
    for (const muscle of exercise.primaryMuscles) {
      primary.add(muscle);
    }
    for (const muscle of exercise.secondaryMuscles) {
      secondary.add(muscle);
    }
  }
  return {
    primary: byMuscle(primary),
    secondary: byMuscle(
      [...secondary].filter((muscle) => !primary.has(muscle))
    ),
  };
};

// PerSide returns " each side" for exercises done on each side.
const perSide = (eachSide: boolean): string => (eachSide ? " each side" : "");

// FormatWeight formats a weight in its unit, e.g. "10 kg" or "25 lb".
export const formatWeight = (
  weight: Pick<Weight, "value" | "unit"> | undefined
): string => {
  if (weight === undefined || weight.value === 0) {
    return "Bodyweight";
  }
  const value = Number.isInteger(weight.value)
    ? String(weight.value)
    : weight.value.toFixed(1);
  return `${value} ${weight.unit === WeightUnit.POUNDS ? "lb" : "kg"}`;
};

// HasWeight reports whether a weight is set, rather than bodyweight.
export const hasWeight = (weight: Weight | undefined): weight is Weight =>
  weight !== undefined && weight.value > 0;

/*
 * EQUIPMENT_LABELS are the names of the kinds of equipment, in the order of
 * the enum.
 */
export const EQUIPMENT_LABELS: {
  equipment: Equipment;
  label: string;
  // Whether it comes in fixed weights.
  weighted: boolean;
}[] = [
  { equipment: Equipment.DUMBBELLS, label: "Dumbbells", weighted: true },
  { equipment: Equipment.KETTLEBELL, label: "Kettlebells", weighted: true },
  {
    equipment: Equipment.BARBELL,
    label: "Barbell and plates",
    weighted: false,
  },
  { equipment: Equipment.SQUAT_RACK, label: "Squat rack", weighted: false },
  {
    equipment: Equipment.MEDICINE_BALL,
    label: "Medicine balls",
    weighted: true,
  },
  { equipment: Equipment.BENCH, label: "Bench", weighted: false },
  { equipment: Equipment.STEP, label: "Step or box", weighted: false },
  { equipment: Equipment.MAT, label: "Mat", weighted: false },
  {
    equipment: Equipment.RESISTANCE_BAND,
    label: "Resistance bands",
    weighted: false,
  },
  { equipment: Equipment.MINI_BAND, label: "Mini bands", weighted: false },
  { equipment: Equipment.PULL_UP_BAR, label: "Pull-up bar", weighted: false },
  {
    equipment: Equipment.SUSPENSION_TRAINER,
    label: "Suspension trainer or rings",
    weighted: false,
  },
  { equipment: Equipment.FOAM_ROLLER, label: "Foam roller", weighted: false },
  { equipment: Equipment.SLIDERS, label: "Sliders", weighted: false },
  {
    equipment: Equipment.BALANCE_BOARD,
    label: "Balance board",
    weighted: false,
  },
  { equipment: Equipment.JUMP_ROPE, label: "Jump rope", weighted: false },
];

// IsWeighted reports whether equipment comes in fixed weights.
export const isWeighted = (equipment: Equipment): boolean =>
  EQUIPMENT_LABELS.find((entry) => entry.equipment === equipment)?.weighted ??
  false;

export const equipmentLabel = (equipment: Equipment): string =>
  EQUIPMENT_LABELS.find((entry) => entry.equipment === equipment)?.label ??
  "Other";

// FormatSeconds formats a short duration, like rest, as seconds or minutes.
export const formatSeconds = (seconds: number): string => {
  if (seconds < 120 || seconds % 60 !== 0) {
    return `${seconds} s`;
  }
  return `${seconds / 60} min`;
};

// FormatReps formats what a set is: its reps or how long it's held.
export const formatReps = (
  reps: number,
  holdSeconds: number,
  eachSide: boolean
): string =>
  holdSeconds > 0
    ? `${formatSeconds(holdSeconds)}${perSide(eachSide)}`
    : `${reps}${perSide(eachSide)}`;

/**
 * FormatDose formats what to do of a planned exercise, e.g. "3 × 8 each
 * side · 10 kg · rest 90 s".
 */
export const formatDose = (
  planned: PlannedExercise,
  eachSide: boolean
): string => {
  const parts = [
    `${planned.sets} × ${formatReps(planned.reps, planned.holdSeconds, eachSide)}`,
  ];
  if (hasWeight(planned.weight)) {
    parts.push(formatWeight(planned.weight));
  }
  if (planned.tempo !== "") {
    parts.push(planned.tempo);
  }
  if (planned.restSeconds > 0) {
    parts.push(`rest ${formatSeconds(planned.restSeconds)}`);
  }
  return parts.join(" · ");
};

// FormatSet formats a logged set, e.g. "8 × 10 kg" or "20 s".
export const formatSet = (set: LoggedSet): string => {
  const reps =
    set.holdSeconds > 0 ? formatSeconds(set.holdSeconds) : `${set.reps}`;
  return hasWeight(set.weight) ? `${reps} × ${formatWeight(set.weight)}` : reps;
};

// FeedbackStyle is how a rating is shown.
export interface FeedbackStyle {
  label: string;
  // Tailwind classes for a badge.
  badgeClass: string;
}

const EASIER = "bg-sky-50 text-sky-800 ring-sky-200";
const RIGHT = "bg-emerald-50 text-emerald-800 ring-emerald-200";
const HARDER = "bg-brand-50 text-brand-800 ring-brand-200";

export const EXERCISE_EFFORTS: {
  effort: ExerciseEffort;
  style: FeedbackStyle;
}[] = [
  {
    effort: ExerciseEffort.TOO_EASY,
    style: { badgeClass: EASIER, label: "Too easy" },
  },
  {
    effort: ExerciseEffort.JUST_RIGHT,
    style: { badgeClass: RIGHT, label: "Just right" },
  },
  {
    effort: ExerciseEffort.TOO_HARD,
    style: { badgeClass: HARDER, label: "Too hard" },
  },
];

export const EXERCISE_VOLUMES: {
  volume: ExerciseVolume;
  style: FeedbackStyle;
}[] = [
  {
    style: { badgeClass: EASIER, label: "Too few reps" },
    volume: ExerciseVolume.TOO_FEW,
  },
  {
    style: { badgeClass: RIGHT, label: "Reps about right" },
    volume: ExerciseVolume.ABOUT_RIGHT,
  },
  {
    style: { badgeClass: HARDER, label: "Too many reps" },
    volume: ExerciseVolume.TOO_MANY,
  },
];

export const SESSION_EFFORTS: { effort: SessionEffort; label: string }[] = [
  { effort: SessionEffort.VERY_EASY, label: "Very easy" },
  { effort: SessionEffort.EASY, label: "Easy" },
  { effort: SessionEffort.ABOUT_RIGHT, label: "About right" },
  { effort: SessionEffort.HARD, label: "Hard" },
  { effort: SessionEffort.VERY_HARD, label: "Very hard" },
];

export const sessionEffortLabel = (effort: SessionEffort): string =>
  SESSION_EFFORTS.find((entry) => entry.effort === effort)?.label ?? "";

// FeedbackStyles returns how the ratings of a logged exercise are shown.
export const feedbackStyles = (
  effort: ExerciseEffort,
  volume: ExerciseVolume
): FeedbackStyle[] => {
  const styles: FeedbackStyle[] = [];
  const effortStyle = EXERCISE_EFFORTS.find((entry) => entry.effort === effort);
  if (effortStyle !== undefined) {
    styles.push(effortStyle.style);
  }
  const volumeStyle = EXERCISE_VOLUMES.find((entry) => entry.volume === volume);
  if (volumeStyle !== undefined) {
    styles.push(volumeStyle.style);
  }
  return styles;
};

// ExerciseGroup is consecutive exercises of a workout in the same group.
export interface ExerciseGroup<Item> {
  // Empty if the exercises have no group.
  name: string;
  items: { item: Item; index: number }[];
}

// GroupExercises groups consecutive exercises with the same group name.
export const groupExercises = <Item extends { group: string }>(
  exercises: Item[]
): ExerciseGroup<Item>[] => {
  const groups: ExerciseGroup<Item>[] = [];
  for (const [index, item] of exercises.entries()) {
    const last = groups.at(-1);
    if (last?.name === item.group) {
      last.items.push({ index, item });
    } else {
      groups.push({ items: [{ index, item }], name: item.group });
    }
  }
  return groups;
};
