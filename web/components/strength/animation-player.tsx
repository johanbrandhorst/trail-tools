import { PauseIcon, PlayIcon } from "@heroicons/react/24/solid";
import type { ExerciseAnimation } from "gen/plans/v1/exercises_pb";
import { AnimationPropKind, BodyPoint } from "gen/plans/v1/exercises_pb";
import type { Figure, PlacedProp, Point, Skeleton } from "helpers/animation";
import { HEAD_RADIUS, layOut, pointOf } from "helpers/animation";
import React, { useEffect, useMemo, useRef, useState } from "react";

// Limb draws a segment of the figure.
const Limb: React.FC<{ from: Point; to: Point; far?: boolean }> = ({
  from,
  to,
  far = false,
}) => (
  <line
    x1={from.left}
    y1={from.top}
    x2={to.left}
    y2={to.top}
    stroke={far ? "#a8a29e" : "#111111"}
    strokeWidth={7}
    strokeLinecap="round"
  />
);

// HeldProp draws a weight in a hand.
const HeldProp: React.FC<{ kind: AnimationPropKind; at: Point }> = ({
  kind,
  at,
}) => {
  if (kind === AnimationPropKind.KETTLEBELL) {
    return (
      <g>
        <circle cx={at.left} cy={at.top + 9} r={7.5} fill="#d2601a" />
        <path
          d={`M${at.left - 4} ${at.top + 3} q4 -7 8 0`}
          stroke="#d2601a"
          strokeWidth={2.5}
          fill="none"
        />
      </g>
    );
  }
  return (
    <rect
      x={at.left - 11}
      y={at.top - 3.5}
      width={22}
      height={8}
      rx={3}
      fill="#d2601a"
    />
  );
};

interface StageProps {
  figure: Figure;
  skeleton: Skeleton;
  highlight: BodyPoint | undefined;
}

// Stage draws the floor, the equipment and the figure in a pose.
const Stage: React.FC<StageProps> = ({ figure, skeleton, highlight }) => {
  const { viewBox, floorY, props } = figure;
  const supports = props.filter(
    (prop) =>
      prop.kind === AnimationPropKind.BENCH ||
      prop.kind === AnimationPropKind.STEP
  );
  const held = (point: BodyPoint): PlacedProp | undefined =>
    props.find(
      (prop) =>
        prop.point === point &&
        (prop.kind === AnimationPropKind.DUMBBELL ||
          prop.kind === AnimationPropKind.KETTLEBELL)
    );
  const nearHeld = held(BodyPoint.NEAR_HAND);
  const farHeld = held(BodyPoint.FAR_HAND);
  const highlighted =
    highlight === undefined || highlight === BodyPoint.UNSPECIFIED
      ? undefined
      : pointOf(skeleton, highlight);
  return (
    <svg
      viewBox={`${viewBox.left} ${viewBox.top} ${viewBox.width} ${viewBox.height}`}
      className="block h-auto w-full"
      aria-hidden="true"
    >
      <line
        x1={viewBox.left}
        x2={viewBox.left + viewBox.width}
        y1={floorY}
        y2={floorY}
        stroke="#d6d3d1"
        strokeWidth={2}
      />
      {props.some((prop) => prop.kind === AnimationPropKind.MAT) && (
        <rect
          x={viewBox.left + 12}
          y={floorY - 3}
          width={viewBox.width - 24}
          height={3}
          rx={1.5}
          fill="#c4b5fd"
        />
      )}
      {supports.map((support) => {
        const width = support.kind === AnimationPropKind.BENCH ? 66 : 40;
        /*
         * The top is just under the supported point, as feet and
         * shoulders have some thickness.
         */
        const top = support.at.top + 4;
        return (
          <g key={`${support.kind}-${support.point}`} fill="#a8a29e">
            <rect
              x={support.at.left - width / 2}
              y={top}
              width={width}
              height={8}
              rx={3}
            />
            {support.kind === AnimationPropKind.BENCH ? (
              <>
                <rect
                  x={support.at.left - width / 2 + 7}
                  y={top + 8}
                  width={6}
                  height={Math.max(floorY - top - 8, 0)}
                />
                <rect
                  x={support.at.left + width / 2 - 13}
                  y={top + 8}
                  width={6}
                  height={Math.max(floorY - top - 8, 0)}
                />
              </>
            ) : (
              <rect
                x={support.at.left - width / 2}
                y={top + 8}
                width={width}
                height={Math.max(floorY - top - 8, 0)}
                opacity={0.6}
              />
            )}
          </g>
        );
      })}
      {props
        .filter((prop) => prop.kind === AnimationPropKind.BAR)
        .map((prop) => (
          <line
            key={`bar-${prop.point}`}
            x1={prop.at.left - 40}
            x2={prop.at.left + 40}
            y1={prop.at.top}
            y2={prop.at.top}
            stroke="#57534e"
            strokeWidth={3}
          />
        ))}
      {/* The far limbs are behind the body, the near ones in front. */}
      <Limb from={skeleton.shoulders} to={skeleton.farElbow} far />
      <Limb from={skeleton.farElbow} to={skeleton.farHand} far />
      <Limb from={skeleton.hips} to={skeleton.farKnee} far />
      <Limb from={skeleton.farKnee} to={skeleton.farFoot} far />
      <Limb from={skeleton.farFoot} to={skeleton.farToe} far />
      {farHeld !== undefined && (
        <HeldProp kind={farHeld.kind} at={skeleton.farHand} />
      )}
      <Limb from={skeleton.hips} to={skeleton.shoulders} />
      <Limb from={skeleton.hips} to={skeleton.nearKnee} />
      <Limb from={skeleton.nearKnee} to={skeleton.nearFoot} />
      <Limb from={skeleton.nearFoot} to={skeleton.nearToe} />
      <circle
        cx={skeleton.head.left}
        cy={skeleton.head.top}
        r={HEAD_RADIUS}
        fill="#111111"
      />
      <Limb from={skeleton.shoulders} to={skeleton.nearElbow} />
      <Limb from={skeleton.nearElbow} to={skeleton.nearHand} />
      {nearHeld !== undefined && (
        <HeldProp kind={nearHeld.kind} at={skeleton.nearHand} />
      )}
      {highlighted !== undefined && (
        <circle
          cx={highlighted.left}
          cy={highlighted.top}
          r={11}
          fill="none"
          stroke="#7c3aed"
          strokeWidth={2}
          strokeDasharray="3 3"
        />
      )}
    </svg>
  );
};

/*
 * PrefersReducedMotion reports whether the user asked for less motion, so
 * animations start paused.
 */
const prefersReducedMotion = (): boolean =>
  globalThis.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;

interface AnimationPlayerProps {
  animations: ExerciseAnimation[];
}

/**
 * AnimationPlayer plays an exercise's stick figure animations in a loop,
 * with their cues, and switches between them.
 */
const AnimationPlayer: React.FC<AnimationPlayerProps> = ({ animations }) => {
  const [selected, setSelected] = useState(0);
  const [playing, setPlaying] = useState(() => !prefersReducedMotion());
  const [elapsed, setElapsed] = useState(0);
  const last = useRef<number | undefined>(undefined);
  const figures = useMemo(() => animations.map(layOut), [animations]);
  const figure = figures[selected] ?? figures[0];

  useEffect(() => {
    if (!playing) {
      last.current = undefined;
      return undefined;
    }
    let frame = 0;
    const tick = (now: number) => {
      if (last.current !== undefined) {
        const step = now - last.current;
        setElapsed((total) => total + step);
      }
      last.current = now;
      frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);
    return () => {
      cancelAnimationFrame(frame);
    };
  }, [playing]);

  if (figure === undefined) {
    return null;
  }
  const current = figure.frameAt(elapsed);
  const cue = current.keyframe?.cue ?? "";
  return (
    <section className="overflow-hidden rounded-lg border border-gray-200 bg-white">
      <div
        className="relative bg-stone-50"
        role="img"
        aria-label={`Animation of ${figure.animation.title.toLowerCase()}: ${figure.animation.keyframes
          .map((keyframe) => keyframe.cue)
          .filter((text) => text !== "")
          .join(", then ")}`}
      >
        <Stage
          figure={figure}
          skeleton={current.skeleton}
          highlight={current.holding ? current.keyframe?.highlight : undefined}
        />
        {cue !== "" && (
          <span className="absolute top-2.5 left-3 rounded-full border border-gray-200 bg-white px-2.5 py-0.5 text-xs font-semibold">
            {cue}
          </span>
        )}
      </div>
      <div className="flex items-center gap-2.5 border-t border-gray-100 px-3 py-2.5">
        <button
          type="button"
          aria-label={playing ? "Pause" : "Play"}
          title={playing ? "Pause" : "Play"}
          className="flex size-11 shrink-0 items-center justify-center rounded-full bg-ink text-white"
          onClick={() => {
            setPlaying((was) => !was);
          }}
        >
          {playing ? (
            <PauseIcon className="size-5" />
          ) : (
            <PlayIcon className="size-5" />
          )}
        </button>
        <div className="flex min-w-0 flex-wrap gap-1.5">
          {animations.map((animation, index) => (
            <button
              // Animations keep their order.
              // oxlint-disable-next-line no-array-index-key
              key={index}
              type="button"
              aria-pressed={index === selected}
              className={`min-h-9 rounded-full px-3 text-sm font-medium ${
                index === selected
                  ? "bg-ink text-white"
                  : "bg-white text-gray-800 ring-1 ring-gray-300 ring-inset"
              }`}
              onClick={() => {
                setSelected(index);
                setElapsed(0);
              }}
            >
              {animation.title}
            </button>
          ))}
        </div>
      </div>
    </section>
  );
};

export default AnimationPlayer;
