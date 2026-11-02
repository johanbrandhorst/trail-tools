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
import { invalidateServices } from "helpers/query";
import { readStorage, writeStorage } from "helpers/storage";
import React, { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";

/**
 * SETTINGS_KEY stores the guided test's settings, so the next test is run the
 * same way.
 */
const SETTINGS_KEY = "trail-tools.lactate-test";

/**
 * BIG_CHANGE_MMOL is how much a reading may change from the last stage's
 * before it's worth measuring again.
 */
const BIG_CHANGE_MMOL = 1.5;

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
 * UseWakeLock keeps the screen on while active, so a phone doesn't lock
 * during a stage. Browsers without the Screen Wake Lock API are left alone.
 */
const useWakeLock = (active: boolean): void => {
  useEffect(() => {
    if (!active || !("wakeLock" in navigator)) {
      return undefined;
    }
    let lock: WakeLockSentinel | undefined = undefined;
    let released = false;
    navigator.wakeLock
      .request("screen")
      .then((sentinel) => {
        if (released) {
          void sentinel.release();
        } else {
          lock = sentinel;
        }
      })
      .catch(() => {
        // The screen may turn off; the test still works.
      });
    return () => {
      released = true;
      void lock?.release();
    };
  }, [active]);
};

// FormatClock formats seconds as m:ss.
const formatClock = (seconds: number): string => {
  const whole = Math.max(0, Math.ceil(seconds));
  return `${Math.floor(whole / 60)}:${String(whole % 60).padStart(2, "0")}`;
};

interface TimerProps {
  minutes: number;
}

/**
 * Timer counts down a stage, keeping the screen on while it runs, and
 * vibrates when the time is up.
 */
const Timer: React.FC<TimerProps> = ({ minutes }) => {
  const [start, setStart] = useState<number | undefined>(undefined);
  const [now, setNow] = useState(() => Date.now());
  const remaining =
    start === undefined ? minutes * 60 : minutes * 60 - (now - start) / 1000;
  const running = start !== undefined && remaining > 0;
  useWakeLock(running);
  useEffect(() => {
    if (start === undefined) {
      return undefined;
    }
    const interval = globalThis.setInterval(() => {
      setNow(Date.now());
    }, 250);
    return () => {
      globalThis.clearInterval(interval);
    };
  }, [start]);
  const done = start !== undefined && remaining <= 0;
  useEffect(() => {
    if (done && "vibrate" in navigator) {
      navigator.vibrate([300, 150, 300]);
    }
  }, [done]);
  return (
    <div className="flex items-center gap-4">
      <span
        className={`text-4xl font-semibold tabular-nums ${done ? "text-ember" : "text-gray-900"}`}
        role="timer"
      >
        {done ? "0:00" : formatClock(remaining)}
      </span>
      {start === undefined ? (
        <button
          type="button"
          className={secondaryButton}
          onClick={() => {
            setNow(Date.now());
            setStart(Date.now());
          }}
        >
          Start the timer
        </button>
      ) : (
        <button
          type="button"
          className={ghostButton}
          onClick={() => {
            setStart(undefined);
          }}
        >
          Reset
        </button>
      )}
      {done && <span className="text-sm font-medium">Take a sample now.</span>}
    </div>
  );
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
        {mode === "measure"
          ? "Measure your resting lactate"
          : "Start the warm-up"}
      </button>
    </div>
  );
};

interface RestingProps {
  pending: boolean;
  onSave: (mmolPerLiter: number) => void;
}

// Resting measures the resting lactate, the test's baseline.
const Resting: React.FC<RestingProps> = ({ pending, onSave }) => {
  const [reading, setReading] = useState("");
  const value = Number(reading.replace(",", "."));
  return (
    <Card title="Resting lactate">
      <form
        className="flex flex-col gap-4"
        onSubmit={(event) => {
          event.preventDefault();
          if (value > 0) {
            onSave(value);
          }
        }}
      >
        <p className="text-sm text-gray-700">
          Sit still for a few minutes, then take a sample. It's your baseline,
          before any exercise.
        </p>
        <SamplingTips />
        <TextField
          label="Resting lactate (mmol/L)"
          value={reading}
          onChange={setReading}
          inputMode="decimal"
          autoFocus
        />
        <div className="flex flex-wrap gap-2">
          <button
            type="submit"
            className={primaryButton}
            disabled={pending || !(value > 0)}
          >
            Save and warm up
          </button>
          <button
            type="button"
            className={secondaryButton}
            disabled={pending}
            onClick={() => {
              onSave(0);
            }}
          >
            Skip
          </button>
        </div>
      </form>
    </Card>
  );
};

interface StageProps {
  test: LactateTest;
  settings: Settings;
  pending: boolean;
  onSave: (stages: LactateStage[]) => void;
}

/**
 * Stage runs the test's next stage: holding a heart rate for the stage's
 * length, then entering the reading.
 */
const Stage: React.FC<StageProps> = ({ test, settings, pending, onSave }) => {
  const target = nextHeartRate(
    test.stages,
    settings.startHeartRate,
    settings.stepBpm
  );
  const [heartRate, setHeartRate] = useState(String(target));
  const [reading, setReading] = useState("");
  const heartRateValue = Number(heartRate);
  const value = Number(reading.replace(",", "."));
  const valid =
    Number.isInteger(heartRateValue) && heartRateValue > 0 && value > 0;
  return (
    <Card title={`Stage ${test.stages.length + 1} · ${target} bpm`}>
      <form
        className="flex flex-col gap-4"
        onSubmit={(event) => {
          event.preventDefault();
          if (valid) {
            onSave([
              ...test.stages,
              create(LactateStageSchema, {
                heartRateBpm: heartRateValue,
                lactateMmolPerLiter: value,
              }),
            ]);
          }
        }}
      >
        <p className="text-sm text-gray-700">
          Bring your heart rate to {target} bpm, and hold it there for{" "}
          {settings.stageMinutes} minutes. Then take a sample, keeping moving if
          you can.
        </p>
        <Timer minutes={settings.stageMinutes} />
        <div className="grid grid-cols-2 gap-3">
          <TextField
            label="Heart rate held (bpm)"
            value={heartRate}
            onChange={setHeartRate}
            inputMode="numeric"
          />
          <TextField
            label="Lactate (mmol/L)"
            value={reading}
            onChange={setReading}
            inputMode="decimal"
          />
        </div>
        <button
          type="submit"
          className={`${primaryButton} self-start`}
          disabled={pending || !valid}
        >
          {pending ? "Saving…" : "Save the reading"}
        </button>
        <details>
          <summary className="cursor-pointer text-sm font-medium text-gray-700">
            How to take a sample
          </summary>
          <div className="pt-2">
            <SamplingTips />
          </div>
        </details>
      </form>
    </Card>
  );
};

interface ReadingProps {
  test: LactateTest;
  settings: Settings;
  onNext: () => void;
  onRedo: () => void;
  onFinish: () => void;
}

// Reading shows the last stage's reading, and what to do next.
const Reading: React.FC<ReadingProps> = ({
  test,
  settings,
  onNext,
  onRedo,
  onFinish,
}) => {
  const last = test.stages.at(-1);
  const previous = test.stages.at(-2);
  if (last === undefined) {
    return null;
  }
  const done = pastThreshold(test.restingLactateMmolPerLiter, test.stages);
  const bigChange =
    previous !== undefined &&
    Math.abs(last.lactateMmolPerLiter - previous.lactateMmolPerLiter) >=
      BIG_CHANGE_MMOL;
  const next = nextHeartRate(
    test.stages,
    settings.startHeartRate,
    settings.stepBpm
  );
  return (
    <Card
      title={`${formatMmol(last.lactateMmolPerLiter)} at ${last.heartRateBpm} bpm`}
    >
      <div className="flex flex-col gap-3 text-sm text-gray-700">
        {bigChange && (
          <p>
            That's a big change from the last stage. If it looks wrong, hold the
            same heart rate and measure again.
          </p>
        )}
        {done ? (
          <p>
            Your lactate is past every definition of your aerobic threshold, so
            the test is done. Cool down easy
            {test.anaerobicThresholdHeartRateBpm === 0 &&
              ", or run more stages until your lactate passes 4 mmol/L to find your anaerobic threshold too"}
            .
          </p>
        ) : (
          <p>Next, raise your heart rate to {next} bpm.</p>
        )}
        {test.aerobicThresholdHeartRateBpm > 0 && (
          <p className="font-medium text-gray-900">
            Your aerobic threshold: {test.aerobicThresholdHeartRateBpm} bpm.
          </p>
        )}
        {test.anaerobicThresholdHeartRateBpm > 0 && (
          <p className="font-medium text-gray-900">
            Your anaerobic threshold: {test.anaerobicThresholdHeartRateBpm} bpm.
          </p>
        )}
        <div className="flex flex-wrap gap-2">
          <button
            type="button"
            className={primaryButton}
            onClick={done ? onFinish : onNext}
          >
            {done ? "Finish the test" : `Start the ${next} bpm stage`}
          </button>
          <button
            type="button"
            className={secondaryButton}
            onClick={done ? onNext : onFinish}
          >
            {done ? "Run another stage" : "Finish the test"}
          </button>
          <button type="button" className={ghostButton} onClick={onRedo}>
            Measure again
          </button>
        </div>
      </div>
    </Card>
  );
};

// Readings lists the test's readings so far.
const Readings: React.FC<{ test: LactateTest }> = ({ test }) => (
  <ul className="flex flex-col gap-1 text-sm text-gray-700 tabular-nums">
    {test.restingLactateMmolPerLiter > 0 && (
      <li>At rest: {formatMmol(test.restingLactateMmolPerLiter)}</li>
    )}
    {test.stages.map((stage) => (
      <li key={stage.heartRateBpm}>
        {stage.heartRateBpm} bpm: {formatMmol(stage.lactateMmolPerLiter)}
      </li>
    ))}
  </ul>
);

// Phase is the step of a guided test.
type Phase = "prepare" | "resting" | "warm-up" | "stage" | "reading";

/**
 * ContinuedPhase returns the phase a continued test starts at: its next
 * stage, or the warm-up if it has none yet.
 */
const continuedPhase = (test: LactateTest | undefined): Phase | undefined => {
  if (test === undefined) {
    return undefined;
  }
  return test.stages.length === 0 ? "warm-up" : "stage";
};

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
 * GuidedTest guides a lactate test as it's run: the checklist, the resting
 * reading, the warm-up, and each stage, saving each reading as it's entered,
 * so the test can be continued later.
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
  const [chosenPhase, setPhase] = useState<Phase | undefined>(
    loaded === undefined ? "prepare" : undefined
  );
  const phase = chosenPhase ?? continuedPhase(loaded);

  const saveStages = (current: LactateTest, stages: LactateStage[]): void => {
    save(current, stages)
      .then(() => {
        setPhase(stages.length > current.stages.length ? "reading" : "stage");
      })
      .catch(() => {
        // The error is shown below, and the stage can be saved again.
      });
  };

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
          // Without a resting reading to take, the warm-up is next.
          createTest(baseline.mode === "known" ? baseline.mmolPerLiter : 0)
            .then(() => {
              setPhase("warm-up");
            })
            .catch(() => {
              // The error is shown below, and the test can be started again.
            });
        }}
      />
    );
  } else if (phase === "resting") {
    content = (
      <Resting
        pending={pending}
        onSave={(mmolPerLiter) => {
          createTest(mmolPerLiter)
            .then(() => {
              setPhase("warm-up");
            })
            .catch(() => {
              // The error is shown below, and the reading can be saved again.
            });
        }}
      />
    );
  } else if (loaded !== undefined && phase === "warm-up") {
    content = (
      <Card title="Warm up">
        <div className="flex flex-col gap-4">
          <p className="text-sm text-gray-700">
            Warm up for {WARM_UP_MINUTES} to 20 minutes, very easy and below{" "}
            {settings.startHeartRate} bpm, until you're lightly sweating.
          </p>
          <Timer minutes={WARM_UP_MINUTES} />
          <button
            type="button"
            className={`${primaryButton} self-start`}
            onClick={() => {
              setPhase("stage");
            }}
          >
            Start the first stage
          </button>
        </div>
      </Card>
    );
  } else if (loaded !== undefined && phase === "stage") {
    content = (
      <Stage
        // A new stage starts with empty inputs and a reset timer.
        key={loaded.stages.length}
        test={loaded}
        settings={settings}
        pending={pending}
        onSave={(stages) => {
          saveStages(loaded, stages);
        }}
      />
    );
  } else if (loaded !== undefined && phase === "reading") {
    content = (
      <Reading
        test={loaded}
        settings={settings}
        onNext={() => {
          setPhase("stage");
        }}
        onRedo={() => {
          saveStages(loaded, loaded.stages.slice(0, -1));
        }}
        onFinish={() => {
          onFinish(loaded);
        }}
      />
    );
  }

  return (
    <div className="flex flex-col gap-4">
      {content}
      <ErrorText error={error} />
      {loaded !== undefined &&
        (loaded.restingLactateMmolPerLiter > 0 || loaded.stages.length > 0) && (
          <Card title="Readings">
            <Readings test={loaded} />
          </Card>
        )}
    </div>
  );
};

// LactateTestRunPage runs one of the user's lactate tests, or a new one.
const LactateTestRunPage: React.FC = () => {
  const { id } = useParams();
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
    <div className="mx-auto max-w-2xl">
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
            test: { date: today(), restingLactateMmolPerLiter: mmolPerLiter },
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
