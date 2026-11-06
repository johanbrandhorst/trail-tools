import Loading from "@components/loading";
import AnimationPlayer from "@components/strength/animation-player";
import BodyMap, { MuscleChips } from "@components/strength/body-map";
import Card from "@components/ui/card";
import { Badge, EmptyState } from "@components/ui/misc";
import QueryError from "@components/ui/query-error";
import { linkClass, tableClass, tdClass, thClass } from "@components/ui/styles";
import { Code } from "@connectrpc/connect";
import { useQuery } from "@connectrpc/connect-query";
import { ArrowLeftIcon } from "@heroicons/react/24/outline";
import {
  getExercise,
  listExerciseHistory,
} from "gen/plans/v1/exercises-ExerciseService_connectquery";
import type { ExerciseHistoryEntry } from "gen/plans/v1/exercises_pb";
import { formatDate } from "helpers/format";
import {
  equipmentLabel,
  feedbackStyles,
  formatDose,
  formatSet,
  workoutMuscles,
} from "helpers/strength";
import React from "react";
import { Link, useParams } from "react-router";

/**
 * ExercisePage shows an exercise from the library: how to do it, how it
 * should feel, the muscles it works, and the history of what the user did
 * in it, most recent first.
 */
const ExercisePage: React.FC = () => {
  const { id = "" } = useParams();
  const exercise = useQuery(getExercise, { id });
  const history = useQuery(listExerciseHistory, { exerciseIds: [id] });

  if (exercise.isPending) {
    return <Loading />;
  }
  if (exercise.isError) {
    if (exercise.error.code === Code.NotFound) {
      return (
        <div className="py-16 text-center">
          <h1 className="text-xl font-semibold">Exercise not found</h1>
          <Link to="/exercises" className={linkClass}>
            See your exercises
          </Link>
        </div>
      );
    }
    return (
      <QueryError
        error={exercise.error}
        onRetry={() => {
          void exercise.refetch();
        }}
      />
    );
  }
  const ex = exercise.data.exercise;
  if (ex === undefined) {
    return null;
  }
  const muscles = workoutMuscles([ex]);
  // The most recent first.
  const entries: ExerciseHistoryEntry[] = [];
  for (const entry of history.data?.entries ?? []) {
    entries.unshift(entry);
  }

  return (
    <div className="mx-auto flex w-full max-w-5xl flex-col gap-4">
      <Link
        to="/exercises"
        className="inline-flex items-center gap-1 self-start text-sm text-gray-600 hover:text-gray-900"
      >
        <ArrowLeftIcon className="size-4" />
        Exercises
      </Link>
      <header className="flex flex-col gap-1">
        <h1 className="text-2xl font-bold text-balance text-gray-900">
          {ex.name}
        </h1>
        <p className="text-sm text-gray-600">
          {[
            ex.equipment.length === 0
              ? "No equipment"
              : ex.equipment.map(equipmentLabel).join(", "),
            ex.equipmentNotes,
            ex.eachSide ? "Each side" : "",
          ]
            .filter((part) => part !== "")
            .join(" · ")}
        </p>
      </header>
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,22rem)]">
        <div className="flex min-w-0 flex-col gap-4">
          {ex.animations.length > 0 && (
            <AnimationPlayer animations={ex.animations} />
          )}
          {ex.instructions.length > 0 && (
            <Card title="How to do it">
              <ol className="flex list-decimal flex-col gap-2 pl-5 text-sm leading-relaxed text-gray-700">
                {ex.instructions.map((step) => (
                  <li key={step}>{step}</li>
                ))}
              </ol>
            </Card>
          )}
          {ex.feel !== "" && (
            <Card title="How it should feel">
              <p className="text-sm leading-relaxed whitespace-pre-line text-gray-700">
                {ex.feel}
              </p>
            </Card>
          )}
          <Card title="History">
            {history.isPending && <Loading />}
            {history.isError && (
              <QueryError
                error={history.error}
                onRetry={() => {
                  void history.refetch();
                }}
              />
            )}
            {history.data !== undefined && entries.length === 0 && (
              <EmptyState title="Not done yet">
                Workouts you log with this exercise show up here.
              </EmptyState>
            )}
            {entries.length > 0 && (
              <div className="-m-4 overflow-x-auto">
                <table className={tableClass}>
                  <thead>
                    <tr>
                      <th className={thClass}>Date</th>
                      <th className={thClass}>Planned</th>
                      <th className={thClass}>Done</th>
                      <th className={thClass}>How it felt</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-gray-100">
                    {entries.map((entry, index) => (
                      /*
                       * An exercise can be in a workout twice, so the index
                       * is part of the key.
                       */
                      // oxlint-disable-next-line no-array-index-key
                      <tr key={`${entry.date}-${index}`}>
                        <td className={`${tdClass} whitespace-nowrap`}>
                          {entry.workoutId === "" ? (
                            formatDate(entry.date)
                          ) : (
                            <Link
                              to={`/workouts/${entry.workoutId}`}
                              className={linkClass}
                              title={entry.workoutTitle}
                            >
                              {formatDate(entry.date)}
                            </Link>
                          )}
                        </td>
                        <td
                          className={`${tdClass} whitespace-nowrap tabular-nums`}
                        >
                          {entry.planned === undefined
                            ? "–"
                            : formatDose(entry.planned, ex.eachSide)}
                        </td>
                        <td
                          className={`${tdClass} whitespace-nowrap tabular-nums`}
                        >
                          {entry.logged === undefined ||
                          entry.logged.sets.length === 0
                            ? "–"
                            : entry.logged.sets.map(formatSet).join(", ")}
                        </td>
                        <td className={tdClass}>
                          <div className="flex flex-wrap gap-1">
                            {entry.logged !== undefined &&
                              feedbackStyles(
                                entry.logged.effort,
                                entry.logged.volume,
                                entry.logged.stiffness
                              ).map((style) => (
                                <Badge
                                  key={style.label}
                                  className={style.badgeClass}
                                >
                                  {style.label}
                                </Badge>
                              ))}
                          </div>
                          {entry.logged !== undefined &&
                            entry.logged.notes !== "" && (
                              <p className="mt-1 text-xs text-gray-600">
                                {entry.logged.notes}
                              </p>
                            )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </Card>
        </div>
        <div className="flex min-w-0 flex-col gap-4">
          <Card title="Muscles">
            <div className="flex flex-col gap-3">
              <BodyMap muscles={muscles} />
              <MuscleChips muscles={muscles} />
            </div>
          </Card>
        </div>
      </div>
    </div>
  );
};

export default ExercisePage;
