import Loading from "@components/loading";
import RunSteps from "@components/plans/run-steps";
import { Goal, RouteCard } from "@components/plans/workout-goal";
import AnimationPlayer from "@components/strength/animation-player";
import Card from "@components/ui/card";
import QueryError from "@components/ui/query-error";
import { secondaryButton } from "@components/ui/styles";
import { Code } from "@connectrpc/connect";
import { skipToken, useQuery } from "@connectrpc/connect-query";
import { ArrowDownTrayIcon } from "@heroicons/react/24/outline";
import type { Exercise } from "gen/plans/v1/exercises_pb";
import type { Workout } from "gen/plans/v1/plans_pb";
import { getSharedWorkout } from "gen/plans/v1/shares-WorkoutShareService_connectquery";
import { formatDate } from "helpers/format";
import { useFragmentToken } from "helpers/hooks";
import { workoutStyle } from "helpers/labels";
import { formatDose, groupExercises } from "helpers/strength";
import { startWindow } from "helpers/workouts";
import React, { useEffect, useMemo, useState } from "react";

/**
 * SHARE_FILES_PATH serves a shared workout's files. The token goes in a
 * header rather than the URL, so it stays out of server and proxy logs.
 */
const SHARE_FILES_PATH = "/share/workout-files/";

// WorkoutNotAvailable says that a share link doesn't work.
const WorkoutNotAvailable: React.FC = () => (
  <div className="py-10 text-center">
    <h1 className="text-xl font-semibold">Workout not available</h1>
    <p className="mt-2 text-gray-600">
      This workout is no longer shared, or the link is wrong. Ask for a new
      link.
    </p>
  </div>
);

// FileName makes a file name from a workout's title, like "hill-repeats.gpx".
const fileName = (title: string, extension: string): string => {
  const slug = title
    .toLowerCase()
    .replaceAll(/[^a-z0-9]+/gu, "-")
    .replaceAll(/^-|-$/gu, "");
  return `${slug === "" ? "workout" : slug}.${extension}`;
};

interface DownloadButtonProps {
  token: string;
  // The file to download, e.g. "route.gpx".
  file: string;
  // The name to save it as.
  saveAs: string;
  children: React.ReactNode;
}

/**
 * DownloadButton downloads one of a shared workout's files. A plain link
 * can't send the token in a header, so it fetches the file and saves it.
 */
const DownloadButton: React.FC<DownloadButtonProps> = ({
  token,
  file,
  saveAs,
  children,
}) => {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const download = async () => {
    setPending(true);
    setError("");
    try {
      const response = await fetch(SHARE_FILES_PATH + file, {
        headers: { "X-Share-Token": token },
      });
      if (!response.ok) {
        const message = (await response.text()).trim();
        throw new Error(
          message === "" ? `Download failed: ${response.statusText}` : message
        );
      }
      const url = URL.createObjectURL(await response.blob());
      const link = document.createElement("a");
      link.href = url;
      link.download = saveAs;
      link.click();
      URL.revokeObjectURL(url);
    } catch (error_) {
      setError(error_ instanceof Error ? error_.message : String(error_));
    } finally {
      setPending(false);
    }
  };
  return (
    <div className="flex flex-col gap-1">
      <button
        type="button"
        className={secondaryButton}
        disabled={pending}
        onClick={() => {
          void download();
        }}
      >
        <ArrowDownTrayIcon className="size-4" />
        {children}
      </button>
      {error !== "" && <p className="text-sm text-red-700">{error}</p>}
    </div>
  );
};

interface SharedExercisesProps {
  workout: Workout;
  exercises: Exercise[];
}

// SharedExercises lists a strength workout's exercises with how to do them.
const SharedExercises: React.FC<SharedExercisesProps> = ({
  workout,
  exercises,
}) => {
  const library = useMemo(
    () => new Map(exercises.map((ex) => [ex.id, ex])),
    [exercises]
  );
  if (workout.details.case !== "strength") {
    return null;
  }
  const groups = groupExercises(workout.details.value.exercises);
  return (
    <Card title="Exercises">
      <div className="flex flex-col gap-6">
        {groups.map((group) => (
          <section
            key={`${group.name}-${group.items[0]?.index ?? 0}`}
            className="flex flex-col gap-4"
          >
            {group.name !== "" && (
              <h3 className="text-xs font-semibold tracking-wide text-gray-500 uppercase">
                {group.name}
              </h3>
            )}
            {group.items.map(({ item, index }) => {
              const ex = library.get(item.exerciseId);
              return (
                <article key={index} className="flex flex-col gap-2">
                  <div className="flex flex-wrap items-baseline justify-between gap-x-3">
                    <h4 className="font-semibold text-gray-900">
                      {item.exerciseName}
                    </h4>
                    <p className="text-sm text-gray-600 tabular-nums">
                      {formatDose(item, ex?.eachSide ?? false)}
                    </p>
                  </div>
                  {item.notes !== "" && (
                    <p className="text-sm text-gray-700">{item.notes}</p>
                  )}
                  {ex !== undefined && ex.animations.length > 0 && (
                    <AnimationPlayer animations={ex.animations} />
                  )}
                  {ex !== undefined && ex.instructions.length > 0 && (
                    <ol className="flex list-decimal flex-col gap-1 pl-5 text-sm leading-relaxed text-gray-700">
                      {ex.instructions.map((step) => (
                        <li key={step}>{step}</li>
                      ))}
                    </ol>
                  )}
                </article>
              );
            })}
          </section>
        ))}
      </div>
    </Card>
  );
};

/**
 * SharedWorkoutPage is the read-only view of a workout shared by link, at
 * /share/workouts#<token>. It works without signing in.
 */
const SharedWorkoutPage: React.FC = () => {
  const token = useFragmentToken();
  const { isPending, isError, data, error, refetch } = useQuery(
    getSharedWorkout,
    token === "" ? skipToken : { token },
    {
      retry: (failureCount, err) =>
        err.code !== Code.NotFound && failureCount < 3,
    }
  );
  const workout = data?.workout;
  let title = workout?.title.trim() ?? "";
  if (workout !== undefined && title === "") {
    title = workoutStyle(workout.type).label;
  }

  useEffect(() => {
    if (title === "") {
      return undefined;
    }
    const previous = document.title;
    document.title = title;
    return () => {
      document.title = previous;
    };
  }, [title]);

  if (token === "") {
    return <WorkoutNotAvailable />;
  }
  if (isPending) {
    return <Loading />;
  }
  if (isError) {
    if (error.code === Code.NotFound) {
      return <WorkoutNotAvailable />;
    }
    return (
      <QueryError
        error={error}
        onRetry={() => {
          void refetch();
        }}
      />
    );
  }
  if (workout === undefined) {
    return <WorkoutNotAvailable />;
  }
  const style = workoutStyle(workout.type);
  const { route } = data;
  const strengthFile =
    workout.details.case === "strength" &&
    workout.details.value.exercises.length > 0;

  return (
    <div className="mx-auto flex w-full max-w-4xl flex-col gap-4">
      <header className="flex flex-col gap-1">
        <p className="text-xs font-semibold tracking-wide text-brand-600 uppercase">
          Shared workout
        </p>
        <p className="flex items-center gap-2 text-sm font-medium text-gray-600">
          <span
            className={`inline-block size-2.5 rounded-sm ${style.dotClass}`}
          />
          {/* Lines break between the parts, not within them. */}
          <span className="flex flex-wrap gap-x-1">
            <span className="whitespace-nowrap">
              {style.label} · {formatDate(workout.date)}
            </span>
            {startWindow(workout) !== "" && (
              <span className="whitespace-nowrap">
                · Start {startWindow(workout)}
              </span>
            )}
            {workout.location !== undefined && (
              <span className="whitespace-nowrap">
                ·{" "}
                <a
                  href={`https://www.openstreetmap.org/?mlat=${workout.location.lat}&mlon=${workout.location.lng}#map=16/${workout.location.lat}/${workout.location.lng}`}
                  target="_blank"
                  rel="noreferrer"
                  className="hover:underline"
                >
                  At{" "}
                  {workout.locationName === ""
                    ? "the meeting point"
                    : workout.locationName}
                </a>
              </span>
            )}
          </span>
        </p>
        <h1 className="text-2xl font-bold text-balance text-gray-900">
          {title}
        </h1>
      </header>
      <Goal workout={workout} />
      {workout.details.case === "run" &&
        workout.details.value.blocks.length > 0 && (
          <RunSteps blocks={workout.details.value.blocks} />
        )}
      {route !== undefined && (
        <RouteCard
          name={route.name}
          points={route.points}
          stats={route.stats}
          link=""
          fitKey={token}
        >
          <div className="flex flex-wrap gap-2">
            <DownloadButton
              token={token}
              file="route.fit"
              saveAs={fileName(route.name, "fit")}
            >
              Course for Garmin (.fit)
            </DownloadButton>
            <DownloadButton
              token={token}
              file="route.gpx"
              saveAs={fileName(route.name, "gpx")}
            >
              GPX
            </DownloadButton>
          </div>
          <p className="text-xs text-gray-500">
            Import the course in Garmin Connect, or copy the file to your
            watch&apos;s NewFiles folder, to follow it on your watch.
          </p>
        </RouteCard>
      )}
      <SharedExercises workout={workout} exercises={data.exercises} />
      {strengthFile && (
        <Card title="On your watch">
          <div className="flex flex-col gap-2">
            <p className="text-sm text-gray-600">
              Download the workout to do it on a Garmin watch: copy the file to
              the watch&apos;s NewFiles folder.
            </p>
            <div className="self-start">
              <DownloadButton
                token={token}
                file="workout.fit"
                saveAs={fileName(title, "fit")}
              >
                Workout for Garmin (.fit)
              </DownloadButton>
            </div>
          </div>
        </Card>
      )}
    </div>
  );
};

export default SharedWorkoutPage;
