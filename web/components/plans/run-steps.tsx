import Card from "@components/ui/card";
import type { HeartRateTarget, RunBlock, RunStep } from "gen/plans/v1/plans_pb";
import { HeartRateReference, RunStepKind } from "gen/plans/v1/plans_pb";
import { formatPace } from "helpers/format";
import React from "react";

const KIND_LABELS: Record<RunStepKind, string> = {
  [RunStepKind.UNSPECIFIED]: "Step",
  [RunStepKind.WARM_UP]: "Warm-up",
  [RunStepKind.RUN]: "Run",
  [RunStepKind.RECOVERY]: "Recovery",
  [RunStepKind.REST]: "Rest",
  [RunStepKind.COOL_DOWN]: "Cool-down",
};

const KIND_DOTS: Record<RunStepKind, string> = {
  [RunStepKind.UNSPECIFIED]: "bg-gray-400",
  [RunStepKind.WARM_UP]: "bg-amber-400",
  [RunStepKind.RUN]: "bg-red-600",
  [RunStepKind.RECOVERY]: "bg-sky-500",
  [RunStepKind.REST]: "bg-gray-400",
  [RunStepKind.COOL_DOWN]: "bg-emerald-500",
};

// FormatStepDuration formats how long a step lasts, e.g. "3 min" or "400 m".
const formatStepDuration = (step: RunStep): string => {
  if (step.durationSeconds > 0) {
    const minutes = Math.floor(step.durationSeconds / 60);
    const seconds = step.durationSeconds % 60;
    if (seconds === 0) {
      return `${minutes} min`;
    }
    return minutes === 0
      ? `${seconds} s`
      : `${minutes}:${String(seconds).padStart(2, "0")} min`;
  }
  if (step.distanceMeters > 0) {
    return step.distanceMeters >= 1000
      ? `${(step.distanceMeters / 1000).toFixed(1)} km`
      : `${Math.round(step.distanceMeters)} m`;
  }
  return "Until lap";
};

// Offset formats an offset in bpm from a threshold, e.g. "−5" or "+3".
const offset = (bpm: number): string => {
  if (bpm === 0) {
    return "";
  }
  return bpm > 0 ? ` +${bpm}` : ` −${Math.abs(bpm)}`;
};

// FormatHeartRate formats a heart rate target, e.g. "AeT −15 to AeT".
const formatHeartRate = (target: HeartRateTarget): string => {
  let name = "";
  if (target.reference === HeartRateReference.AEROBIC_THRESHOLD) {
    name = "AeT";
  } else if (target.reference === HeartRateReference.ANAEROBIC_THRESHOLD) {
    name = "AnT";
  }
  if (name === "") {
    return `${target.lowBpm}–${target.highBpm} bpm`;
  }
  return `${name}${offset(target.lowBpm)} to ${name}${offset(target.highBpm)} bpm`;
};

// FormatTarget formats a step's target, empty if it has none.
const formatTarget = (step: RunStep): string => {
  switch (step.target.case) {
    case "heartRate": {
      return formatHeartRate(step.target.value);
    }
    case "pace": {
      return `${formatPace(step.target.value.fastSecondsPerKilometer).replace(" /km", "")}–${formatPace(step.target.value.slowSecondsPerKilometer)}`;
    }
    default: {
      return "";
    }
  }
};

const Step: React.FC<{ step: RunStep }> = ({ step }) => {
  const target = formatTarget(step);
  return (
    <li className="flex items-start gap-2 py-1.5 text-sm">
      <span
        className={`mt-1.5 inline-block size-2 shrink-0 rounded-full ${KIND_DOTS[step.kind]}`}
      />
      <div className="min-w-0">
        <p className="text-gray-900">
          <span className="font-medium">{KIND_LABELS[step.kind]}</span>{" "}
          <span className="text-gray-700 tabular-nums">
            {formatStepDuration(step)}
          </span>
          {target !== "" && (
            <span className="text-gray-600 tabular-nums"> · {target}</span>
          )}
        </p>
        {step.notes !== "" && (
          <p className="text-xs text-gray-600">{step.notes}</p>
        )}
      </div>
    </li>
  );
};

interface RunStepsProps {
  blocks: RunBlock[];
}

/**
 * RunSteps shows the structure of a run: its steps in order, with the
 * blocks done more than once grouped with how often.
 */
const RunSteps: React.FC<RunStepsProps> = ({ blocks }) => (
  <Card title="Structure">
    <ol className="flex flex-col gap-1">
      {blocks.map((block, index) =>
        block.repeats > 1 ? (
          // The blocks are only listed, never reordered.
          // eslint-disable-next-line react/no-array-index-key
          <li key={index} className="rounded-md bg-gray-50 px-2 py-1">
            <p className="text-xs font-semibold text-gray-700">
              {block.repeats} ×
            </p>
            <ul className="divide-y divide-gray-100">
              {block.steps.map((step, stepIndex) => (
                // eslint-disable-next-line react/no-array-index-key
                <Step key={stepIndex} step={step} />
              ))}
            </ul>
          </li>
        ) : (
          // eslint-disable-next-line react/no-array-index-key
          <li key={index}>
            <ul className="divide-y divide-gray-100">
              {block.steps.map((step, stepIndex) => (
                // eslint-disable-next-line react/no-array-index-key
                <Step key={stepIndex} step={step} />
              ))}
            </ul>
          </li>
        )
      )}
    </ol>
  </Card>
);

export default RunSteps;
