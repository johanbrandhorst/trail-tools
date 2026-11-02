import type { LactateStage, LactateTest } from "gen/lactate/v1/lactate_pb";
import { ThresholdDefinition } from "gen/lactate/v1/lactate_pb";

/**
 * DEFAULT_START_HEART_RATE is the heart rate of a guided test's first stage,
 * unless the last test started elsewhere.
 */
export const DEFAULT_START_HEART_RATE = 100;

// DEFAULT_END_HEART_RATE is the heart rate of a Garmin workout's last stage.
export const DEFAULT_END_HEART_RATE = 180;

/**
 * DEFAULT_RANGE_BPM is how far the heart rate may stray from a stage's on a
 * Garmin workout, e.g. 95 to 105 bpm for 100.
 */
export const DEFAULT_RANGE_BPM = 5;

// DEFAULT_STEP_BPM is how much higher each stage's heart rate is.
export const DEFAULT_STEP_BPM = 10;

/**
 * DEFAULT_STAGE_MINUTES is how long each stage is held: at least 3 minutes,
 * for the lactate to settle.
 */
export const DEFAULT_STAGE_MINUTES = 4;

// WARM_UP_MINUTES is how long to warm up before the first stage.
export const WARM_UP_MINUTES = 15;

/**
 * STOP_ABOVE_MMOL is the lactate past which a test has found every
 * definition of the aerobic threshold, together with lowest + 1 mmol/L.
 */
const STOP_ABOVE_MMOL = 2.5;

/**
 * LOWEST_PLUS_MMOL is how far above the lowest reading the baseline-relative
 * definition is.
 */
const LOWEST_PLUS_MMOL = 1;

// FormatMmol formats a lactate reading, e.g. "1.4 mmol/L".
export const formatMmol = (mmolPerLiter: number): string =>
  `${mmolPerLiter.toFixed(1)} mmol/L`;

// DefinitionLabel names a definition of the aerobic or anaerobic threshold.
export const definitionLabel = (definition: ThresholdDefinition): string => {
  switch (definition) {
    case ThresholdDefinition.FIXED_2_MMOL: {
      return "2 mmol/L";
    }
    case ThresholdDefinition.FIXED_2_5_MMOL: {
      return "2.5 mmol/L";
    }
    case ThresholdDefinition.LOWEST_PLUS_1_MMOL: {
      return "Lowest + 1 mmol/L";
    }
    case ThresholdDefinition.FIXED_4_MMOL: {
      return "4 mmol/L";
    }
    case ThresholdDefinition.DMAX: {
      return "Dmax";
    }
    default: {
      return "Unknown";
    }
  }
};

// AerobicThreshold is the user's current aerobic threshold, from a test.
export interface AerobicThreshold {
  heartRateBpm: number;
  test: LactateTest;
}

/**
 * LatestThreshold returns the aerobic threshold of the latest test that
 * found one, from tests listed the latest first, or undefined if none did.
 */
export const latestThreshold = (
  tests: LactateTest[]
): AerobicThreshold | undefined => {
  const test = tests.find(
    (candidate) => candidate.aerobicThresholdHeartRateBpm > 0
  );
  return test === undefined
    ? undefined
    : { heartRateBpm: test.aerobicThresholdHeartRateBpm, test };
};

/**
 * LowestReading returns the lowest reading at rest or of the stages, or 0 if
 * there are none, like the server's lowest_lactate_mmol_per_liter.
 */
const lowestReading = (resting: number, stages: LactateStage[]): number => {
  let lowest = resting;
  for (const stage of stages) {
    if (lowest === 0 || stage.lactateMmolPerLiter < lowest) {
      lowest = stage.lactateMmolPerLiter;
    }
  }
  return lowest;
};

/**
 * PastThreshold reports whether a test's last stage is past every
 * definition of the aerobic threshold, so the test can stop: at or above 2.5
 * mmol/L, and 1 mmol/L above the lowest reading.
 */
export const pastThreshold = (
  resting: number,
  stages: LactateStage[]
): boolean => {
  const last = stages.at(-1);
  if (last === undefined) {
    return false;
  }
  const target = Math.max(
    STOP_ABOVE_MMOL,
    lowestReading(resting, stages) + LOWEST_PLUS_MMOL
  );
  return last.lactateMmolPerLiter >= target;
};

/**
 * NextHeartRate returns the heart rate of the stage after the test's
 * stages: the start heart rate, then a step above the last stage.
 */
export const nextHeartRate = (
  stages: LactateStage[],
  start: number,
  step: number
): number => {
  const last = stages.at(-1);
  return last === undefined ? start : last.heartRateBpm + step;
};
