import { useElementWidth } from "helpers/hooks";
import type { PointerEvent } from "react";
import React, { useId, useMemo, useRef, useState } from "react";

export interface ProfilePoint {
  distanceMeters: number;
  elevationMeters: number;
}

export interface ProfileMarker {
  distanceMeters: number;
  label: string;
}

// ProfileRange is a stretch of the profile, e.g. a section of a story.
export interface ProfileRange {
  startMeters: number;
  endMeters: number;
  // A short label drawn in the stretch, if it fits.
  label?: string;
  // Active stretches are highlighted.
  active?: boolean;
}

// MAX_LABEL_ROWS is the number of rows marker labels are spread over.
const MAX_LABEL_ROWS = 3;
// LABEL_ROW_HEIGHT is the height of a row of marker labels in pixels.
const LABEL_ROW_HEIGHT = 11;

// MarkerLabel shortens a marker's label to fit above the profile.
const markerLabel = (marker: ProfileMarker): string =>
  marker.label.length > 14 ? `${marker.label.slice(0, 13)}…` : marker.label;

/**
 * LabelRows assigns each marker label, centered at its x position, the first
 * row where it doesn't overlap another label, or -1 if no row has room.
 */
const labelRows = (xs: number[], labels: string[]): number[] => {
  // The right edge of the last label in each row.
  const rowEnds: number[] = [];
  const rows = xs.map(() => -1);
  const order = xs.map((_value, index) => index);
  order.sort((first, second) => (xs[first] ?? 0) - (xs[second] ?? 0));
  for (const index of order) {
    // About 6 pixels per character at 10px, and a gap.
    const halfWidth = ((labels[index] ?? "").length * 6) / 2 + 2;
    const center = xs[index] ?? 0;
    let row = rowEnds.findIndex((end) => end <= center - halfWidth);
    if (row < 0 && rowEnds.length < MAX_LABEL_ROWS) {
      row = rowEnds.length;
    }
    if (row >= 0) {
      rowEnds[row] = center + halfWidth;
      rows[index] = row;
    }
  }
  return rows;
};

interface ElevationProfileProps {
  points: ProfilePoint[];
  // An optional second series with one value per point, e.g. heart rate.
  // Zero values are treated as missing.
  secondary?: number[];
  secondaryLabel?: string;
  markers?: ProfileMarker[];
  // Stretches shaded behind the profile, e.g. the sections of a story.
  ranges?: ProfileRange[];
  /**
   * An optional reference line in meters of elevation, e.g. the freezing
   * level along the route. Parts far above the profile are not drawn.
   */
  levelLine?: ProfilePoint[];
  levelLabel?: string;
  // An optional highlighted position in meters, e.g. the runner's position.
  positionMeters?: number | undefined;
  positionLabel?: string;
  height?: number;
  // Called with the index of the hovered point, or null when not hovering.
  onHover?: (index: number | null) => void;
  // Called with the distance clicked, e.g. to split a section there.
  onSelectDistance?: ((distanceMeters: number) => void) | undefined;
  // Colors the profile in the icy colors of a ski tour.
  snow?: boolean;
}

// Palette is the colors of the profile's highlights.
interface Palette {
  // The colors ranges alternate between, so neighbors differ.
  bands: string[];
  activeBand: string;
  // The fill and line of the active range and the position.
  fill: string;
  line: string;
}

const TRAIL_PALETTE: Palette = {
  activeBand: "#f5c9a6",
  bands: ["#f5f5f4", "#fdf0e6"],
  fill: "#d2601a",
  line: "#b4500f",
};

const SNOW_PALETTE: Palette = {
  activeBand: "#bae6fd",
  bands: ["#f1f5f9", "#e0f2fe"],
  fill: "#0284c7",
  line: "#075985",
};

const NO_MARKERS: ProfileMarker[] = [];
const NO_RANGES: ProfileRange[] = [];
const NO_LEVELS: ProfilePoint[] = [];

const NO_POINT: ProfilePoint = { distanceMeters: 0, elevationMeters: 0 };

// BandFill is the color of a range: active ranges stand out, others cycle.
const bandFill = (
  range: ProfileRange,
  index: number,
  palette: Palette
): string => {
  if (range.active === true) {
    return palette.activeBand;
  }
  return palette.bands[index % palette.bands.length] ?? "#f5f5f4";
};

// FitLabel shortens a label to about the width in pixels, with an ellipsis.
const fitLabel = (label: string, width: number): string => {
  const characters = Math.floor(width / 5.5);
  if (label.length <= characters) {
    return label;
  }
  return `${label.slice(0, Math.max(characters - 1, 1)).trimEnd()}…`;
};

// PointAt returns the point at an index that callers keep within range.
const pointAt = (points: ProfilePoint[], index: number): ProfilePoint =>
  points[index] ?? NO_POINT;

/**
 * NearestIndex finds the index of the point closest to the distance using a
 * binary search. Points must be sorted by distance.
 */
const nearestIndex = (points: ProfilePoint[], distance: number): number => {
  let low = 0;
  let high = points.length - 1;
  while (high - low > 1) {
    const middle = Math.floor((low + high) / 2);
    if (pointAt(points, middle).distanceMeters < distance) {
      low = middle;
    } else {
      high = middle;
    }
  }
  const lowGap = Math.abs(pointAt(points, low).distanceMeters - distance);
  const highGap = Math.abs(pointAt(points, high).distanceMeters - distance);
  return lowGap <= highGap ? low : high;
};

// ElevationAt linearly interpolates the elevation at a distance.
const elevationAt = (points: ProfilePoint[], distance: number): number => {
  const index = nearestIndex(points, distance);
  const point = pointAt(points, index);
  let other = index + 1;
  if (point.distanceMeters > distance) {
    other = index - 1;
  }
  if (other < 0 || other >= points.length) {
    return point.elevationMeters;
  }
  const neighbor = pointAt(points, other);
  const span = neighbor.distanceMeters - point.distanceMeters;
  if (span === 0) {
    return point.elevationMeters;
  }
  const fraction = (distance - point.distanceMeters) / span;
  return (
    point.elevationMeters +
    fraction * (neighbor.elevationMeters - point.elevationMeters)
  );
};

/**
 * GradeAt returns the grade in percent around a distance, measured over a
 * window of at least 100 meters to smooth out noise.
 */
const gradeAt = (
  points: ProfilePoint[],
  distance: number,
  window: number
): number => {
  const half = Math.max(window, 100) / 2;
  const before = elevationAt(points, distance - half);
  const after = elevationAt(points, distance + half);
  return ((after - before) / (half * 2)) * 100;
};

const SMOOTHING_HALF_WINDOW_METERS = 50;

/**
 * SmoothProfile returns the profile with each elevation replaced by the
 * average elevation within 50 meters, to remove elevation model noise.
 */
const smoothProfile = (points: ProfilePoint[]): ProfilePoint[] => {
  const result: ProfilePoint[] = [];
  let low = 0;
  let high = 0;
  let sum = 0;
  for (const point of points) {
    const center = point.distanceMeters;
    while (
      high < points.length &&
      pointAt(points, high).distanceMeters <=
        center + SMOOTHING_HALF_WINDOW_METERS
    ) {
      sum += pointAt(points, high).elevationMeters;
      high++;
    }
    while (
      low < high &&
      pointAt(points, low).distanceMeters <
        center - SMOOTHING_HALF_WINDOW_METERS
    ) {
      sum -= pointAt(points, low).elevationMeters;
      low++;
    }
    result.push({
      distanceMeters: center,
      elevationMeters: high > low ? sum / (high - low) : point.elevationMeters,
    });
  }
  return result;
};

const ELEVATION_STEPS = [10, 20, 25, 50, 100, 200, 250, 500, 1000, 2000];
const DISTANCE_STEPS_KM = [0.1, 0.2, 0.5, 1, 2, 5, 10, 20, 25, 50, 100];

const chooseStep = (range: number, maxTicks: number, steps: number[]) => {
  for (const step of steps) {
    if (range / step <= maxTicks) {
      return step;
    }
  }
  return steps.at(-1) ?? 1;
};

const ticksFor = (min: number, max: number, step: number): number[] => {
  const ticks: number[] = [];
  for (let tick = Math.ceil(min / step) * step; tick <= max; tick += step) {
    ticks.push(Math.round(tick * 1000) / 1000);
  }
  return ticks;
};

/**
 * ElevationProfile draws an elevation profile as an SVG area chart, with a
 * hover crosshair.
 */
const ElevationProfile: React.FC<ElevationProfileProps> = ({
  points,
  secondary,
  secondaryLabel = "",
  markers = NO_MARKERS,
  ranges = NO_RANGES,
  levelLine = NO_LEVELS,
  levelLabel = "",
  positionMeters,
  positionLabel = "",
  height = 200,
  onHover,
  onSelectDistance,
  snow = false,
}) => {
  const palette = snow ? SNOW_PALETTE : TRAIL_PALETTE;
  const containerRef = useRef<HTMLDivElement>(null);
  // The ID of the clip path of the active range, without React's symbols.
  const clipId = `profile-${useId().replaceAll(/[^\w-]/gu, "")}`;
  const width = useElementWidth(containerRef);
  const [hoverIndex, setHoverIndex] = useState<number | null>(null);
  const smoothed = useMemo(() => smoothProfile(points), [points]);

  const [first] = points;
  const last = points.at(-1);
  if (points.length < 2 || first === undefined || last === undefined) {
    /**
     * The measured container is rendered without data too, so that its width
     * is known when data arrives, e.g. while drawing a new route.
     */
    return (
      <div ref={containerRef} className="relative w-full select-none">
        <div className="flex h-24 items-center justify-center text-sm text-gray-500">
          No elevation data
        </div>
      </div>
    );
  }

  const hasSecondary =
    secondary !== undefined &&
    secondary.length === points.length &&
    secondary.some((value) => value > 0);
  const marginLeft = 44;
  const marginRight = hasSecondary ? 40 : 12;
  const marginBottom = 22;
  const plotWidth = Math.max(width - marginLeft - marginRight, 10);
  const minDistance = first.distanceMeters;
  const maxDistance = Math.max(last.distanceMeters, minDistance + 1);
  const xFor = (distance: number): number =>
    marginLeft +
    ((distance - minDistance) / (maxDistance - minDistance)) * plotWidth;

  // Marker labels are spread over rows above the plot, so they don't overlap.
  const markerLabels = markers.map(markerLabel);
  const markerRows = labelRows(
    markers.map((marker) => xFor(marker.distanceMeters)),
    markerLabels
  );
  const labelRowCount = Math.max(...markerRows, -1) + 1;
  const marginTop =
    markers.length > 0 ? 8 + Math.max(labelRowCount, 1) * LABEL_ROW_HEIGHT : 10;
  const plotHeight = height - marginTop - marginBottom;
  let minElevation = Infinity;
  let maxElevation = -Infinity;
  for (const point of points) {
    minElevation = Math.min(minElevation, point.elevationMeters);
    maxElevation = Math.max(maxElevation, point.elevationMeters);
  }
  /**
   * Include the level line in the vertical range when it is reasonably close
   * to the profile, so that it is visible without flattening the profile.
   */
  const profileRange = Math.max(maxElevation - minElevation, 50);
  for (const level of levelLine) {
    if (level.elevationMeters < maxElevation + profileRange) {
      minElevation = Math.min(minElevation, level.elevationMeters);
      maxElevation = Math.max(maxElevation, level.elevationMeters);
    }
  }
  const elevationRange = Math.max(maxElevation - minElevation, 50);
  const elevationStep = chooseStep(elevationRange * 1.2, 4, ELEVATION_STEPS);
  // The axis only goes below sea level when the profile does.
  const yMin = Math.max(
    Math.floor((minElevation - elevationRange * 0.05) / elevationStep) *
      elevationStep,
    Math.min(minElevation, 0)
  );
  const yMax =
    Math.ceil((maxElevation + elevationRange * 0.05) / elevationStep) *
    elevationStep;

  const yFor = (elevation: number): number =>
    marginTop + (1 - (elevation - yMin) / (yMax - yMin)) * plotHeight;
  const baseline = marginTop + plotHeight;

  // The outline and the area use at most about one point per pixel.
  const stride = Math.max(1, Math.floor(points.length / plotWidth));

  const pointString = (distance: number, elevation: number): string =>
    `${xFor(distance).toFixed(1)},${yFor(elevation).toFixed(1)}`;

  const outline: string[] = [];
  for (let index = 0; index < points.length; index += stride) {
    const point = pointAt(points, index);
    outline.push(pointString(point.distanceMeters, point.elevationMeters));
  }
  outline.push(pointString(last.distanceMeters, last.elevationMeters));
  const area = `M${xFor(minDistance)},${baseline} L${outline.join(" L")} L${xFor(last.distanceMeters)},${baseline} Z`;

  let secondaryPath = "";
  let secondaryMin = 0;
  let secondaryMax = 0;
  if (hasSecondary) {
    secondaryMin = Infinity;
    secondaryMax = -Infinity;
    for (const value of secondary) {
      if (value > 0) {
        secondaryMin = Math.min(secondaryMin, value);
        secondaryMax = Math.max(secondaryMax, value);
      }
    }
    const span = Math.max(secondaryMax - secondaryMin, 10);
    const commands: string[] = [];
    let drawing = false;
    for (let index = 0; index < points.length; index += stride) {
      const value = secondary[index] ?? 0;
      if (value <= 0) {
        drawing = false;
        continue;
      }
      const xValue = xFor(pointAt(points, index).distanceMeters).toFixed(1);
      const yValue = (
        marginTop +
        (1 - (value - secondaryMin) / span) * plotHeight
      ).toFixed(1);
      commands.push(`${drawing ? "L" : "M"}${xValue},${yValue}`);
      drawing = true;
    }
    secondaryPath = commands.join(" ");
  }

  const levelCommands: string[] = [];
  let levelDrawing = false;
  for (const level of levelLine) {
    if (level.elevationMeters < yMin || level.elevationMeters > yMax) {
      levelDrawing = false;
      continue;
    }
    levelCommands.push(
      `${levelDrawing ? "L" : "M"}${xFor(level.distanceMeters).toFixed(1)},${yFor(level.elevationMeters).toFixed(1)}`
    );
    levelDrawing = true;
  }
  const levelPath = levelCommands.join(" ");

  const distanceRangeKm = (maxDistance - minDistance) / 1000;
  const distanceStep = chooseStep(
    distanceRangeKm,
    Math.max(Math.floor(plotWidth / 70), 2),
    DISTANCE_STEPS_KM
  );
  const distanceTicks = ticksFor(
    minDistance / 1000,
    maxDistance / 1000,
    distanceStep
  );
  const elevationTicks = ticksFor(yMin, yMax, elevationStep);

  // DistanceAt returns the distance under the pointer, if it's over the plot.
  const distanceAt = (event: PointerEvent<SVGSVGElement>) => {
    const rect = event.currentTarget.getBoundingClientRect();
    const xPosition = event.clientX - rect.left;
    if (xPosition < marginLeft || xPosition > marginLeft + plotWidth) {
      return undefined;
    }
    return (
      minDistance +
      ((xPosition - marginLeft) / plotWidth) * (maxDistance - minDistance)
    );
  };
  const handlePointer = (event: PointerEvent<SVGSVGElement>) => {
    const distance = distanceAt(event);
    if (distance === undefined) {
      setHoverIndex(null);
      onHover?.(null);
      return;
    }
    const index = nearestIndex(points, distance);
    setHoverIndex(index);
    onHover?.(index);
  };

  const hovered = hoverIndex === null ? undefined : points.at(hoverIndex);
  const bands = ranges.map((range) => ({
    ...range,
    x1: xFor(Math.max(range.startMeters, minDistance)),
    x2: xFor(Math.min(range.endMeters, maxDistance)),
  }));
  const active = bands.find((band) => band.active === true);
  const hoveredSecondary =
    hoverIndex !== null && hasSecondary ? (secondary[hoverIndex] ?? 0) : 0;

  return (
    <div ref={containerRef} className="relative w-full select-none">
      {width > 0 && (
        <svg
          width={width}
          height={height}
          className={`block touch-pan-y ${onSelectDistance === undefined ? "" : "cursor-crosshair"}`}
          onPointerMove={handlePointer}
          onPointerDown={handlePointer}
          onPointerUp={(event) => {
            const distance = distanceAt(event);
            if (distance !== undefined) {
              onSelectDistance?.(distance);
            }
          }}
          onPointerLeave={() => {
            setHoverIndex(null);
            onHover?.(null);
          }}
        >
          {bands.map((band, index) => (
            <g key={`band-${band.startMeters}-${band.endMeters}`}>
              <rect
                x={band.x1}
                y={marginTop}
                width={Math.max(band.x2 - band.x1, 0)}
                height={plotHeight}
                fill={bandFill(band, index, palette)}
              />
              {index > 0 && (
                <line
                  x1={band.x1}
                  x2={band.x1}
                  y1={marginTop}
                  y2={baseline}
                  stroke="#78716c"
                  strokeWidth={1.5}
                />
              )}
            </g>
          ))}
          {elevationTicks.map((tick) => (
            <g key={`y-${tick}`}>
              <line
                x1={marginLeft}
                x2={marginLeft + plotWidth}
                y1={yFor(tick)}
                y2={yFor(tick)}
                stroke="#e5e7eb"
              />
              <text
                x={marginLeft - 6}
                y={yFor(tick)}
                textAnchor="end"
                dominantBaseline="middle"
                className="fill-gray-500 text-[10px]"
              >
                {tick}
              </text>
            </g>
          ))}
          <path d={area} fill="#292524" fillOpacity={0.12} stroke="none" />
          <polyline
            points={outline.join(" ")}
            fill="none"
            stroke="#292524"
            strokeWidth={1.5}
            strokeLinejoin="round"
          />
          {active !== undefined && (
            <>
              <clipPath id={clipId}>
                <rect
                  x={active.x1}
                  y={0}
                  width={Math.max(active.x2 - active.x1, 0)}
                  height={height}
                />
              </clipPath>
              <g clipPath={`url(#${clipId})`}>
                <path
                  d={area}
                  fill={palette.fill}
                  opacity={0.35}
                  stroke="none"
                />
                <polyline
                  points={outline.join(" ")}
                  fill="none"
                  stroke={palette.line}
                  strokeWidth={2}
                  strokeLinejoin="round"
                />
              </g>
            </>
          )}
          {bands.map(
            (band) =>
              band.label !== undefined &&
              band.x2 - band.x1 > 48 && (
                <text
                  key={`label-${band.startMeters}-${band.endMeters}`}
                  x={band.x1 + 4}
                  y={marginTop + 11}
                  className={`text-[10px] ${band.active === true ? "fill-brand-800 font-semibold" : "fill-gray-700 font-medium"}`}
                >
                  {fitLabel(band.label, band.x2 - band.x1 - 8)}
                </text>
              )
          )}
          {secondaryPath !== "" && (
            <path
              d={secondaryPath}
              fill="none"
              stroke="#dc2626"
              strokeWidth={1}
              opacity={0.8}
            />
          )}
          {levelPath !== "" && (
            <path
              d={levelPath}
              fill="none"
              stroke="#2563eb"
              strokeDasharray="5 3"
              strokeWidth={1.5}
            />
          )}
          {hasSecondary && (
            <>
              <text
                x={marginLeft + plotWidth + 4}
                y={marginTop}
                dominantBaseline="hanging"
                className="fill-red-600 text-[10px]"
              >
                {Math.round(secondaryMax)}
              </text>
              <text
                x={marginLeft + plotWidth + 4}
                y={marginTop + plotHeight}
                className="fill-red-600 text-[10px]"
              >
                {Math.round(secondaryMin)}
              </text>
            </>
          )}
          {distanceTicks.map((tick) => (
            <text
              key={`x-${tick}`}
              x={xFor(tick * 1000)}
              y={height - 6}
              textAnchor="middle"
              className="fill-gray-500 text-[10px]"
            >
              {tick} km
            </text>
          ))}
          {markers.map((marker, index) => {
            const xMarker = xFor(marker.distanceMeters);
            const row = markerRows[index] ?? -1;
            return (
              <g key={`marker-${marker.distanceMeters}-${marker.label}`}>
                <title>{marker.label}</title>
                <line
                  x1={xMarker}
                  x2={xMarker}
                  y1={marginTop - 4}
                  y2={baseline}
                  stroke="#1f2937"
                  strokeDasharray="3 3"
                  strokeWidth={1}
                />
                {row >= 0 && (
                  <text
                    x={xMarker}
                    y={10 + row * LABEL_ROW_HEIGHT}
                    textAnchor="middle"
                    className="fill-gray-700 text-[10px] font-medium"
                  >
                    {markerLabels[index]}
                  </text>
                )}
              </g>
            );
          })}
          {positionMeters !== undefined &&
            positionMeters >= minDistance &&
            positionMeters <= maxDistance && (
              <g pointerEvents="none">
                <line
                  x1={xFor(positionMeters)}
                  x2={xFor(positionMeters)}
                  y1={marginTop}
                  y2={baseline}
                  stroke={palette.line}
                  strokeWidth={2}
                />
                <circle
                  cx={xFor(positionMeters)}
                  cy={yFor(elevationAt(points, positionMeters))}
                  r={6}
                  fill={palette.fill}
                  stroke="#ffffff"
                  strokeWidth={2}
                />
                {positionLabel !== "" && (
                  <text
                    x={xFor(positionMeters) + 8}
                    y={yFor(elevationAt(points, positionMeters)) - 8}
                    className="fill-brand-700 text-[11px] font-semibold"
                  >
                    {positionLabel}
                  </text>
                )}
              </g>
            )}
          {hovered !== undefined && (
            <g pointerEvents="none">
              <line
                x1={xFor(hovered.distanceMeters)}
                x2={xFor(hovered.distanceMeters)}
                y1={marginTop}
                y2={baseline}
                stroke="#111827"
                strokeWidth={1}
              />
              <circle
                cx={xFor(hovered.distanceMeters)}
                cy={yFor(hovered.elevationMeters)}
                r={4}
                fill="#facc15"
                stroke="#111827"
              />
            </g>
          )}
        </svg>
      )}
      {hovered !== undefined && hoverIndex !== null && (
        <div
          className="pointer-events-none absolute top-1 rounded bg-gray-900/90 px-2 py-1 text-xs whitespace-nowrap text-white tabular-nums"
          style={{
            left: Math.min(
              Math.max(xFor(hovered.distanceMeters) + 8, 0),
              Math.max(width - 150, 0)
            ),
          }}
        >
          {(hovered.distanceMeters / 1000).toFixed(2)} km ·{" "}
          {Math.round(hovered.elevationMeters)} m ·{" "}
          {gradeAt(smoothed, hovered.distanceMeters, 100).toFixed(1)}%
          {hoveredSecondary > 0 &&
            ` · ${Math.round(hoveredSecondary)} ${secondaryLabel}`}
        </div>
      )}
      {(levelLine.length > 0 || hasSecondary) && (
        <div className="mt-1 flex flex-wrap items-center gap-3 text-[11px] text-gray-500">
          {levelLine.length > 0 && (
            <span className="inline-flex items-center gap-1">
              <span className="inline-block w-3 border-t-2 border-dashed border-blue-600" />
              {levelPath === "" ? `${levelLabel} (above chart)` : levelLabel}
            </span>
          )}
          {hasSecondary && (
            <span className="inline-flex items-center gap-1">
              <span className="inline-block h-0.5 w-3 bg-red-600" />
              {secondaryLabel === "" ? "Secondary" : secondaryLabel}
            </span>
          )}
        </div>
      )}
    </div>
  );
};

export default ElevationProfile;
