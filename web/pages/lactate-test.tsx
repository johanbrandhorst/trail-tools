import LactateChart from "@components/lactate/lactate-chart";
import LactateTestDialog from "@components/lactate/lactate-test-dialog";
import { definitionLabel, formatMmol } from "@components/lactate/lactate-utils";
import Loading from "@components/loading";
import Card from "@components/ui/card";
import ConfirmButton from "@components/ui/confirm-button";
import { PageHeader, Stat } from "@components/ui/misc";
import QueryError, { ErrorText } from "@components/ui/query-error";
import {
  linkClass,
  secondaryButton,
  secondaryDangerButton,
  tableClass,
  tdClass,
  thClass,
} from "@components/ui/styles";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import {
  PencilSquareIcon,
  PlayIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";
import { useQueryClient } from "@tanstack/react-query";
import {
  deleteLactateTest,
  getLactateTest,
} from "gen/lactate/v1/lactate-LactateTestService_connectquery";
import type { LactateTest } from "gen/lactate/v1/lactate_pb";
import {
  LactateTestService,
  ThresholdDefinition,
} from "gen/lactate/v1/lactate_pb";
import { formatDate } from "helpers/format";
import { invalidateServices } from "helpers/query";
import React, { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";

/**
 * AnaerobicResults shows a test's anaerobic threshold by each definition,
 * if the test went hard enough to find one.
 */
const AnaerobicResults: React.FC<{ test: LactateTest }> = ({ test }) => {
  if (!test.anaerobicThresholdEstimates.some((est) => est.heartRateBpm > 0)) {
    return null;
  }
  return (
    <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
      {test.anaerobicThresholdEstimates.map((estimate, index) => (
        <Stat
          key={estimate.definition}
          label={
            index === 0
              ? "Anaerobic threshold"
              : definitionLabel(estimate.definition)
          }
          value={
            estimate.heartRateBpm > 0 ? `${estimate.heartRateBpm} bpm` : "–"
          }
          sub={
            estimate.definition === ThresholdDefinition.DMAX
              ? `Fitted curve, ${formatMmol(estimate.lactateMmolPerLiter)}`
              : `At ${formatMmol(estimate.lactateMmolPerLiter)}`
          }
        />
      ))}
    </dl>
  );
};

/**
 * Results shows a test's aerobic threshold by each definition, its
 * baseline, and its anaerobic threshold if it found one.
 */
const Results: React.FC<{ test: LactateTest }> = ({ test }) => (
  <div className="flex flex-col gap-4">
    <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
      {test.thresholdEstimates.map((estimate, index) => (
        <Stat
          key={estimate.definition}
          label={
            index === 0
              ? "Aerobic threshold"
              : definitionLabel(estimate.definition)
          }
          value={
            estimate.heartRateBpm > 0 ? `${estimate.heartRateBpm} bpm` : "–"
          }
          sub={
            index === 0
              ? "At 2 mmol/L"
              : `At ${formatMmol(estimate.lactateMmolPerLiter)}`
          }
        />
      ))}
      <Stat
        label="Lowest reading"
        value={
          test.lowestLactateMmolPerLiter > 0
            ? formatMmol(test.lowestLactateMmolPerLiter)
            : "–"
        }
        sub={
          test.restingLactateMmolPerLiter > 0
            ? `${formatMmol(test.restingLactateMmolPerLiter)} at rest`
            : "Not measured at rest"
        }
      />
    </dl>
    <AnaerobicResults test={test} />
  </div>
);

// LactateTestPage shows a lactate test's curve and aerobic threshold.
const LactateTestPage: React.FC = () => {
  const { id = "" } = useParams();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const { isPending, isError, data, error, refetch } = useQuery(
    getLactateTest,
    { id }
  );
  const [editing, setEditing] = useState(false);
  const remove = useMutation(deleteLactateTest, {
    onSuccess: async () => {
      await invalidateServices(queryClient, [LactateTestService]);
      void navigate("/lactate-tests");
    },
  });

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
  const { test } = data;
  if (test === undefined) {
    return null;
  }
  const notFound =
    test.stages.length > 0 && test.aerobicThresholdHeartRateBpm === 0;

  return (
    <div>
      <PageHeader
        title={`Lactate test · ${formatDate(test.date)}`}
        subtitle={
          <span className="flex flex-wrap gap-x-3">
            <Link to="/lactate-tests" className={linkClass}>
              All lactate tests
            </Link>
            {test.workoutId !== "" && (
              <Link to={`/workouts/${test.workoutId}`} className={linkClass}>
                The planned workout
              </Link>
            )}
          </span>
        }
        actions={
          <div className="flex flex-wrap gap-2">
            <Link
              to={`/lactate-tests/${test.id}/run`}
              className={secondaryButton}
            >
              <PlayIcon className="size-4" />
              Continue the test
            </Link>
            <button
              type="button"
              className={secondaryButton}
              onClick={() => {
                setEditing(true);
              }}
            >
              <PencilSquareIcon className="size-4" />
              Edit
            </button>
            <ConfirmButton
              className={secondaryDangerButton}
              title="Delete lactate test?"
              message="The test and its readings are deleted. This can't be undone."
              disabled={remove.isPending}
              onConfirm={() => {
                remove.mutate({ id: test.id });
              }}
            >
              <TrashIcon className="size-4" />
              Delete
            </ConfirmButton>
          </div>
        }
      />
      <ErrorText error={remove.error} />
      <div className="flex flex-col gap-4">
        <Card title="Results">
          <div className="flex flex-col gap-3">
            <Results test={test} />
            {notFound && (
              <p className="text-sm text-gray-600">
                No stage rose to 2 mmol/L from below. If the first stage was
                already above it, start lower next time; otherwise, continue the
                test at higher heart rates.
              </p>
            )}
          </div>
        </Card>
        <Card title="Lactate and heart rate">
          <LactateChart test={test} />
        </Card>
        {test.stages.length > 0 && (
          <Card title="Stages">
            <div className="overflow-x-auto">
              <table className={tableClass}>
                <thead>
                  <tr>
                    <th className={thClass}>Stage</th>
                    <th className={thClass}>Heart rate</th>
                    <th className={thClass}>Lactate</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100">
                  {test.stages.map((stage, index) => (
                    <tr key={stage.heartRateBpm}>
                      <td className={tdClass}>{index + 1}</td>
                      <td className={`${tdClass} tabular-nums`}>
                        {stage.heartRateBpm} bpm
                      </td>
                      <td className={`${tdClass} tabular-nums`}>
                        {formatMmol(stage.lactateMmolPerLiter)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </Card>
        )}
        {test.notes !== "" && (
          <Card title="Notes">
            <p className="text-sm whitespace-pre-line text-gray-800">
              {test.notes}
            </p>
          </Card>
        )}
      </div>
      {editing && (
        <LactateTestDialog
          test={test}
          onClose={() => {
            setEditing(false);
          }}
        />
      )}
    </div>
  );
};

export default LactateTestPage;
