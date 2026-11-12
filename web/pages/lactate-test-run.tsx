import { create } from "@bufbuild/protobuf";
import { PREPARATION, SamplingTips } from "@components/lactate/lactate-bits";
import {
  DEFAULT_END_HEART_RATE,
  DEFAULT_RANGE_BPM,
  DEFAULT_STAGE_MINUTES,
  DEFAULT_START_HEART_RATE,
  DEFAULT_STEP_BPM,
  formatMmol,
  nextHeartRate,
  pastThreshold,
  WARM_UP_MINUTES,
} from "@components/lactate/lactate-utils";
import Loading from "@components/loading";
import Card from "@components/ui/card";
import { TextField } from "@components/ui/fields";
import { PageHeader } from "@components/ui/misc";
import QueryError, { ErrorText } from "@components/ui/query-error";
import {
  ghostButton,
  inputClass,
  linkClass,
  primaryButton,
  secondaryButton,
} from "@components/ui/styles";
import type { ConnectError } from "@connectrpc/connect";
import {
  createConnectQueryKey,
  useMutation,
  useQuery,
  useTransport,
} from "@connectrpc/connect-query";
import { ArrowDownTrayIcon } from "@heroicons/react/24/outline";
import { skipToken, useQueryClient } from "@tanstack/react-query";
import {
  createLactateTest,
  getLactateTest,
  listLactateTests,
  updateLactateTest,
} from "gen/lactate/v1/lactate-LactateTestService_connectquery";
import type { LactateStage, LactateTest } from "gen/lactate/v1/lactate_pb";
import {
  GetLactateTestResponseSchema,
  LactateStageSchema,
  LactateTestService,
} from "gen/lactate/v1/lactate_pb";
import { today } from "helpers/dates";
import { useWakeLock } from "helpers/hooks";
import { invalidateServices } from "helpers/query";
import { readStorage, writeStorage } from "helpers/storage";
import React, { useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";

/**
 * SETTINGS_KEY stores the guided test's settings, so the next test is run the
 * same way.
 */
const SETTINGS_KEY = "trail-tools.lactate-test";

// Settings are how a guided test is run.
interface Settings {
  startHeartRate: number;
  stepBpm: number;
  stageMinutes: number;
  // The heart rate of the Garmin workout's last stage.
  endHeartRate: number;
  // How far the heart rate may stray from a stage's on the Garmin workout.
  rangeBpm: number;
}

const DEFAULT_SETTINGS: Settings = {
  endHeartRate: DEFAULT_END_HEART_RATE,
  rangeBpm: DEFAULT_RANGE_BPM,
  stageMinutes: DEFAULT_STAGE_MINUTES,
  startHeartRate: DEFAULT_START_HEART_RATE,
  stepBpm: DEFAULT_STEP_BPM,
};

// ReadSettings reads the settings of the last guided test, if there was one.
const readSettings = (): Settings | undefined => {
  const stored = readStorage(SETTINGS_KEY);
  if (stored === null) {
    return undefined;
  }
  try {
    const parsed: unknown = JSON.parse(stored);
    if (typeof parsed !== "object" || parsed === null) {
      return undefined;
    }
    const field = (key: keyof Settings): number => {
      const value: unknown = Reflect.get(parsed, key);
      return typeof value === "number" && value > 0
        ? value
        : DEFAULT_SETTINGS[key];
    };
    return {
      endHeartRate: field("endHeartRate"),
      rangeBpm: field("rangeBpm"),
      stageMinutes: field("stageMinutes"),
      startHeartRate: field("startHeartRate"),
      stepBpm: field("stepBpm"),
    };
  } catch {
    return undefined;
  }
};

// WorkoutURL returns the URL of the Garmin workout of a test's settings.
const workoutURL = (settings: Settings): string => {
  const query = new URLSearchParams({
    end: String(settings.endHeartRate),
    minutes: String(settings.stageMinutes),
    range: String(settings.rangeBpm),
    start: String(settings.startHeartRate),
    step: String(settings.stepBpm),
  });
  return `/lactate-tests/workout.fit?${query.toString()}`;
};

/**
 * Baseline is how a test gets its baseline: measured at rest before the
 * warm-up, a known value entered instead, or none.
 */
type Baseline =
  | { mode: "measure" }
  | { mode: "known"; mmolPerLiter: number }
  | { mode: "skip" };

interface PrepareProps {
  settings: Settings;
  // The user's latest test, to start where it did.
  previous: LactateTest | undefined;
  pending: boolean;
  onChange: (settings: Settings) => void;
  onStart: (baseline: Baseline) => void;
}

/**
 * THRESHOLD_MARGIN_STAGES is how many stages below the last aerobic
 * threshold a test can start, to approach it from below.
 */
const THRESHOLD_MARGIN_STAGES = 2;

/**
 * StartOptions returns the first stages to suggest from the previous test:
 * where it started, and a couple of stages below its aerobic threshold.
 */
const startOptions = (
  previous: LactateTest | undefined,
  stepBpm: number
): { label: string; heartRate: number }[] => {
  const options: { label: string; heartRate: number }[] = [];
  const start = previous?.stages[0]?.heartRateBpm;
  if (start !== undefined) {
    options.push({
      heartRate: start,
      label: `Where your last test started: ${start} bpm`,
    });
  }
  const threshold = previous?.aerobicThresholdHeartRateBpm ?? 0;
  if (threshold > 0) {
    // Rounded down to 5 bpm, a heart rate that's easy to hold.
    const below =
      Math.floor((threshold - THRESHOLD_MARGIN_STAGES * stepBpm) / 5) * 5;
    if (below !== start) {
      options.push({
        heartRate: below,
        label: `Below your last threshold of ${threshold} bpm: ${below} bpm`,
      });
    }
  }
  return options;
};

// Prepare goes through the checklist and the test's settings.
const Prepare: React.FC<PrepareProps> = ({
  settings,
  previous,
  pending,
  onChange,
  onStart,
}) => {
  // The last test's resting reading, or else its lowest.
  let previousBaseline = previous?.restingLactateMmolPerLiter ?? 0;
  if (previousBaseline === 0) {
    previousBaseline = previous?.lowestLactateMmolPerLiter ?? 0;
  }
  const [mode, setMode] = useState<Baseline["mode"]>("measure");
  const [known, setKnown] = useState(
    previousBaseline > 0 ? String(previousBaseline) : ""
  );
  const knownValue = Number(known.replace(",", "."));
  const field = (key: keyof Settings, label: string, hint: string) => (
    <TextField
      label={label}
      hint={hint}
      inputMode="numeric"
      value={String(settings[key])}
      onChange={(value) => {
        const parsed = Number(value);
        if (Number.isInteger(parsed) && parsed >= 0) {
          onChange({ ...settings, [key]: parsed });
        }
      }}
    />
  );
  return (
    <div className="flex flex-col gap-4">
      <Card title="Before you start">
        <ul className="list-disc space-y-1 pl-5 text-sm text-gray-700">
          {PREPARATION.map((item) => (
            <li key={item}>{item}</li>
          ))}
        </ul>
        <p className="mt-3 text-sm text-gray-700">
          Once the test starts, the screen shows only the heart rate to hold and
          a box for the reading, for whoever takes the samples:
        </p>
        <ol className="mt-1 list-decimal space-y-1 pl-5 text-sm text-gray-700">
          {mode === "measure" && (
            <li>Take a resting sample after a few minutes sitting still.</li>
          )}
          <li>
            Warm up for {WARM_UP_MINUTES} to 20 minutes, very easy and below{" "}
            {settings.startHeartRate} bpm, until you&apos;re lightly sweating.
          </li>
          <li>
            Hold each stage&apos;s heart rate for {settings.stageMinutes}{" "}
            minutes, then take a sample, keeping moving if you can.
          </li>
        </ol>
        <details className="mt-3">
          <summary className="cursor-pointer text-sm font-medium text-gray-700">
            How to take a sample
          </summary>
          <div className="pt-2">
            <SamplingTips />
          </div>
        </details>
      </Card>
      <Card title="Stages">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          {field(
            "startHeartRate",
            "First stage (bpm)",
            "Lower than you think you need."
          )}
          {field("stepBpm", "Step (bpm)", "How much higher each stage is.")}
          {field(
            "stageMinutes",
            "Stage length (minutes)",
            "At least 3, so lactate settles."
          )}
        </div>
        {startOptions(previous, settings.stepBpm).length > 0 && (
          <div className="mt-3 flex flex-wrap gap-2">
            {startOptions(previous, settings.stepBpm).map((option) => (
              <button
                key={option.label}
                type="button"
                className={secondaryButton}
                onClick={() => {
                  onChange({ ...settings, startHeartRate: option.heartRate });
                }}
              >
                {option.label}
              </button>
            ))}
          </div>
        )}
      </Card>
      <Card title="Baseline">
        <fieldset className="flex flex-col gap-2 text-sm text-gray-700">
          <legend className="sr-only">Baseline</legend>
          <label className="flex items-center gap-2">
            <input
              type="radio"
              name="baseline"
              checked={mode === "measure"}
              onChange={() => {
                setMode("measure");
              }}
            />
            Measure my resting lactate first
          </label>
          <label className="flex flex-wrap items-center gap-2">
            <input
              type="radio"
              name="baseline"
              checked={mode === "known"}
              onChange={() => {
                setMode("known");
              }}
            />
            I know my baseline:
            <input
              className={`${inputClass} w-24`}
              inputMode="decimal"
              aria-label="Baseline lactate (mmol/L)"
              value={known}
              onFocus={() => {
                setMode("known");
              }}
              onChange={(event) => {
                setKnown(event.target.value);
              }}
            />
            mmol/L
          </label>
          <label className="flex items-center gap-2">
            <input
              type="radio"
              name="baseline"
              checked={mode === "skip"}
              onChange={() => {
                setMode("skip");
              }}
            />
            Skip the baseline
          </label>
          <p className="text-xs text-gray-500">
            The baseline counts as the resting reading, for the lowest + 1
            mmol/L definition.
          </p>
        </fieldset>
      </Card>
      <Card title="Garmin workout">
        <div className="flex flex-col gap-3 text-sm text-gray-700">
          <p>
            Run the stages as a workout on your watch, which alerts you when
            your heart rate leaves each stage's range, and waits for you to
            press lap after each sample.
          </p>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            {field(
              "endHeartRate",
              "Last stage (bpm)",
              "Where the workout ends."
            )}
            {field(
              "rangeBpm",
              "Range (± bpm)",
              `e.g. ${settings.startHeartRate - settings.rangeBpm}–${settings.startHeartRate + settings.rangeBpm} for the first stage.`
            )}
          </div>
          <a
            href={workoutURL(settings)}
            download
            className={`${secondaryButton} self-start`}
          >
            <ArrowDownTrayIcon className="size-4" />
            Download the workout
          </a>
          <p className="text-xs text-gray-500">
            Copy the .fit file to the GARMIN/NewFiles folder of your watch over
            USB, and it shows up in your workouts.
          </p>
        </div>
      </Card>
      <button
        type="button"
        className={`${primaryButton} self-start`}
        disabled={
          pending ||
          settings.startHeartRate < 40 ||
          settings.stepBpm < 1 ||
          settings.stageMinutes < 1 ||
          (mode === "known" && !(knownValue > 0))
        }
        onClick={() => {
          if (mode === "known") {
            onStart({ mmolPerLiter: knownValue, mode });
          } else {
            onStart({ mode });
          }
        }}
      >
        Start the test
      </button>
    </div>
  );
};

interface EntryProps {
  // What to hold for the reading: a heart rate, or undefined at rest.
  targetBpm: number | undefined;
  // The last reading entered, to check it and undo it.
  last: { label: string; mmolPerLiter: number } | undefined;
  // Done reports whether the readings are past the aerobic threshold.
  done: boolean;
  pending: boolean;
  onSave: (mmolPerLiter: number) => void;
  onUndo: (() => void) | undefined;
  onFinish: (() => void) | undefined;
}

/**
 * Entry takes a test's readings one at a time, shown big enough to read at a
 * glance mid-run: the heart rate to hold now, and the lactate to enter for
 * it. Whoever records the test keeps the time, so there's no timer, and the
 * next stage follows as soon as a reading is saved.
 */
const Entry: React.FC<EntryProps> = ({
  targetBpm,
  last,
  done,
  pending,
  onSave,
  onUndo,
  onFinish,
}) => {
  useWakeLock(true);
  const [reading, setReading] = useState("");
  const value = Number(reading.replace(",", "."));
  return (
    <Card>
      <form
        className="flex flex-col items-center gap-6 py-4"
        onSubmit={(event) => {
          event.preventDefault();
          if (value > 0) {
            onSave(value);
          }
        }}
      >
        <div className="text-center">
          <p className="text-sm font-medium tracking-wide text-gray-500 uppercase">
            {targetBpm === undefined ? "Now" : "Current HR target"}
          </p>
          <p
            className={`font-bold text-gray-900 tabular-nums ${targetBpm === undefined ? "text-5xl sm:text-6xl" : "text-7xl sm:text-8xl"}`}
          >
            {targetBpm ?? "At rest"}
          </p>
          {targetBpm !== undefined && (
            <p className="text-lg font-medium text-gray-500">bpm</p>
          )}
        </div>
        <label className="flex w-full max-w-xs flex-col gap-2 text-center">
          <span className="text-sm font-medium text-gray-700">
            Lactate (mmol/L)
          </span>
          <input
            className={`${inputClass} py-3 text-center text-4xl font-semibold tabular-nums`}
            inputMode="decimal"
            enterKeyHint="done"
            autoFocus
            value={reading}
            onChange={(event) => {
              setReading(event.target.value);
            }}
          />
        </label>
        <button
          type="submit"
          className={`${primaryButton} w-full max-w-xs justify-center py-3 text-lg`}
          disabled={pending || !(value > 0)}
        >
          {pending ? "Saving…" : "Save"}
        </button>
        {(last !== undefined || onFinish !== undefined) && (
          <div className="flex flex-wrap items-center justify-center gap-x-4 gap-y-2 text-sm text-gray-600 tabular-nums">
            {last !== undefined && (
              <span>
                Last: {last.label}, {formatMmol(last.mmolPerLiter)}
              </span>
            )}
            {onUndo !== undefined && (
              <button
                type="button"
                className={ghostButton}
                disabled={pending}
                onClick={onUndo}
              >
                Undo
              </button>
            )}
            {onFinish !== undefined && (
              <button
                type="button"
                className={done ? primaryButton : secondaryButton}
                disabled={pending}
                onClick={onFinish}
              >
                {done ? "Past the threshold: finish" : "Finish the test"}
              </button>
            )}
          </div>
        )}
      </form>
    </Card>
  );
};

// Phase is the step of a guided test.
type Phase = "prepare" | "resting" | "stage";

export interface GuidedTestProps {
  // The test so far, or undefined until it's created.
  test: LactateTest | undefined;
  // The user's latest test, to start where it did, if there is one.
  previous: LactateTest | undefined;
  pending: boolean;
  error: ConnectError | null;
  // Create creates the test, with its resting reading, 0 if none.
  create: (restingMmolPerLiter: number) => Promise<void>;
  // SaveStages replaces the stages of the test.
  saveStages: (test: LactateTest, stages: LactateStage[]) => Promise<void>;
  onFinish: (test: LactateTest) => void;
}

/**
 * GuidedTest guides a lactate test: the checklist and settings, then the
 * readings, saving each as it's entered, so the test can be continued later.
 */
export const GuidedTest: React.FC<GuidedTestProps> = ({
  test: loaded,
  previous,
  pending,
  error,
  create: createTest,
  saveStages: save,
  onFinish,
}) => {
  // The settings the user chose, now or for the last guided test.
  const [chosen, setChosen] = useState(readSettings);
  /**
   * Without settings of their own, guided tests start where the last test
   * started, so tests are comparable.
   */
  const lastStart = previous?.stages[0]?.heartRateBpm;
  const settings = chosen ?? {
    ...DEFAULT_SETTINGS,
    startHeartRate: lastStart ?? DEFAULT_SETTINGS.startHeartRate,
  };
  // A continued test goes straight to its next stage.
  const [chosenPhase, setPhase] = useState<Phase | undefined>(
    loaded === undefined ? "prepare" : undefined
  );
  const phase = chosenPhase ?? "stage";

  let content: React.ReactNode = null;
  if (phase === "prepare") {
    content = (
      <Prepare
        settings={settings}
        previous={previous}
        pending={pending}
        onChange={setChosen}
        onStart={(baseline) => {
          writeStorage(SETTINGS_KEY, JSON.stringify(settings));
          // Once the test starts, its settings stay as they are.
          setChosen(settings);
          if (baseline.mode === "measure") {
            setPhase("resting");
            return;
          }
          createTest(baseline.mode === "known" ? baseline.mmolPerLiter : 0)
            .then(() => {
              setPhase("stage");
            })
            .catch(() => {
              // The error is shown below, and the test can be started again.
            });
        }}
      />
    );
  } else if (phase === "resting") {
    content = (
      <Entry
        targetBpm={undefined}
        last={undefined}
        done={false}
        pending={pending}
        onSave={(mmolPerLiter) => {
          createTest(mmolPerLiter)
            .then(() => {
              setPhase("stage");
            })
            .catch(() => {
              // The error is shown below, and the reading can be saved again.
            });
        }}
        onUndo={undefined}
        onFinish={undefined}
      />
    );
  } else if (loaded !== undefined) {
    const lastStage = loaded.stages.at(-1);
    let last: EntryProps["last"] = undefined;
    if (lastStage !== undefined) {
      last = {
        label: `${lastStage.heartRateBpm} bpm`,
        mmolPerLiter: lastStage.lactateMmolPerLiter,
      };
    } else if (loaded.restingLactateMmolPerLiter > 0) {
      last = {
        label: "at rest",
        mmolPerLiter: loaded.restingLactateMmolPerLiter,
      };
    }
    const targetBpm = nextHeartRate(
      loaded.stages,
      settings.startHeartRate,
      settings.stepBpm
    );
    const saveStages = (stages: LactateStage[]): void => {
      save(loaded, stages).catch(() => {
        // The error is shown below, and the reading can be saved again.
      });
    };
    content = (
      <Entry
        // A new stage starts with an empty input.
        key={loaded.stages.length}
        targetBpm={targetBpm}
        last={last}
        done={pastThreshold(loaded.restingLactateMmolPerLiter, loaded.stages)}
        pending={pending}
        onSave={(mmolPerLiter) => {
          saveStages([
            ...loaded.stages,
            create(LactateStageSchema, {
              heartRateBpm: targetBpm,
              lactateMmolPerLiter: mmolPerLiter,
            }),
          ]);
        }}
        onUndo={
          lastStage === undefined
            ? undefined
            : () => {
                saveStages(loaded.stages.slice(0, -1));
              }
        }
        onFinish={
          lastStage === undefined
            ? undefined
            : () => {
                onFinish(loaded);
              }
        }
      />
    );
  }

  return (
    <div className="flex flex-col gap-4">
      {content}
      <ErrorText error={error} />
    </div>
  );
};

// LactateTestRunPage runs one of the user's lactate tests, or a new one.
const LactateTestRunPage: React.FC = () => {
  const { id } = useParams();
  // The workout a new test is run in, from the workout's page.
  const [searchParams] = useSearchParams();
  const workoutId = searchParams.get("workout") ?? "";
  const queryClient = useQueryClient();
  const transport = useTransport();
  const navigate = useNavigate();
  const tests = useQuery(listLactateTests, {});
  const test = useQuery(getLactateTest, id === undefined ? skipToken : { id });

  const store = (updated: LactateTest | undefined): void => {
    if (updated === undefined) {
      return;
    }
    queryClient.setQueryData(
      createConnectQueryKey({
        cardinality: "finite",
        input: { id: updated.id },
        schema: getLactateTest,
        transport,
      }),
      create(GetLactateTestResponseSchema, { test: updated })
    );
  };
  const createTest = useMutation(createLactateTest, {
    onSuccess: async (response) => {
      store(response.test);
      await invalidateServices(queryClient, [LactateTestService]);
      if (response.test !== undefined) {
        void navigate(`/lactate-tests/${response.test.id}/run`, {
          replace: true,
        });
      }
    },
  });
  const update = useMutation(updateLactateTest, {
    onSuccess: async (response) => {
      store(response.test);
      await invalidateServices(queryClient, [LactateTestService]);
    },
  });

  if ((id !== undefined && test.isPending) || tests.isPending) {
    return <Loading />;
  }
  if (test.isError) {
    return (
      <QueryError
        error={test.error}
        onRetry={() => {
          void test.refetch();
        }}
      />
    );
  }

  return (
    <div className="mx-auto w-full max-w-2xl">
      <PageHeader
        title="Lactate test"
        subtitle={
          <Link to="/lactate-tests" className={linkClass}>
            All lactate tests
          </Link>
        }
      />
      <GuidedTest
        test={test.data?.test}
        // The latest other test with stages to start from.
        previous={tests.data?.tests.find(
          (other) => other.id !== test.data?.test?.id && other.stages.length > 0
        )}
        pending={createTest.isPending || update.isPending}
        error={createTest.error ?? update.error}
        create={async (mmolPerLiter) => {
          await createTest.mutateAsync({
            test: {
              date: today(),
              restingLactateMmolPerLiter: mmolPerLiter,
              workoutId,
            },
          });
        }}
        saveStages={async (current, stages) => {
          await update.mutateAsync({
            test: { id: current.id, stages },
            updateMask: { paths: ["stages"] },
          });
        }}
        onFinish={(finished) => {
          void navigate(`/lactate-tests/${finished.id}`);
        }}
      />
    </div>
  );
};

export default LactateTestRunPage;
