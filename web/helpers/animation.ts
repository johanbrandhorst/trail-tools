import type {
  AnimationKeyframe,
  ExerciseAnimation,
  Pose,
} from "gen/plans/v1/exercises_pb";
import { AnimationPropKind, BodyPoint } from "gen/plans/v1/exercises_pb";

/*
 * The stick figure of exercise animations, seen from the side facing right.
 * Poses give the direction of each segment from the joint it starts at:
 * 0 degrees points down, 90 forward, 180 up and -90 back. The segments keep
 * their lengths, in the figure's units, so the figure keeps its
 * proportions.
 */
const TORSO = 52;
const HEAD = 17;
const THIGH = 44;
const SHIN = 42;
const UPPER_ARM = 30;
const FOREARM = 28;
const FOOT = 14;
// HEAD_RADIUS is the radius of the head, drawn as a circle.
export const HEAD_RADIUS = 9.5;

// Point is a point of the figure, from the left and the top.
export interface Point {
  left: number;
  top: number;
}

// Skeleton is the joints of the figure in a pose.
export interface Skeleton {
  head: Point;
  shoulders: Point;
  hips: Point;
  nearElbow: Point;
  nearHand: Point;
  farElbow: Point;
  farHand: Point;
  nearKnee: Point;
  nearFoot: Point;
  nearToe: Point;
  farKnee: Point;
  farFoot: Point;
  farToe: Point;
}

// Direction returns the unit vector of a segment's direction.
const direction = (degrees: number): Point => {
  const radians = (degrees * Math.PI) / 180;
  return { left: Math.sin(radians), top: Math.cos(radians) };
};

const along = (from: Point, degrees: number, length: number): Point => {
  const dir = direction(degrees);
  return {
    left: from.left + dir.left * length,
    top: from.top + dir.top * length,
  };
};

// SkeletonOf returns the joints of a pose, with the hips at the origin.
const skeletonOf = (pose: Pose): Skeleton => {
  const hips = { left: 0, top: 0 };
  const shoulders = along(hips, pose.torsoDegrees, TORSO);
  const nearElbow = along(shoulders, pose.nearUpperArmDegrees, UPPER_ARM);
  const farElbow = along(shoulders, pose.farUpperArmDegrees, UPPER_ARM);
  const nearKnee = along(hips, pose.nearThighDegrees, THIGH);
  const farKnee = along(hips, pose.farThighDegrees, THIGH);
  const nearFoot = along(nearKnee, pose.nearShinDegrees, SHIN);
  const farFoot = along(farKnee, pose.farShinDegrees, SHIN);
  return {
    farElbow,
    farFoot,
    farHand: along(farElbow, pose.farForearmDegrees, FOREARM),
    farKnee,
    farToe: { left: farFoot.left + FOOT, top: farFoot.top },
    head: along(shoulders, pose.headDegrees, HEAD),
    hips,
    nearElbow,
    nearFoot,
    nearHand: along(nearElbow, pose.nearForearmDegrees, FOREARM),
    nearKnee,
    nearToe: { left: nearFoot.left + FOOT, top: nearFoot.top },
    shoulders,
  };
};

// PointOf returns where a body point is in a skeleton.
const pointOf = (skeleton: Skeleton, point: BodyPoint): Point => {
  switch (point) {
    case BodyPoint.HEAD: {
      return skeleton.head;
    }
    case BodyPoint.SHOULDERS: {
      return skeleton.shoulders;
    }
    case BodyPoint.NEAR_ELBOW: {
      return skeleton.nearElbow;
    }
    case BodyPoint.NEAR_HAND: {
      return skeleton.nearHand;
    }
    case BodyPoint.FAR_ELBOW: {
      return skeleton.farElbow;
    }
    case BodyPoint.FAR_HAND: {
      return skeleton.farHand;
    }
    case BodyPoint.NEAR_KNEE: {
      return skeleton.nearKnee;
    }
    case BodyPoint.NEAR_FOOT: {
      return skeleton.nearFoot;
    }
    case BodyPoint.FAR_KNEE: {
      return skeleton.farKnee;
    }
    case BodyPoint.FAR_FOOT: {
      return skeleton.farFoot;
    }
    default: {
      return skeleton.hips;
    }
  }
};

const translate = (skeleton: Skeleton, dx: number, dy: number): Skeleton => {
  const move = (point: Point): Point => ({
    left: point.left + dx,
    top: point.top + dy,
  });
  return {
    farElbow: move(skeleton.farElbow),
    farFoot: move(skeleton.farFoot),
    farHand: move(skeleton.farHand),
    farKnee: move(skeleton.farKnee),
    farToe: move(skeleton.farToe),
    head: move(skeleton.head),
    hips: move(skeleton.hips),
    nearElbow: move(skeleton.nearElbow),
    nearFoot: move(skeleton.nearFoot),
    nearHand: move(skeleton.nearHand),
    nearKnee: move(skeleton.nearKnee),
    nearToe: move(skeleton.nearToe),
    shoulders: move(skeleton.shoulders),
  };
};

/**
 * Reach returns the middle joint of a two segment limb from root to a
 * target, bending the way the limb's current middle joint is: the knee or
 * elbow that keeps a planted foot or hand where it is. An unreachable
 * target straightens the limb towards it.
 */
const reach = (
  root: Point,
  target: Point,
  first: number,
  second: number,
  current: Point
): Point => {
  const dx = target.left - root.left;
  const dy = target.top - root.top;
  const distance = Math.min(
    Math.max(Math.hypot(dx, dy), Math.abs(first - second) + 0.01),
    first + second - 0.01
  );
  const toward = Math.atan2(dy, dx);
  const bend = Math.acos(
    (first * first + distance * distance - second * second) /
      (2 * first * distance)
  );
  const candidates = [toward + bend, toward - bend].map((angle) => ({
    left: root.left + first * Math.cos(angle),
    top: root.top + first * Math.sin(angle),
  }));
  const [one, two] = candidates;
  if (one === undefined || two === undefined) {
    return current;
  }
  const away = (point: Point) =>
    Math.hypot(point.left - current.left, point.top - current.top);
  return away(one) <= away(two) ? one : two;
};

// Plant keeps a planted hand or foot at its place, bending its limb.
const plant = (skeleton: Skeleton, point: BodyPoint, at: Point): Skeleton => {
  switch (point) {
    case BodyPoint.NEAR_FOOT: {
      const knee = reach(skeleton.hips, at, THIGH, SHIN, skeleton.nearKnee);
      return {
        ...skeleton,
        nearFoot: at,
        nearKnee: knee,
        nearToe: { left: at.left + FOOT, top: at.top },
      };
    }
    case BodyPoint.FAR_FOOT: {
      const knee = reach(skeleton.hips, at, THIGH, SHIN, skeleton.farKnee);
      return {
        ...skeleton,
        farFoot: at,
        farKnee: knee,
        farToe: { left: at.left + FOOT, top: at.top },
      };
    }
    case BodyPoint.NEAR_HAND: {
      const elbow = reach(
        skeleton.shoulders,
        at,
        UPPER_ARM,
        FOREARM,
        skeleton.nearElbow
      );
      return { ...skeleton, nearElbow: elbow, nearHand: at };
    }
    case BodyPoint.FAR_HAND: {
      const elbow = reach(
        skeleton.shoulders,
        at,
        UPPER_ARM,
        FOREARM,
        skeleton.farElbow
      );
      return { ...skeleton, farElbow: elbow, farHand: at };
    }
    default: {
      return skeleton;
    }
  }
};

// Shortest returns the change from one angle to another the short way round.
const shortest = (from: number, to: number): number =>
  ((((to - from) % 360) + 540) % 360) - 180;

const POSE_FIELDS = [
  "torsoDegrees",
  "headDegrees",
  "nearThighDegrees",
  "nearShinDegrees",
  "farThighDegrees",
  "farShinDegrees",
  "nearUpperArmDegrees",
  "nearForearmDegrees",
  "farUpperArmDegrees",
  "farForearmDegrees",
] as const;

// Blend returns the pose a fraction of the way from one pose to another.
const blend = (from: Pose, to: Pose, fraction: number): Pose => {
  const result = { ...from };
  for (const field of POSE_FIELDS) {
    result[field] = from[field] + shortest(from[field], to[field]) * fraction;
  }
  return result;
};

// Ease slows the start and end of a move, like a body does.
const ease = (fraction: number): number =>
  0.5 - Math.cos(Math.PI * fraction) / 2;

// Frame is the figure at a moment of an animation.
export interface Frame {
  skeleton: Skeleton;
  keyframe: AnimationKeyframe | undefined;
  /*
   * Whether the figure is holding the keyframe's pose, rather than moving
   * into it.
   */
  holding: boolean;
}

// Prop is a piece of equipment where it's drawn.
export interface PlacedProp {
  kind: AnimationPropKind;
  point: BodyPoint;
  // Where a support's top or a bar is, from the first pose.
  at: Point;
}

/*
 * Figure is an animation laid out: its first pose and floor, and the box
 * that contains it throughout.
 */
export interface Figure {
  animation: ExerciseAnimation;
  // The length of a loop in milliseconds.
  durationMs: number;
  floorY: number;
  props: PlacedProp[];
  viewBox: { left: number; top: number; width: number; height: number };
  frameAt: (ms: number) => Frame;
}

const KEY_POINTS = [
  BodyPoint.HEAD,
  BodyPoint.SHOULDERS,
  BodyPoint.HIPS,
  BodyPoint.NEAR_ELBOW,
  BodyPoint.NEAR_HAND,
  BodyPoint.FAR_ELBOW,
  BodyPoint.FAR_HAND,
  BodyPoint.NEAR_KNEE,
  BodyPoint.NEAR_FOOT,
  BodyPoint.FAR_KNEE,
  BodyPoint.FAR_FOOT,
];

const isSupport = (kind: AnimationPropKind): boolean =>
  kind === AnimationPropKind.BENCH || kind === AnimationPropKind.STEP;

/**
 * LayOut lays an animation out: the figure stays anchored where its anchor
 * is in the first pose, and planted hands and feet where they are in it.
 */
export const layOut = (animation: ExerciseAnimation): Figure | undefined => {
  const { keyframes } = animation;
  const first = keyframes[0]?.pose;
  if (first === undefined || keyframes.length < 2) {
    return undefined;
  }
  const start = skeletonOf(first);
  const anchorAt = pointOf(start, animation.anchor);
  const planted = animation.planted.map((point) => ({
    at: pointOf(start, point),
    point,
  }));

  const pose = (target: Pose): Skeleton => {
    const free = skeletonOf(target);
    const anchor = pointOf(free, animation.anchor);
    let skeleton = translate(
      free,
      anchorAt.left - anchor.left,
      anchorAt.top - anchor.top
    );
    for (const plantedPoint of planted) {
      skeleton = plant(skeleton, plantedPoint.point, plantedPoint.at);
    }
    return skeleton;
  };

  // Each keyframe is a move into its pose, then a hold of it.
  const spans = keyframes.map((keyframe) => ({
    hold: Math.max(keyframe.holdMilliseconds, 0),
    keyframe,
    move: Math.max(keyframe.moveMilliseconds, 0),
  }));
  const durationMs = Math.max(
    spans.reduce((total, span) => total + span.move + span.hold, 0),
    1
  );
  const frameAt = (ms: number): Frame => {
    let at = ((ms % durationMs) + durationMs) % durationMs;
    for (const [index, span] of spans.entries()) {
      const target = span.keyframe.pose ?? first;
      if (at < span.move) {
        const previous =
          spans[(index - 1 + spans.length) % spans.length]?.keyframe.pose ??
          first;
        return {
          holding: false,
          keyframe: span.keyframe,
          skeleton: pose(blend(previous, target, ease(at / span.move))),
        };
      }
      at -= span.move;
      if (at < span.hold) {
        return {
          holding: true,
          keyframe: span.keyframe,
          skeleton: pose(target),
        };
      }
      at -= span.hold;
    }
    return { holding: true, keyframe: keyframes[0], skeleton: pose(first) };
  };

  /*
   * The floor is under the lowest point of the first pose that no prop
   * supports.
   */
  const supported = new Set(
    animation.props
      .filter((prop) => isSupport(prop.kind))
      .map((prop) => prop.point)
  );
  const firstSkeleton = pose(first);
  const lowest = KEY_POINTS.filter((point) => !supported.has(point)).map(
    (point) =>
      pointOf(firstSkeleton, point).top +
      (point === BodyPoint.HEAD ? HEAD_RADIUS : 0)
  );
  const floorY =
    lowest.length > 0
      ? Math.max(...lowest) + 3
      : Math.max(
          ...KEY_POINTS.map((point) => pointOf(firstSkeleton, point).top)
        ) + 30;
  const props = animation.props.map((prop) => ({
    at: pointOf(firstSkeleton, prop.point),
    kind: prop.kind,
    point: prop.point,
  }));

  /*
   * The box around the figure through the loop, sampled at its keyframes
   * and between them.
   */
  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  for (let ms = 0; ms < durationMs; ms += durationMs / 48) {
    const { skeleton } = frameAt(ms);
    for (const point of KEY_POINTS.map((key) => pointOf(skeleton, key))) {
      minX = Math.min(minX, point.left);
      maxX = Math.max(maxX, point.left);
      minY = Math.min(minY, point.top);
    }
  }
  for (const prop of props) {
    if (isSupport(prop.kind)) {
      minX = Math.min(minX, prop.at.left - 36);
      maxX = Math.max(maxX, prop.at.left + 36);
    }
  }
  minY -= HEAD_RADIUS;
  // A 3:2 box with some room around the figure, the floor near the bottom.
  const pad = 18;
  let width = maxX - minX + 2 * pad;
  let height = floorY - minY + 2 * pad;
  if (width / height < 1.5) {
    width = height * 1.5;
  } else {
    height = width / 1.5;
  }
  const centerX = (minX + maxX) / 2;
  return {
    animation,
    durationMs,
    floorY,
    frameAt,
    props,
    viewBox: {
      height,
      left: centerX - width / 2,
      top: floorY + pad - height,
      width,
    },
  };
};

export { pointOf };
