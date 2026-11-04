import Loading from "@components/loading";
import AnimationPlayer from "@components/strength/animation-player";
import BodyMap, { MuscleChips } from "@components/strength/body-map";
import Card from "@components/ui/card";
import { Badge } from "@components/ui/misc";
import QueryError, { ErrorText } from "@components/ui/query-error";
import { inputClass, labelClass } from "@components/ui/styles";
import type { ConnectError } from "@connectrpc/connect";
import { Code } from "@connectrpc/connect";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import {
  CheckIcon,
  ChevronLeftIcon,
  ChevronRightIcon,
  XMarkIcon,
} from "@heroicons/react/24/outline";
import { useQueryClient } from "@tanstack/react-query";
import { WeightUnit } from "gen/plans/v1/equipment_pb";
import { listExercises } from "gen/plans/v1/exercises-ExerciseService_connectquery";
import type { Exercise } from "gen/plans/v1/exercises_pb";
import {
  getWorkout,
  setStrengthLog,
} from "gen/plans/v1/plans-TrainingPlanService_connectquery";
import type {
  ExerciseGroup,
  PlannedExercise,
  StrengthWorkout,
} from "gen/plans/v1/plans_pb";
import {
  ExerciseEffort,
  ExerciseGroupKind,
  ExerciseVolume,
  SessionEffort,
  TrainingPlanService,
} from "gen/plans/v1/plans_pb";
import { invalidateServices } from "helpers/query";
import { readStorage, writeStorage } from "helpers/storage";
import {
  EXERCISE_EFFORTS,
  EXERCISE_VOLUMES,
  SESSION_EFFORTS,
  feedbackStyles,
  equipmentLabel,
  formatWeight,
  formatReps,
  isOpen,
  roundGroup,
  withRoundSets,
  formatSeconds,
  workoutMuscles,
} from "helpers/strength";
import React, { useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";

// DraftSet is a set of the workout being done.
interface DraftSet {
  done: boolean;
  reps: number;
  holdSeconds: number;
  // The weight in its unit; a value of 0 is bodyweight.
  weight: { value: number; unit: WeightUnit };
}

// DraftExercise is what the user did of a planned exercise so far.
interface DraftExercise {
  exerciseId: string;
  sets: DraftSet[];
  effort: ExerciseEffort;
  volume: ExerciseVolume;
  notes: string;
}

/**
 * Draft is the log of the workout being done, kept in the browser until it's
 * saved, so reloading the page or the phone locking doesn't lose it.
 */
interface Draft {
  exercises: DraftExercise[];
  effort: SessionEffort;
  notes: string;
}

/**
 * UseWakeLock keeps the screen on while the page is shown, so the phone
 * doesn't lock between sets. Browsers without the Screen Wake Lock API just
 * lock as usual.
 */
const useWakeLock = (): void => {
  useEffect(() => {
    let lock: WakeLockSentinel | undefined = undefined;
    let released = false;
    const acquire = async () => {
      if (
        document.visibilityState !== "visible" ||
        !("wakeLock" in navigator)
      ) {
        return;
      }
      try {
        lock = await navigator.wakeLock.request("screen");
        if (released) {
          await lock.release();
        }
      } catch {
        // Low battery modes refuse the lock; the screen locks as usual.
      }
    };
    /*
     * The lock is released when the page is hidden, so it's taken again
     * when it's shown.
     */
    const onVisibility = () => {
      void acquire();
    };
    void acquire();
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      released = true;
      document.removeEventListener("visibilitychange", onVisibility);
      void lock?.release();
    };
  }, []);
};

// IsNetworkError reports whether a request failed for want of a connection.
const isNetworkError = (error: ConnectError): boolean =>
  error.code === Code.Unavailable ||
  error.code === Code.Unknown ||
  !navigator.onLine;

// EFFORT_SCALE is the levels of effort, from very easy to maximal.
const EFFORT_SCALE = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10];

const draftKey = (workoutId: string): string =>
  `trail-tools.strength-draft.${workoutId}`;

// PlannedSets returns the sets of a planned exercise, none done yet.
const plannedSets = (planned: PlannedExercise): DraftSet[] =>
  Array.from({ length: planned.sets }, () => ({
    done: false,
    holdSeconds: planned.holdSeconds,
    reps: planned.reps,
    weight: {
      unit: planned.weight?.unit ?? WeightUnit.KILOGRAMS,
      value: planned.weight?.value ?? 0,
    },
  }));

// IsDraft checks that a draft read back from the browser has its shape.
const isDraft = (value: unknown): value is Draft =>
  typeof value === "object" &&
  value !== null &&
  "exercises" in value &&
  Array.isArray(value.exercises) &&
  value.exercises.every(
    (exercise: unknown) =>
      typeof exercise === "object" &&
      exercise !== null &&
      "exerciseId" in exercise &&
      "sets" in exercise &&
      Array.isArray(exercise.sets)
  );

/**
 * InitialDraft returns the draft to start from: the one kept in the browser
 * if it's of the same exercises, else the workout's log, if it has one, or
 * else the planned sets.
 */
const initialDraft = (workoutId: string, strength: StrengthWorkout): Draft => {
  const planned = strength.exercises;
  const stored = readStorage(draftKey(workoutId));
  if (stored !== null) {
    try {
      const draft: unknown = JSON.parse(stored);
      if (
        isDraft(draft) &&
        draft.exercises.length === planned.length &&
        draft.exercises.every(
          (exercise, index) =>
            exercise.exerciseId === planned[index]?.exerciseId
        )
      ) {
        return draft;
      }
    } catch {
      // A draft that doesn't parse is ignored.
    }
  }
  const { log } = strength;
  /*
   * The nth time an exercise is logged matches the nth time it's planned,
   * as the server matches them.
   */
  const used = new Set<number>();
  const exercises = planned.map((item) => {
    const index =
      log?.exercises.findIndex(
        (entry, position) =>
          !used.has(position) && entry.exerciseId === item.exerciseId
      ) ?? -1;
    const logged = index === -1 ? undefined : log?.exercises[index];
    if (logged === undefined) {
      return {
        effort: ExerciseEffort.UNSPECIFIED,
        exerciseId: item.exerciseId,
        notes: "",
        sets: plannedSets(item),
        volume: ExerciseVolume.UNSPECIFIED,
      };
    }
    used.add(index);
    const sets: DraftSet[] = logged.sets.map((set) => ({
      done: true,
      holdSeconds: set.holdSeconds,
      reps: set.reps,
      weight: {
        unit: set.weight?.unit ?? WeightUnit.KILOGRAMS,
        value: set.weight?.value ?? 0,
      },
    }));
    // Sets that weren't done are still there to do.
    sets.push(...plannedSets(item).slice(sets.length));
    return {
      effort: logged.effort,
      exerciseId: item.exerciseId,
      notes: logged.notes,
      sets,
      volume: logged.volume,
    };
  });
  return {
    effort: log?.effort ?? SessionEffort.UNSPECIFIED,
    exercises,
    notes: log?.notes ?? "",
  };
};

// Started reports whether any set of an exercise is done, or it's rated.
const started = (exercise: DraftExercise): boolean =>
  exercise.sets.some((set) => set.done) ||
  exercise.effort !== ExerciseEffort.UNSPECIFIED ||
  exercise.volume !== ExerciseVolume.UNSPECIFIED ||
  exercise.notes.trim() !== "";

// Finished reports whether all the sets of an exercise are done.
const finished = (exercise: DraftExercise): boolean =>
  exercise.sets.length > 0 && exercise.sets.every((set) => set.done);

/**
 * StrengthSessionPage is a strength workout to follow on the phone, one
 * exercise at a time: what to do, how it should feel and how to do it. The
 * user ticks off each set, rates each exercise, and finishes with a
 * debrief, which saves the log and completes the workout.
 */
const StrengthSessionPage: React.FC = () => {
  const { id = "" } = useParams();
  const workoutQuery = useQuery(getWorkout, { id });
  const exercisesQuery = useQuery(listExercises, {});

  if (workoutQuery.isPending || exercisesQuery.isPending) {
    return <Loading />;
  }
  const queryError = workoutQuery.error ?? exercisesQuery.error;
  if (queryError !== null) {
    return (
      <div className="p-4">
        <QueryError
          error={queryError}
          onRetry={() => {
            void workoutQuery.refetch();
            void exercisesQuery.refetch();
          }}
        />
      </div>
    );
  }
  const workout = workoutQuery.data?.workout;
  if (workout?.details.case !== "strength") {
    return (
      <div className="p-4 text-center text-gray-700">
        <p>Only strength workouts can be followed here.</p>
        <Link to={`/workouts/${id}`} className="text-brand-600 hover:underline">
          Back to the workout
        </Link>
      </div>
    );
  }
  const library = new Map(
    (exercisesQuery.data?.exercises ?? []).map((ex) => [ex.id, ex])
  );
  return (
    <Session
      key={workout.id}
      workoutId={workout.id}
      title={workout.title === "" ? "Strength" : workout.title}
      strength={withRoundSets(workout.details.value)}
      library={library}
    />
  );
};

interface SessionProps {
  workoutId: string;
  title: string;
  strength: StrengthWorkout;
  library: Map<string, Exercise>;
}

const Session: React.FC<SessionProps> = ({
  workoutId,
  title,
  strength,
  library,
}) => {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [searchParams, setSearchParams] = useSearchParams();
  const [draft, setDraft] = useState(() => initialDraft(workoutId, strength));
  const planned = strength.exercises;
  const finishStep = planned.length;
  const requested = Number(searchParams.get("step") ?? "0");
  const step =
    Number.isInteger(requested) && requested >= 0
      ? Math.min(requested, finishStep)
      : 0;

  useEffect(() => {
    writeStorage(draftKey(workoutId), JSON.stringify(draft));
  }, [workoutId, draft]);

  useWakeLock();
  const save = useMutation(setStrengthLog, {
    onSuccess: async () => {
      writeStorage(draftKey(workoutId), "");
      await invalidateServices(queryClient, [TrainingPlanService]);
      void navigate(`/workouts/${workoutId}`);
    },
  });
  /*
   * Without a connection, like in a basement gym, the save is retried once
   * the phone is back online; the draft is kept on the phone meanwhile.
   */
  const offline = save.isError && isNetworkError(save.error);
  const retry = useRef<() => void>(() => undefined);
  useEffect(() => {
    if (!offline) {
      return undefined;
    }
    const onOnline = () => {
      retry.current();
    };
    globalThis.addEventListener("online", onOnline);
    return () => {
      globalThis.removeEventListener("online", onOnline);
    };
  }, [offline]);

  const goTo = (next: number) => {
    setSearchParams({ step: String(next) });
    // Each step starts at its top, like a new page.
    globalThis.scrollTo({ top: 0 });
  };
  const updateExercise = (index: number, change: Partial<DraftExercise>) => {
    setDraft((previous) => ({
      ...previous,
      exercises: previous.exercises.map((exercise, position) =>
        position === index ? { ...exercise, ...change } : exercise
      ),
    }));
  };

  // Submit saves the debrief.
  const submit = (): void => {
    save.mutate({
      log: {
        effort: draft.effort,
        exercises: draft.exercises.filter(started).map((exercise) => ({
          effort: exercise.effort,
          exerciseId: exercise.exerciseId,
          notes: exercise.notes.trim(),
          sets: exercise.sets
            .filter((set) => set.done)
            .map((set) => ({
              holdSeconds: set.holdSeconds,
              reps: set.reps,
              weight: set.weight.value > 0 ? set.weight : undefined,
            })),
          volume: exercise.volume,
        })),
        notes: draft.notes.trim(),
      },
      workoutId,
    });
  };

  const current = planned[step];
  const currentDraft = draft.exercises[step];
  const next = nextStep(strength, draft, step);
  const block = roundBlockOf(strength, step);
  return (
    <div className="flex min-h-full flex-col bg-gray-100 font-ui text-gray-900">
      <SessionHeader
        workoutId={workoutId}
        title={title}
        step={step}
        exercises={draft.exercises}
        onStep={goTo}
      />
      <main className="mx-auto flex w-full max-w-lg flex-col gap-4 p-4">
        {current !== undefined && currentDraft !== undefined ? (
          <ExerciseStep
            planned={current}
            block={roundBlockOf(strength, step)}
            lastOfBlock={roundBlockOf(strength, step)?.last === step}
            exercise={library.get(current.exerciseId)}
            draft={currentDraft}
            onChange={(change) => {
              updateExercise(step, change);
            }}
          />
        ) : (
          <FinishStep
            planned={planned}
            draft={draft}
            onChange={(change) => {
              setDraft((previous) => ({ ...previous, ...change }));
            }}
            onStep={goTo}
          />
        )}
        {step === finishStep &&
          (offline ? (
            <p className="rounded-lg bg-amber-50 p-3 text-sm text-amber-900 ring-1 ring-amber-200 ring-inset">
              You're offline. The debrief is kept on this phone, and saved as
              soon as you're back online.
            </p>
          ) : (
            <ErrorText error={save.error} />
          ))}
        <nav className="flex gap-2 pt-1">
          {step > 0 && (
            <button
              type="button"
              className="flex h-13 items-center justify-center gap-1 rounded-lg bg-white px-4 font-medium text-gray-800 ring-1 ring-gray-300 ring-inset"
              onClick={() => {
                goTo(step - 1);
              }}
            >
              <ChevronLeftIcon className="size-5" />
              Back
            </button>
          )}
          {step < finishStep ? (
            <button
              type="button"
              className="flex h-13 min-w-0 grow items-center justify-center gap-1 rounded-lg bg-ink px-4 font-medium text-white"
              onClick={() => {
                goTo(next.step);
              }}
            >
              <span className="truncate">
                {next.step < finishStep
                  ? `Next: ${planned[next.step]?.exerciseName ?? ""}${next.round > 0 ? ` · round ${next.round}` : ""}`
                  : "Finish"}
              </span>
              <ChevronRightIcon className="size-5 shrink-0" />
            </button>
          ) : (
            <button
              type="button"
              className="flex h-13 grow items-center justify-center rounded-lg bg-ink px-4 font-medium text-white disabled:opacity-50"
              disabled={save.isPending}
              onClick={() => {
                retry.current = () => {
                  submit();
                };
                submit();
              }}
            >
              {save.isPending ? "Saving…" : "Save debrief"}
            </button>
          )}
        </nav>
        {block !== undefined && next.step === block.first && (
          <button
            type="button"
            className="self-center text-sm font-medium text-gray-600 hover:underline"
            onClick={() => {
              goTo(block.last + 1);
            }}
          >
            {`Done with the ${block.group.kind === ExerciseGroupKind.CIRCUIT ? "circuit" : "superset"}`}
          </button>
        )}
      </main>
    </div>
  );
};

interface SessionHeaderProps {
  workoutId: string;
  title: string;
  step: number;
  exercises: DraftExercise[];
  onStep: (step: number) => void;
}

// SessionHeader closes the workout, and shows how far through it the user is.
const SessionHeader: React.FC<SessionHeaderProps> = ({
  workoutId,
  title,
  step,
  exercises,
  onStep,
}) => (
  <header className="sticky top-0 z-10 border-b border-gray-200 bg-white px-2 pt-2 pb-2.5">
    <div className="mx-auto flex max-w-lg flex-col gap-2">
      <div className="flex items-center gap-1">
        <Link
          to={`/workouts/${workoutId}`}
          aria-label="Close the workout"
          title="Close"
          className="flex size-11 items-center justify-center rounded-md text-gray-600 hover:bg-gray-100"
        >
          <XMarkIcon className="size-6" />
        </Link>
        <span className="min-w-0 grow truncate text-sm font-medium">
          {title}
        </span>
        <span className="pr-2 text-sm text-gray-600 tabular-nums">
          {step < exercises.length
            ? `${step + 1} of ${exercises.length}`
            : "Debrief"}
        </span>
      </div>
      <div className="flex gap-1 px-2">
        {exercises.map((exercise, index) => {
          let color = "bg-gray-200";
          if (index === step) {
            color = "bg-violet-300";
          } else if (finished(exercise)) {
            color = "bg-violet-600";
          }
          return (
            <button
              // Exercises keep their order during the workout.
              // oxlint-disable-next-line no-array-index-key
              key={index}
              type="button"
              aria-label={`Go to exercise ${index + 1}`}
              title={`Exercise ${index + 1}`}
              className="flex h-4 grow items-center"
              onClick={() => {
                onStep(index);
              }}
            >
              <span className={`h-1 w-full rounded-full ${color}`} />
            </button>
          );
        })}
      </div>
    </div>
  </header>
);

interface ExerciseStepProps {
  planned: PlannedExercise;
  // The superset or circuit the exercise is in, if any.
  block: RoundBlock | undefined;
  // Whether the exercise is the last of its superset or circuit.
  lastOfBlock: boolean;
  // The exercise in the library, undefined if it's missing.
  exercise: Exercise | undefined;
  draft: DraftExercise;
  onChange: (change: Partial<DraftExercise>) => void;
}

/**
 * RoundBlock is the exercises of a superset or circuit, done one set of each
 * in order, then again for its rounds.
 */
interface RoundBlock {
  group: ExerciseGroup;
  // The indices of its first and last exercises.
  first: number;
  last: number;
}

// RoundBlockOf returns the superset or circuit an exercise is in, if any.
const roundBlockOf = (
  strength: StrengthWorkout,
  index: number
): RoundBlock | undefined => {
  const name = strength.exercises[index]?.group ?? "";
  const group = roundGroup(strength.groups, name);
  if (group === undefined) {
    return undefined;
  }
  let first = index;
  while (strength.exercises[first - 1]?.group === name) {
    first -= 1;
  }
  let last = index;
  while (strength.exercises[last + 1]?.group === name) {
    last += 1;
  }
  return { first, group, last };
};

// DoneSets counts the sets of an exercise ticked off.
const doneSets = (exercise: DraftExercise | undefined): number =>
  exercise?.sets.filter((set) => set.done).length ?? 0;

/**
 * NextStep is the step after one: in a superset or circuit, the next
 * exercise of the round, and after its last exercise, its first again until
 * all the rounds are done.
 */
const nextStep = (
  strength: StrengthWorkout,
  draft: Draft,
  step: number
): { step: number; round: number } => {
  const block = roundBlockOf(strength, step);
  if (block === undefined || step < block.last) {
    const next = roundBlockOf(strength, step + 1);
    return {
      round:
        next === undefined
          ? 0
          : Math.min(
              doneSets(draft.exercises[step + 1]) + 1,
              next.group.rounds ?? 1
            ),
      step: step + 1,
    };
  }
  const rounds = block.group.rounds ?? 1;
  const done = doneSets(draft.exercises[block.last]);
  if (done < rounds) {
    return { round: done + 1, step: block.first };
  }
  return { round: 0, step: block.last + 1 };
};

/**
 * TapHint says to tick off a set when it's done, and what follows it: the
 * rest, or in a superset or circuit, the next exercise.
 */
const tapHint = (
  planned: PlannedExercise,
  block: RoundBlock | undefined,
  lastOfBlock: boolean,
  round: number
): string => {
  if (
    block !== undefined &&
    lastOfBlock &&
    round >= (block.group.rounds ?? 1)
  ) {
    return "Tap this round when you finish it: it's the last.";
  }
  if (block === undefined) {
    return `Tap a set when you finish it${planned.restSeconds > 0 ? `, then rest ${formatSeconds(planned.restSeconds)}` : ""}.`;
  }
  const rest = lastOfBlock
    ? (block.group.restSeconds ?? 0)
    : planned.restSeconds;
  const then = lastOfBlock
    ? "start the next round"
    : "go on to the next exercise";
  return `Tap this round when you finish it, then ${rest > 0 ? `rest ${formatSeconds(rest)} and ` : ""}${then}.`;
};

// SetsLabel labels what a planned exercise's sets are of.
const setsLabel = (planned: PlannedExercise): string => {
  if (planned.holdSeconds > 0) {
    return "Sets × time";
  }
  return isOpen(planned) ? "Sets" : "Sets × reps";
};

/**
 * ExerciseStep is one exercise of the workout: what to do, the sets to tick
 * off, how it should feel and how to do it.
 */
const ExerciseStep: React.FC<ExerciseStepProps> = ({
  planned,
  block,
  lastOfBlock,
  exercise,
  draft,
  onChange,
}) => {
  const [adjusting, setAdjusting] = useState(false);
  const eachSide = exercise?.eachSide ?? false;
  const muscles = useMemo(
    () => workoutMuscles(exercise === undefined ? [] : [exercise]),
    [exercise]
  );
  const allDone = finished(draft);
  const reps = formatReps(planned.reps, planned.holdSeconds, false);
  const stats: { label: string; value: string; sub: string }[] = [
    block === undefined
      ? {
          label: setsLabel(planned),
          sub: eachSide ? "each side" : "",
          value: `${planned.sets} × ${reps}`,
        }
      : {
          label: planned.holdSeconds > 0 ? "Time" : "Reps",
          sub: `${eachSide ? "each side, " : ""}once each round`,
          value: isOpen(planned) ? "Until done" : reps,
        },
  ];
  if (planned.restSeconds > 0) {
    stats.push({
      label: "Rest",
      sub: block === undefined ? "between sets" : "before the next exercise",
      value: formatSeconds(planned.restSeconds),
    });
  }
  const roundRest = block?.group.restSeconds ?? 0;
  if (block !== undefined && lastOfBlock && roundRest > 0) {
    stats.push({
      label: "Rest",
      sub: "after each round",
      value: formatSeconds(roundRest),
    });
  }
  const round =
    block === undefined
      ? 0
      : Math.min(doneSets(draft) + 1, block.group.rounds ?? 1);
  stats.push({
    label: "Weight",
    sub:
      planned.weight === undefined || planned.weight.value === 0 ? "" : "each",
    value: formatWeight(planned.weight),
  });
  if (planned.tempo !== "") {
    stats.push({ label: "Tempo", sub: "", value: planned.tempo });
  }
  const setSet = (index: number, change: Partial<DraftSet>) => {
    onChange({
      sets: draft.sets.map((set, position) =>
        position === index ? { ...set, ...change } : set
      ),
    });
  };

  return (
    <>
      <div className="flex flex-col gap-1">
        {planned.group !== "" && (
          <span className="text-xs font-semibold tracking-wide text-violet-700 uppercase">
            {planned.group}
            {block !== undefined &&
              (allDone
                ? " · all rounds done"
                : ` · round ${round} of ${block.group.rounds ?? 1}`)}
          </span>
        )}
        <h1 className="text-[1.75rem] leading-tight font-semibold text-balance">
          {planned.exerciseName}
        </h1>
      </div>

      {exercise !== undefined && exercise.animations.length > 0 && (
        <AnimationPlayer animations={exercise.animations} />
      )}

      <dl className="grid grid-cols-2 gap-2">
        {stats.map((stat) => (
          <div
            key={stat.label}
            className="flex flex-col gap-0.5 rounded-lg border border-gray-200 bg-white p-3"
          >
            <dt className="text-xs text-gray-500">{stat.label}</dt>
            <dd className="text-[1.375rem] font-semibold tabular-nums">
              {stat.value}
            </dd>
            {stat.sub !== "" && (
              <dd className="text-xs text-gray-600">{stat.sub}</dd>
            )}
          </div>
        ))}
      </dl>
      {exercise !== undefined && exercise.equipment.length > 0 && (
        <p className="-mt-2 text-sm text-pretty text-gray-600">
          <span className="font-medium text-gray-800">Equipment:</span>{" "}
          {[
            exercise.equipment.map(equipmentLabel).join(", "),
            exercise.equipmentNotes,
          ]
            .filter((part) => part !== "")
            .join(" · ")}
        </p>
      )}

      <section className="flex flex-col gap-2">
        <h2 className="text-[0.9375rem] font-semibold">
          {block === undefined ? "Sets" : "Rounds"}
        </h2>
        <div className="grid grid-cols-3 gap-2">
          {draft.sets.map((set, index) => (
            <button
              // Sets keep their order.
              // oxlint-disable-next-line no-array-index-key
              key={index}
              type="button"
              aria-pressed={set.done}
              className={`flex h-16 flex-col items-center justify-center gap-0.5 rounded-lg text-[0.9375rem] font-medium ${
                set.done
                  ? "bg-violet-100 text-violet-900 ring-2 ring-violet-600 ring-inset"
                  : "bg-white text-gray-800 ring-1 ring-gray-300 ring-inset"
              }`}
              onClick={() => {
                setSet(index, { done: !set.done });
              }}
            >
              <span className="flex items-center gap-1">
                {set.done && <CheckIcon className="size-4" />}
                {block === undefined ? "Set" : "Round"} {index + 1}
              </span>
              <span className="text-xs font-normal tabular-nums">
                {formatReps(set.reps, set.holdSeconds, false)}
                {set.weight.value > 0 && ` · ${formatWeight(set.weight)}`}
              </span>
            </button>
          ))}
        </div>
        <div className="flex items-baseline justify-between gap-2">
          <p className="text-sm text-gray-600">
            {allDone
              ? `All ${block === undefined ? "sets" : "rounds"} done.`
              : tapHint(planned, block, lastOfBlock, round)}
          </p>
          <button
            type="button"
            className="shrink-0 text-sm font-medium text-brand-600"
            onClick={() => {
              setAdjusting((open) => !open);
            }}
          >
            {adjusting ? "Done adjusting" : "Adjust sets"}
          </button>
        </div>
        {adjusting && (
          <AdjustSets
            sets={draft.sets}
            byTime={planned.holdSeconds > 0}
            onChange={setSet}
          />
        )}
      </section>

      {(allDone || started(draft)) && (
        <Debrief draft={draft} onChange={onChange} />
      )}

      <Card title="How it should feel">
        <div className="flex flex-col gap-2.5 text-sm leading-relaxed text-gray-700">
          {planned.targetEffort > 0 && (
            <div className="flex items-center gap-2.5">
              <div
                className="grid grow grid-cols-10 gap-0.5"
                aria-hidden="true"
              >
                {EFFORT_SCALE.map((level) => (
                  <span
                    key={level}
                    className={`h-2 rounded-sm ${level <= planned.targetEffort ? "bg-ember" : "bg-gray-200"}`}
                  />
                ))}
              </div>
              <span className="text-sm font-semibold whitespace-nowrap text-gray-900">
                Effort {planned.targetEffort} of 10
              </span>
            </div>
          )}
          {exercise !== undefined && exercise.feel !== "" && (
            <p className="whitespace-pre-line">{exercise.feel}</p>
          )}
          {planned.notes !== "" && (
            <p className="whitespace-pre-line">{planned.notes}</p>
          )}
          {planned.targetEffort === 0 &&
            (exercise?.feel ?? "") === "" &&
            planned.notes === "" && (
              <p className="text-gray-500">No notes on how it should feel.</p>
            )}
        </div>
      </Card>

      {exercise !== undefined &&
        muscles.primary.length + muscles.secondary.length > 0 && (
          <Card title="Muscles">
            <div className="flex flex-col gap-3">
              <BodyMap muscles={muscles} />
              <MuscleChips muscles={muscles} />
            </div>
          </Card>
        )}

      {exercise !== undefined && exercise.instructions.length > 0 && (
        <Card title="How to do it">
          <ol className="flex list-decimal flex-col gap-2 pl-5 text-sm leading-relaxed text-gray-700">
            {exercise.instructions.map((step) => (
              <li key={step}>{step}</li>
            ))}
          </ol>
        </Card>
      )}
    </>
  );
};

interface AdjustSetsProps {
  sets: DraftSet[];
  // Whether the sets are held for a time rather than done for reps.
  byTime: boolean;
  onChange: (index: number, change: Partial<DraftSet>) => void;
}

/**
 * AdjustSets edits the reps or time and weight of each set, for sets that
 * didn't go as planned.
 */
const AdjustSets: React.FC<AdjustSetsProps> = ({ sets, byTime, onChange }) => (
  <div className="flex flex-col gap-2 rounded-lg border border-gray-200 bg-white p-3">
    {sets.map((set, index) => (
      <div
        // Sets keep their order.
        // oxlint-disable-next-line no-array-index-key
        key={index}
        className="grid grid-cols-[3.5rem_1fr_1fr] items-end gap-2"
      >
        <span className="pb-2 text-sm font-medium">Set {index + 1}</span>
        <label className={labelClass}>
          {byTime ? "Seconds" : "Reps"}
          <span className="mt-1 flex">
            <NumberInput
              value={byTime ? set.holdSeconds : set.reps}
              onChange={(value) => {
                onChange(
                  index,
                  byTime ? { holdSeconds: value } : { reps: value }
                );
              }}
            />
          </span>
        </label>
        <label className={labelClass}>
          Weight ({set.weight.unit === WeightUnit.POUNDS ? "lb" : "kg"})
          <span className="mt-1 flex gap-1">
            <NumberInput
              value={set.weight.value}
              decimal
              onChange={(value) => {
                onChange(index, { weight: { ...set.weight, value } });
              }}
            />
            <button
              type="button"
              aria-label="Switch between kilograms and pounds"
              title="kg or lb"
              className="shrink-0 rounded-md px-2 text-sm font-medium text-gray-700 ring-1 ring-gray-300 ring-inset"
              onClick={() => {
                onChange(index, {
                  weight: {
                    ...set.weight,
                    unit:
                      set.weight.unit === WeightUnit.POUNDS
                        ? WeightUnit.KILOGRAMS
                        : WeightUnit.POUNDS,
                  },
                });
              }}
            >
              {set.weight.unit === WeightUnit.POUNDS ? "lb" : "kg"}
            </button>
          </span>
        </label>
      </div>
    ))}
  </div>
);

interface NumberInputProps {
  value: number;
  decimal?: boolean;
  onChange: (value: number) => void;
}

/**
 * NumberInput edits a non-negative number, keeping what's typed until it's a
 * number.
 */
const NumberInput: React.FC<NumberInputProps> = ({
  value,
  decimal = false,
  onChange,
}) => {
  const [text, setText] = useState(String(value));
  return (
    <input
      className={`${inputClass} text-base tabular-nums`}
      inputMode={decimal ? "decimal" : "numeric"}
      value={text}
      onChange={(event) => {
        setText(event.target.value);
        const parsed = Number(event.target.value.replace(",", "."));
        if (
          event.target.value.trim() !== "" &&
          Number.isFinite(parsed) &&
          parsed >= 0
        ) {
          onChange(decimal ? parsed : Math.round(parsed));
        }
      }}
    />
  );
};

interface ChipsProps<Value> {
  options: { value: Value; label: string }[];
  value: Value;
  // The value of no choice, chosen by tapping the chosen chip again.
  none: Value;
  onChange: (value: Value) => void;
  // Tailwind classes laying the chips out, e.g. "grid grid-cols-3".
  layout: string;
}

// Chips picks one of a few options, or none.
const Chips = <Value,>({
  options,
  value,
  none,
  onChange,
  layout,
}: ChipsProps<Value>) => (
  <div className={`gap-1.5 ${layout}`}>
    {options.map((option) => {
      const selected = option.value === value;
      return (
        <button
          key={option.label}
          type="button"
          aria-pressed={selected}
          className={`min-h-11 rounded-full px-3.5 text-sm font-medium whitespace-nowrap ${
            selected
              ? "bg-ink text-white"
              : "bg-white text-gray-800 ring-1 ring-gray-300 ring-inset"
          }`}
          onClick={() => {
            onChange(selected ? none : option.value);
          }}
        >
          {option.label}
        </button>
      );
    })}
  </div>
);

interface DebriefProps {
  draft: DraftExercise;
  onChange: (change: Partial<DraftExercise>) => void;
}

// Debrief asks how an exercise went once its sets are done.
const Debrief: React.FC<DebriefProps> = ({ draft, onChange }) => (
  <section className="flex flex-col gap-3.5 rounded-lg border-2 border-ink bg-white p-4">
    <h2 className="text-base font-semibold">How was it?</h2>
    <div className="flex flex-col gap-1.5">
      <span className="text-sm text-gray-600">Effort</span>
      <Chips
        layout="grid grid-cols-3"
        options={EXERCISE_EFFORTS.map((entry) => ({
          label: entry.style.label,
          value: entry.effort,
        }))}
        value={draft.effort}
        none={ExerciseEffort.UNSPECIFIED}
        onChange={(effort) => {
          onChange({ effort });
        }}
      />
    </div>
    <div className="flex flex-col gap-1.5">
      <span className="text-sm text-gray-600">Reps</span>
      <Chips
        layout="grid grid-cols-3"
        options={EXERCISE_VOLUMES.map((entry) => ({
          label: entry.style.label
            .replace(" reps", "")
            .replace("Reps about", "About"),
          value: entry.volume,
        }))}
        value={draft.volume}
        none={ExerciseVolume.UNSPECIFIED}
        onChange={(volume) => {
          onChange({ volume });
        }}
      />
    </div>
    <label className="flex flex-col gap-1.5 text-sm text-gray-600">
      A note, if anything stood out
      <input
        className={`${inputClass} text-base`}
        value={draft.notes}
        placeholder="E.g. left knee felt the last set"
        onChange={(event) => {
          onChange({ notes: event.target.value });
        }}
      />
    </label>
  </section>
);

interface FinishStepProps {
  planned: PlannedExercise[];
  draft: Draft;
  onChange: (change: Partial<Draft>) => void;
  onStep: (step: number) => void;
}

// FinishStep is the debrief of the whole workout, before saving it.
const FinishStep: React.FC<FinishStepProps> = ({
  planned,
  draft,
  onChange,
  onStep,
}) => {
  const done = draft.exercises.filter(started).length;
  return (
    <>
      <div className="flex flex-col items-center gap-2 pt-3 text-center">
        <span className="flex size-14 items-center justify-center rounded-full bg-violet-100 text-violet-700">
          <CheckIcon className="size-8" />
        </span>
        <h1 className="text-2xl font-semibold">Workout done</h1>
        <p className="text-sm text-gray-600">
          {done} of {planned.length} exercises
        </p>
      </div>
      <Card title="How hard was the session?">
        <Chips
          layout="flex flex-wrap"
          options={SESSION_EFFORTS.map((entry) => ({
            label: entry.label,
            value: entry.effort,
          }))}
          value={draft.effort}
          none={SessionEffort.UNSPECIFIED}
          onChange={(effort) => {
            onChange({ effort });
          }}
        />
      </Card>
      <Card title="Exercises">
        <ul className="-m-4 divide-y divide-gray-100">
          {planned.map((item, index) => {
            const exercise = draft.exercises[index];
            const styles =
              exercise === undefined
                ? []
                : feedbackStyles(exercise.effort, exercise.volume);
            const sets = exercise?.sets.filter((set) => set.done).length ?? 0;
            return (
              // Exercises keep their order during the workout.
              // oxlint-disable-next-line no-array-index-key
              <li key={index}>
                <button
                  type="button"
                  className="flex min-h-11 w-full flex-col items-start gap-1.5 px-4 py-2.5 text-left hover:bg-gray-50"
                  onClick={() => {
                    onStep(index);
                  }}
                >
                  <span className="flex w-full items-baseline justify-between gap-3">
                    <span className="text-sm font-medium">
                      {item.exerciseName}
                    </span>
                    <span className="shrink-0 text-xs text-gray-600 tabular-nums">
                      {sets} of {item.sets} sets
                    </span>
                  </span>
                  <span className="flex flex-wrap gap-1">
                    {styles.length === 0 ? (
                      <Badge>Not rated</Badge>
                    ) : (
                      styles.map((style) => (
                        <Badge key={style.label} className={style.badgeClass}>
                          {style.label}
                        </Badge>
                      ))
                    )}
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
      </Card>
      <label className="flex flex-col gap-1.5 text-base font-semibold">
        Notes
        <textarea
          rows={3}
          className={`${inputClass} text-base font-normal`}
          value={draft.notes}
          placeholder="How did the session go? Anything that hurt?"
          onChange={(event) => {
            onChange({ notes: event.target.value });
          }}
        />
      </label>
      <p className="text-center text-sm text-gray-600">
        Saving completes the workout, and the next planning session can adjust
        the weights and reps from it.
      </p>
    </>
  );
};

export default StrengthSessionPage;
