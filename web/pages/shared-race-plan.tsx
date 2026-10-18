import Loading from "@components/loading";
import RaceDayPanel from "@components/race-day/race-day-panel";
import { useRaceDay } from "@components/race-day/use-race-day";
import PlanDocument, { RaceHeader } from "@components/race-plan/plan-document";
import QueryError from "@components/ui/query-error";
import { secondaryButton } from "@components/ui/styles";
import { Code } from "@connectrpc/connect";
import { skipToken, useQuery } from "@connectrpc/connect-query";
import { PrinterIcon } from "@heroicons/react/24/outline";
import { getSharedRacePlan } from "gen/races/v1/race_plans-RacePlanService_connectquery";
import { formatDurationLong } from "helpers/format";
import { useFragmentToken } from "helpers/hooks";
import React from "react";

// PlanNotAvailable says that a share link doesn't work.
const PlanNotAvailable: React.FC = () => (
  <div className="py-10 text-center">
    <h1 className="text-xl font-semibold">Plan not available</h1>
    <p className="mt-2 text-gray-600">
      This race plan is no longer shared, or the link is wrong.
    </p>
  </div>
);

/**
 * SharedRacePlanPage is the read-only view of a plan shared by link. It works
 * without signing in.
 */
const SharedRacePlanPage: React.FC = () => {
  const token = useFragmentToken();
  const { isPending, isError, data, error, refetch } = useQuery(
    getSharedRacePlan,
    token === "" ? skipToken : { token },
    {
      retry: (failureCount, err) =>
        err.code !== Code.NotFound &&
        err.code !== Code.PermissionDenied &&
        failureCount < 3,
    }
  );
  const raceDay = useRaceDay({ planId: "", shareToken: token });

  if (token === "") {
    return <PlanNotAvailable />;
  }
  if (isPending) {
    return <Loading />;
  }
  if (isError) {
    if (error.code === Code.NotFound || error.code === Code.PermissionDenied) {
      return <PlanNotAvailable />;
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
  const { plan, race, analysis, course } = data;
  if (plan === undefined) {
    return <p>Race plan not found.</p>;
  }

  return (
    <div className="mx-auto flex w-full max-w-4xl flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <RaceHeader race={race}>
          <p className="text-xs font-semibold tracking-wide text-brand-600 uppercase">
            Race plan
          </p>
        </RaceHeader>
        <button
          type="button"
          className={`${secondaryButton} print:hidden`}
          onClick={() => {
            globalThis.print();
          }}
        >
          <PrinterIcon className="size-4" />
          Print
        </button>
      </div>
      <RaceDayPanel
        raceDay={raceDay}
        plan={plan}
        race={race}
        course={course}
        shared
      />
      {analysis !== undefined && analysis.plannedFinishSeconds > 0 && (
        <div className="rounded-lg bg-gray-800 px-4 py-3 text-white print:bg-white print:text-gray-900 print:ring-1 print:ring-gray-300">
          <span className="text-xs tracking-wide text-gray-300 uppercase print:text-gray-500">
            Planned finish
          </span>
          <p className="text-2xl font-bold tabular-nums">
            {formatDurationLong(analysis.plannedFinishSeconds)}
            {analysis.plannedFinishClockTime !== "" && (
              <span className="ml-2 text-base font-normal text-gray-300 print:text-gray-600">
                at {analysis.plannedFinishClockTime}
              </span>
            )}
          </p>
        </div>
      )}
      <PlanDocument
        plan={plan}
        race={race}
        analysis={analysis}
        coursePoints={course}
        onSave={undefined}
      />
    </div>
  );
};

export default SharedRacePlanPage;
