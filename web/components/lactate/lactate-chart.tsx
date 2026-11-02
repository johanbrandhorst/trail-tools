import type { LactateTest } from "gen/lactate/v1/lactate_pb";
import { ThresholdDefinition } from "gen/lactate/v1/lactate_pb";
import { useElementWidth } from "helpers/hooks";
import React, { useRef } from "react";

import { definitionLabel, formatMmol } from "./lactate-utils";

// HEIGHT is the height of the chart in pixels.
const HEIGHT = 240;
// PADDING is the space around the plot for the axes' labels.
const PADDING = { bottom: 28, left: 36, right: 12, top: 12 };
/**
 * MIN_MMOL_RANGE is the least lactate the y axis shows, so a flat test
 * doesn't look steep.
 */
const MIN_MMOL_RANGE = 4;

interface LactateChartProps {
  test: LactateTest;
}

/**
 * LactateChart plots a test's lactate against heart rate, with the
 * definitions of the aerobic threshold as horizontal lines, and the aerobic
 * threshold's heart rate as a vertical one.
 */
const LactateChart: React.FC<LactateChartProps> = ({ test }) => {
  const ref = useRef<HTMLDivElement>(null);
  const width = useElementWidth(ref);
  const { stages } = test;
  if (stages.length === 0) {
    return (
      <p className="text-sm text-gray-500">
        No stages yet. Add the readings of each stage to see the curve.
      </p>
    );
  }
  const heartRates = stages.map((stage) => stage.heartRateBpm);
  const minHeartRate = Math.min(...heartRates) - 5;
  const maxHeartRate = Math.max(...heartRates) + 5;
  const maxMmol = Math.max(
    MIN_MMOL_RANGE,
    ...stages.map((stage) => stage.lactateMmolPerLiter + 0.5),
    ...test.thresholdEstimates.map((est) => est.lactateMmolPerLiter + 0.5)
  );
  const plotWidth = Math.max(width - PADDING.left - PADDING.right, 1);
  const plotHeight = HEIGHT - PADDING.top - PADDING.bottom;
  const xOf = (heartRate: number): number =>
    PADDING.left +
    ((heartRate - minHeartRate) / (maxHeartRate - minHeartRate)) * plotWidth;
  const yOf = (mmol: number): number =>
    PADDING.top + plotHeight - (mmol / maxMmol) * plotHeight;
  const line = stages
    .map(
      (stage) =>
        `${xOf(stage.heartRateBpm).toFixed(1)},${yOf(stage.lactateMmolPerLiter).toFixed(1)}`
    )
    .join(" ");
  const mmolTicks = Array.from(
    { length: Math.floor(maxMmol) + 1 },
    (_tick, index) => index
  );
  const heartRateTicks = heartRates.filter(
    (_heartRate, index) => stages.length <= 10 || index % 2 === 0
  );
  const threshold = test.aerobicThresholdHeartRateBpm;
  const anaerobic = test.anaerobicThresholdHeartRateBpm;

  return (
    <div ref={ref} className="w-full">
      {width > 0 && (
        <svg
          width={width}
          height={HEIGHT}
          role="img"
          aria-label="Blood lactate against heart rate"
          className="text-gray-400"
        >
          {mmolTicks.map((mmol) => (
            <g key={mmol}>
              <line
                x1={PADDING.left}
                x2={width - PADDING.right}
                y1={yOf(mmol)}
                y2={yOf(mmol)}
                className="stroke-gray-100"
              />
              <text
                x={PADDING.left - 6}
                y={yOf(mmol) + 3}
                textAnchor="end"
                className="fill-gray-500 text-[10px] tabular-nums"
              >
                {mmol}
              </text>
            </g>
          ))}
          {heartRateTicks.map((heartRate) => (
            <text
              key={heartRate}
              x={xOf(heartRate)}
              y={HEIGHT - PADDING.bottom + 14}
              textAnchor="middle"
              className="fill-gray-500 text-[10px] tabular-nums"
            >
              {heartRate}
            </text>
          ))}
          <text
            x={width - PADDING.right}
            y={HEIGHT - 2}
            textAnchor="end"
            className="fill-gray-400 text-[10px]"
          >
            bpm
          </text>
          {test.thresholdEstimates.map((estimate) => {
            const primary =
              estimate.definition === ThresholdDefinition.FIXED_2_MMOL;
            return (
              <g key={estimate.definition}>
                <line
                  x1={PADDING.left}
                  x2={width - PADDING.right}
                  y1={yOf(estimate.lactateMmolPerLiter)}
                  y2={yOf(estimate.lactateMmolPerLiter)}
                  strokeDasharray={primary ? undefined : "4 3"}
                  className={primary ? "stroke-ember" : "stroke-gray-300"}
                />
                {primary && (
                  <text
                    x={PADDING.left + 4}
                    y={yOf(estimate.lactateMmolPerLiter) - 3}
                    className="fill-ember text-[10px]"
                  >
                    {definitionLabel(estimate.definition)}
                  </text>
                )}
              </g>
            );
          })}
          {threshold > 0 && (
            <g>
              <line
                x1={xOf(threshold)}
                x2={xOf(threshold)}
                y1={PADDING.top}
                y2={HEIGHT - PADDING.bottom}
                className="stroke-ember"
                strokeDasharray="2 2"
              />
              <text
                x={xOf(threshold) + 4}
                y={PADDING.top + 10}
                className="fill-ember text-[11px] font-semibold tabular-nums"
              >
                {threshold} bpm
              </text>
            </g>
          )}
          {anaerobic > 0 && (
            <g>
              <line
                x1={xOf(anaerobic)}
                x2={xOf(anaerobic)}
                y1={PADDING.top}
                y2={HEIGHT - PADDING.bottom}
                className="stroke-red-700"
                strokeDasharray="2 2"
              />
              <text
                x={xOf(anaerobic) + 4}
                y={PADDING.top + 22}
                className="fill-red-700 text-[11px] font-semibold tabular-nums"
              >
                AnT {anaerobic} bpm
              </text>
            </g>
          )}
          <polyline
            points={line}
            fill="none"
            strokeWidth={2}
            className="stroke-ink"
          />
          {stages.map((stage) => (
            <circle
              key={stage.heartRateBpm}
              cx={xOf(stage.heartRateBpm)}
              cy={yOf(stage.lactateMmolPerLiter)}
              r={3.5}
              className="fill-white stroke-ink"
              strokeWidth={2}
            >
              <title>{`${stage.heartRateBpm} bpm · ${formatMmol(stage.lactateMmolPerLiter)}`}</title>
            </circle>
          ))}
        </svg>
      )}
      {/* The dashed lines are labeled here, as they're often close together. */}
      <ul className="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-xs text-gray-500">
        {test.thresholdEstimates
          .filter((est) => est.definition !== ThresholdDefinition.FIXED_2_MMOL)
          .map((est) => (
            <li key={est.definition} className="flex items-center gap-1.5">
              <span className="inline-block w-4 border-t border-dashed border-gray-400" />
              {definitionLabel(est.definition)}
              {est.definition === ThresholdDefinition.LOWEST_PLUS_1_MMOL &&
                ` (${formatMmol(est.lactateMmolPerLiter)})`}
            </li>
          ))}
      </ul>
    </div>
  );
};

export default LactateChart;
