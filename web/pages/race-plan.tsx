import { create } from "@bufbuild/protobuf";
import Loading from "@components/loading";
import RaceDayPanel from "@components/race-day/race-day-panel";
import { useRaceDay } from "@components/race-day/use-race-day";
import PlanDocument, {
  RaceHeader,
  RulesPanel,
} from "@components/race-plan/plan-document";
import Card from "@components/ui/card";
import ConfirmButton from "@components/ui/confirm-button";
import { CopyField } from "@components/ui/misc";
import QueryError, { ErrorText } from "@components/ui/query-error";
import {
  linkClass,
  primaryButton,
  secondaryButton,
} from "@components/ui/styles";
import {
  createConnectQueryKey,
  useMutation,
  useQuery,
  useTransport,
} from "@connectrpc/connect-query";
import {
  ArrowDownTrayIcon,
  PrinterIcon,
  ShareIcon,
} from "@heroicons/react/24/outline";
import { skipToken, useQueryClient } from "@tanstack/react-query";
import {
  deleteRacePlan,
  getRacePlan,
  listRacePlans,
  shareRacePlan,
  unshareRacePlan,
  updateRacePlan,
} from "gen/races/v1/race_plans-RacePlanService_connectquery";
import type { RacePlan } from "gen/races/v1/race_plans_pb";
import {
  GetRacePlanResponseSchema,
  RacePlanService,
} from "gen/races/v1/race_plans_pb";
import { getRoute } from "gen/routes/v1/routes-RouteService_connectquery";
import type { RoutePoint } from "gen/routes/v1/routes_pb";
import { invalidateServices } from "helpers/query";
import React from "react";
import { Link, useNavigate, useParams } from "react-router";

const NO_POINTS: RoutePoint[] = [];

interface ShareControlsProps {
  plan: RacePlan;
}

const ShareControls: React.FC<ShareControlsProps> = ({ plan }) => {
  const queryClient = useQueryClient();
  const onSuccess = async () => {
    await invalidateServices(queryClient, [RacePlanService]);
  };
  const share = useMutation(shareRacePlan, { onSuccess });
  const unshare = useMutation(unshareRacePlan, { onSuccess });
  const shareUrl =
    plan.shareUrl === "" ? (share.data?.shareUrl ?? "") : plan.shareUrl;

  return (
    <div className="flex flex-col gap-2">
      <p className="text-sm text-gray-600">
        Share a link with your crew and pacers. Anyone with the link can view
        the plan and, on race day, record your start and checkpoint times and
        link your LiveTrack. Share it only with people you trust, and stop
        sharing when the race is over.
      </p>
      {shareUrl === "" ? (
        <button
          type="button"
          className={primaryButton}
          disabled={share.isPending}
          onClick={() => {
            share.mutate({ id: plan.id });
          }}
        >
          <ShareIcon className="size-4" />
          Create share link
        </button>
      ) : (
        <>
          <CopyField value={shareUrl} />
          <div className="flex gap-3 text-sm">
            <a
              href={shareUrl}
              target="_blank"
              rel="noreferrer"
              className={linkClass}
            >
              Open
            </a>
            <button
              type="button"
              className="font-medium text-red-700 hover:underline"
              disabled={unshare.isPending}
              onClick={() => {
                share.reset();
                unshare.mutate({ id: plan.id });
              }}
            >
              Stop sharing
            </button>
          </div>
        </>
      )}
      <ErrorText error={share.error ?? unshare.error} />
    </div>
  );
};

const RacePlanPage: React.FC = () => {
  const { id = "" } = useParams();
  const queryClient = useQueryClient();
  const transport = useTransport();
  const navigate = useNavigate();
  const { isPending, isError, data, error, refetch } = useQuery(getRacePlan, {
    id,
  });
  const raceDay = useRaceDay({ planId: id, shareToken: "" });
  const courseRouteId = data?.race?.courseRouteId ?? "";
  const course = useQuery(
    getRoute,
    courseRouteId === ""
      ? skipToken
      : { id: courseRouteId, includePoints: true }
  );
  const update = useMutation(updateRacePlan);
  const remove = useMutation(deleteRacePlan, {
    onSuccess: async () => {
      await invalidateServices(queryClient, [RacePlanService]);
      void navigate("/races");
    },
  });

  /**
   * Save replaces one section of the plan and stores the returned plan and
   * analysis in the query cache, so the page updates without a refetch.
   */
  const save = async (plan: RacePlan, section: string): Promise<void> => {
    const response = await update.mutateAsync({
      plan,
      updateMask: { paths: [section] },
    });
    queryClient.setQueryData(
      createConnectQueryKey({
        cardinality: "finite",
        input: { id },
        schema: getRacePlan,
        transport,
      }),
      create(GetRacePlanResponseSchema, {
        analysis: response.analysis,
        plan: response.plan,
        race: response.race,
      })
    );
    await queryClient.invalidateQueries({
      queryKey: createConnectQueryKey({
        cardinality: undefined,
        schema: listRacePlans,
      }),
    });
  };

  if (isPending) {
    return <Loading />;
  }
  if (isError) {
    return (
      <QueryError
        error={error}
        onRetry={() => {
          void refetch();
        }}
      />
    );
  }
  const { plan, race, analysis } = data;
  if (plan === undefined) {
    return <p>Race plan not found.</p>;
  }

  return (
    <div className="flex flex-col gap-4">
      <RaceHeader race={race}>
        <Link
          to="/races"
          className="text-sm text-gray-500 hover:underline print:hidden"
        >
          Races
        </Link>
      </RaceHeader>
      <RaceDayPanel
        raceDay={raceDay}
        plan={plan}
        race={race}
        course={course.data?.route?.points ?? NO_POINTS}
        shared={false}
      />
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
        <div className="order-2 min-w-0 lg:order-1">
          {course.isError && (
            <p className="mb-2 text-sm text-amber-800">
              Could not load the course route: {course.error.rawMessage}
            </p>
          )}
          <PlanDocument
            plan={plan}
            race={race}
            analysis={analysis}
            coursePoints={course.data?.route?.points ?? NO_POINTS}
            onSave={save}
          />
        </div>
        <aside className="order-1 flex flex-col gap-4 lg:sticky lg:top-4 lg:order-2 lg:max-h-[calc(100vh-2rem)] lg:self-start lg:overflow-y-auto print:hidden">
          <Card>
            <RulesPanel analysis={analysis} />
          </Card>
          <Card title="Share">
            <ShareControls plan={plan} />
          </Card>
          <div className="flex flex-wrap gap-2">
            <button
              type="button"
              className={secondaryButton}
              onClick={() => {
                globalThis.print();
              }}
            >
              <PrinterIcon className="size-4" />
              Print
            </button>
            {courseRouteId !== "" && (
              <a
                href={`/race-plans/${plan.id}.fit`}
                className={secondaryButton}
                title="The course for Garmin and other watches, with the checkpoints and a virtual partner on your planned splits"
                download
              >
                <ArrowDownTrayIcon className="size-4" />
                Garmin course
              </a>
            )}
            <ConfirmButton
              title="Delete race plan?"
              message="The race itself is kept. A new plan can be created from the Races page."
              disabled={remove.isPending}
              onConfirm={() => {
                remove.mutate({ id: plan.id });
              }}
            >
              Delete plan
            </ConfirmButton>
          </div>
          <ErrorText error={remove.error} />
        </aside>
      </div>
    </div>
  );
};

export default RacePlanPage;
