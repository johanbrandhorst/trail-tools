import { Muscle } from "gen/plans/v1/exercises_pb";
import type { MuscleSet } from "helpers/strength";
import { muscleLabel } from "helpers/strength";
import React from "react";

/**
 * Shape is a region of the body map, on the left side of the figure as seen.
 * Regions marked mirror are drawn on the right side too.
 */
type Shape =
  | { kind: "ellipse"; cx: number; cy: number; rx: number; ry: number }
  | { kind: "path"; data: string };

interface Region {
  // The muscle the region is, or undefined for parts that are no muscle.
  muscle?: Muscle;
  shape: Shape;
  mirror: boolean;
}

const ellipse = (cx: number, cy: number, rx: number, ry: number): Shape => ({
  cx,
  cy,
  kind: "ellipse",
  rx,
  ry,
});

const path = (data: string): Shape => ({ data, kind: "path" });

// The parts both views share: the head, hands and knees.
const SHARED: Region[] = [
  { mirror: false, shape: ellipse(50, 12, 8, 9.5) },
  { mirror: true, shape: ellipse(21, 94, 3.6, 5) },
  { mirror: true, shape: ellipse(41, 125, 4.2, 4) },
];

// FRONT is the figure from the front, 100 wide and 175 high.
const FRONT: Region[] = [
  ...SHARED,
  { mirror: false, muscle: Muscle.NECK, shape: path("M45 21h10l1 7H44z") },
  { mirror: true, muscle: Muscle.SHOULDERS, shape: ellipse(32, 35, 7, 6.5) },
  {
    mirror: true,
    muscle: Muscle.CHEST,
    shape: path("M38 29h11.5v16q-6 3-12-1.5q-2-6 0.5-14.5z"),
  },
  { mirror: true, muscle: Muscle.BICEPS, shape: ellipse(27, 52, 4.4, 9) },
  {
    mirror: true,
    muscle: Muscle.FOREARMS,
    shape: path("M22.5 62q4-1 6 2l-3.5 24h-5l-1-18q0-6 3.5-8z"),
  },
  {
    mirror: false,
    muscle: Muscle.ABS,
    shape: path("M43.5 47h13l-0.5 27q-6 4-12 0z"),
  },
  {
    mirror: true,
    muscle: Muscle.OBLIQUES,
    shape: path("M37 48l5.5 1l0.5 22l-5-4q-2-9-1-19z"),
  },
  {
    mirror: true,
    muscle: Muscle.HIP_ABDUCTORS,
    shape: path("M36.5 70l5 5l-1.5 9l-4.5-2q-1-6 1-12z"),
  },
  {
    mirror: true,
    muscle: Muscle.HIP_FLEXORS,
    shape: path("M42 75l6.5 4l-1 9l-5-2z"),
  },
  {
    mirror: true,
    muscle: Muscle.QUADS,
    shape: path("M36 84l5 2.5l6 3.5l-1 26q-3 5-8 3l-3-14q-1.5-12 1-21z"),
  },
  {
    mirror: true,
    muscle: Muscle.ADDUCTORS,
    shape: path("M47.5 90.5l1.5 1l-1 20l-2.5 3z"),
  },
  {
    mirror: true,
    muscle: Muscle.SHINS,
    shape: path("M38.5 131q3-2 5.5 0l-0.5 26h-4q-2-14-1-26z"),
  },
  { mirror: true, muscle: Muscle.FEET, shape: ellipse(41, 163, 5, 3) },
];

// BACK is the figure from behind, 100 wide and 175 high.
const BACK: Region[] = [
  ...SHARED,
  { mirror: false, muscle: Muscle.NECK, shape: path("M45 21h10l1 6H44z") },
  {
    mirror: false,
    muscle: Muscle.UPPER_BACK,
    shape: path("M44 26h12l11 7l-6 3l-4 10H43l-4-10l-6-3z"),
  },
  { mirror: true, muscle: Muscle.SHOULDERS, shape: ellipse(31.5, 35, 6.5, 6) },
  {
    mirror: true,
    muscle: Muscle.LATS,
    shape: path("M38 38l5 9h5.5l-0.5 13l-6 7q-4-5-5-12q-1-9 1-17z"),
  },
  {
    mirror: false,
    muscle: Muscle.LOWER_BACK,
    shape: path("M44 61h12l1 14H43z"),
  },
  { mirror: true, muscle: Muscle.TRICEPS, shape: ellipse(27, 52, 4.4, 9) },
  {
    mirror: true,
    muscle: Muscle.FOREARMS,
    shape: path("M22.5 62q4-1 6 2l-3.5 24h-5l-1-18q0-6 3.5-8z"),
  },
  {
    mirror: true,
    muscle: Muscle.HIP_ABDUCTORS,
    shape: path("M36 70l6.5 5l-1.5 6l-5.5 3q-1.5-7 0.5-14z"),
  },
  {
    mirror: true,
    muscle: Muscle.GLUTES,
    shape: path("M42.5 76l6.5 1v15q-7 3-13-1l-0.5-6q2-6 7-9z"),
  },
  {
    mirror: true,
    muscle: Muscle.HAMSTRINGS,
    shape: path("M36 94q6 2 12.5 0l-1.5 22q-4 4-9 2q-3-12-2-24z"),
  },
  {
    mirror: true,
    muscle: Muscle.CALVES,
    shape: path("M37 132q4-3 8.5 0q1.5 9-1.5 17l-2 6h-2.5l-1.5-6q-2.5-8-1-17z"),
  },
  { mirror: true, muscle: Muscle.FEET, shape: ellipse(41, 163, 4.5, 3) },
];

const PRIMARY_FILL = "#7c3aed";
const SECONDARY_FILL = "#c4b5fd";
const MUSCLE_FILL = "#e5e7eb";
const BODY_FILL = "#f3f4f6";

const ShapeElement: React.FC<{
  shape: Shape;
  fill: string;
  mirrored: boolean;
}> = ({ shape, fill, mirrored }) => {
  // The right side is the left side mirrored about the middle.
  const transform = mirrored ? "translate(100 0) scale(-1 1)" : undefined;
  switch (shape.kind) {
    case "ellipse": {
      return (
        <ellipse
          cx={shape.cx}
          cy={shape.cy}
          rx={shape.rx}
          ry={shape.ry}
          fill={fill}
          stroke="#ffffff"
          strokeWidth={0.8}
          transform={transform}
        />
      );
    }
    default: {
      return (
        <path
          d={shape.data}
          fill={fill}
          stroke="#ffffff"
          strokeWidth={0.8}
          strokeLinejoin="round"
          transform={transform}
        />
      );
    }
  }
};

interface FigureProps {
  regions: Region[];
  muscles: MuscleSet;
  label: string;
  compact?: boolean;
}

// Figure draws one view of the body with its muscles colored.
const Figure: React.FC<FigureProps> = ({
  regions,
  muscles,
  label,
  compact = false,
}) => (
  <figure
    className={`flex flex-col items-center gap-1 ${compact ? "h-full" : ""}`}
  >
    <svg
      viewBox="12 0 76 170"
      className={`${compact ? "h-full" : "h-56"} w-auto`}
      aria-hidden="true"
    >
      {regions.map((region, index) => {
        let fill = region.muscle === undefined ? BODY_FILL : MUSCLE_FILL;
        if (
          region.muscle !== undefined &&
          muscles.primary.includes(region.muscle)
        ) {
          fill = PRIMARY_FILL;
        } else if (
          region.muscle !== undefined &&
          muscles.secondary.includes(region.muscle)
        ) {
          fill = SECONDARY_FILL;
        }
        return (
          // The regions never change order, so their index is stable.
          // oxlint-disable-next-line no-array-index-key
          <g key={index}>
            <ShapeElement shape={region.shape} fill={fill} mirrored={false} />
            {region.mirror && (
              <ShapeElement shape={region.shape} fill={fill} mirrored />
            )}
          </g>
        );
      })}
    </svg>
    {!compact && (
      <figcaption className="text-xs text-gray-500">{label}</figcaption>
    )}
  </figure>
);

interface BodyMapProps {
  muscles: MuscleSet;
  className?: string;
  /*
   * Draw the figures without captions, filling the height, e.g. for a
   * thumbnail.
   */
  compact?: boolean;
}

/**
 * BodyMap shows the muscles worked on a figure from the front and from
 * behind: those worked hardest in a strong color, and those also worked in
 * a light one.
 */
const BodyMap: React.FC<BodyMapProps> = ({
  muscles,
  className = "",
  compact = false,
}) => {
  const description = [
    muscles.primary.length > 0
      ? `Worked hardest: ${muscles.primary.map(muscleLabel).join(", ")}.`
      : "",
    muscles.secondary.length > 0
      ? `Also worked: ${muscles.secondary.map(muscleLabel).join(", ")}.`
      : "",
  ]
    .filter((sentence) => sentence !== "")
    .join(" ");
  return (
    <div
      role="img"
      aria-label={description === "" ? "No muscles set" : description}
      className={`flex justify-center ${compact ? "gap-0.5" : "gap-6"} ${className}`}
    >
      <Figure
        regions={FRONT}
        muscles={muscles}
        label="Front"
        compact={compact}
      />
      <Figure regions={BACK} muscles={muscles} label="Back" compact={compact} />
    </div>
  );
};

// MuscleChips lists the muscles worked, the hardest worked filled.
export const MuscleChips: React.FC<{ muscles: MuscleSet }> = ({ muscles }) => (
  <ul className="flex flex-wrap gap-1.5">
    {muscles.primary.map((muscle) => (
      <li
        key={muscle}
        className="rounded-full bg-violet-100 px-2.5 py-0.5 text-sm font-medium text-violet-900"
      >
        {muscleLabel(muscle)}
      </li>
    ))}
    {muscles.secondary.map((muscle) => (
      <li
        key={muscle}
        className="rounded-full bg-white px-2.5 py-0.5 text-sm text-gray-700 ring-1 ring-gray-300 ring-inset"
      >
        {muscleLabel(muscle)}
      </li>
    ))}
  </ul>
);

export default BodyMap;
