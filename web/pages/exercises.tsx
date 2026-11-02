import Loading from "@components/loading";
import { MuscleChips } from "@components/strength/body-map";
import { EmptyState, PageHeader } from "@components/ui/misc";
import QueryError from "@components/ui/query-error";
import { cardClass } from "@components/ui/styles";
import { useQuery } from "@connectrpc/connect-query";
import { ChevronRightIcon } from "@heroicons/react/24/outline";
import { listExercises } from "gen/plans/v1/exercises-ExerciseService_connectquery";
import { workoutMuscles } from "helpers/strength";
import React from "react";
import { Link } from "react-router";

/**
 * ExercisesPage lists the user's exercise library, which strength workouts
 * are planned from through the MCP server.
 */
const ExercisesPage: React.FC = () => {
  const { isPending, isError, data, error, refetch } = useQuery(
    listExercises,
    {}
  );
  let content: React.ReactNode;
  if (isPending) {
    content = <Loading />;
  } else if (isError) {
    content = (
      <QueryError
        error={error}
        onRetry={() => {
          void refetch();
        }}
      />
    );
  } else if (data.exercises.length === 0) {
    content = (
      <EmptyState title="No exercises yet">
        Ask your agent to plan a strength workout through the MCP server, and it
        adds the exercises here.
      </EmptyState>
    );
  } else {
    content = (
      <ul className={`${cardClass} divide-y divide-gray-100`}>
        {data.exercises.map((exercise) => (
          <li key={exercise.id}>
            <Link
              to={`/exercises/${exercise.id}`}
              className="flex items-center gap-3 px-4 py-3 hover:bg-gray-50"
            >
              <span className="flex min-w-0 grow flex-col gap-1.5">
                <span className="font-medium text-gray-900">
                  {exercise.name}
                </span>
                <MuscleChips muscles={workoutMuscles([exercise])} />
              </span>
              <ChevronRightIcon className="size-5 shrink-0 text-gray-400" />
            </Link>
          </li>
        ))}
      </ul>
    );
  }
  return (
    <div className="mx-auto flex w-full max-w-3xl flex-col">
      <PageHeader
        title="Exercises"
        subtitle="Your strength and mobility exercises, and what you did in each."
      />
      {content}
    </div>
  );
};

export default ExercisesPage;
