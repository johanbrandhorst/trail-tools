import { ProgressBar } from "@components/ui/misc";
import { formatHoursMinutes } from "helpers/format";
import React from "react";

interface TargetRowProps {
  label: string;
  actual: number;
  target: number;
  planned: number;
  format: (value: number) => string;
  barClass: string;
}

const TargetRow: React.FC<TargetRowProps> = ({
  label,
  actual,
  target,
  planned,
  format,
  barClass,
}) => (
  /*
   * The label sits above the values, and each value wraps whole, so three
   * columns fit a phone without breaking "43.0 km" across lines.
   */
  <div className="flex min-w-0 flex-col gap-0.5">
    <span className="text-xs text-gray-500">{label}</span>
    <span className="text-xs text-gray-800 tabular-nums">
      <span className="font-semibold whitespace-nowrap">{format(actual)}</span>
      {target > 0 && (
        <>
          {" "}
          <span className="whitespace-nowrap text-gray-500">
            / {format(target)}
          </span>
        </>
      )}
    </span>
    <ProgressBar value={actual} target={target} className={barClass} />
    {planned > 0 && (
      <span className="text-[10px] text-gray-400">
        <span className="whitespace-nowrap">{format(planned)}</span> in workouts
      </span>
    )}
  </div>
);

const km = (meters: number): string => `${(meters / 1000).toFixed(1)} km`;
const meters = (value: number): string => `${Math.round(value)} m`;

export interface TargetValues {
  distanceMeters: number;
  elevationGainMeters: number;
  durationSeconds: number;
}

interface TargetsProps {
  actual: TargetValues;
  target: TargetValues;
  planned?: TargetValues;
}

// Targets compares actual totals against period targets.
const Targets: React.FC<TargetsProps> = ({ actual, target, planned }) => (
  <div className="grid grid-cols-3 gap-3">
    <TargetRow
      label="Distance"
      actual={actual.distanceMeters}
      target={target.distanceMeters}
      planned={planned?.distanceMeters ?? 0}
      format={km}
      barClass="bg-stone-800"
    />
    <TargetRow
      label="Gain"
      actual={actual.elevationGainMeters}
      target={target.elevationGainMeters}
      planned={planned?.elevationGainMeters ?? 0}
      format={meters}
      barClass="bg-brand-500"
    />
    <TargetRow
      label="Time"
      actual={actual.durationSeconds}
      target={target.durationSeconds}
      planned={planned?.durationSeconds ?? 0}
      format={formatHoursMinutes}
      barClass="bg-stone-400"
    />
  </div>
);

export default Targets;
