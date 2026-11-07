import { formatMmol } from "@components/lactate/lactate-utils";
import ShareDialog from "@components/lactate/share-dialog";
import Card from "@components/ui/card";
import {
  linkClass,
  primaryButton,
  secondaryButton,
} from "@components/ui/styles";
import { useQuery } from "@connectrpc/connect-query";
import { listLactateTests } from "gen/lactate/v1/lactate-LactateTestService_connectquery";
import type { Workout } from "gen/plans/v1/plans_pb";
import React, { useState } from "react";
import { Link } from "react-router";

interface WorkoutLactateTestProps {
  workout: Workout;
}

/**
 * WorkoutLactateTest is the lactate test of a run planned as one: it starts
 * the test, or sends a link for someone else to record it, and once there's
 * a test linked to the workout, shows its result.
 */
const WorkoutLactateTest: React.FC<WorkoutLactateTestProps> = ({ workout }) => {
  const tests = useQuery(listLactateTests, {});
  const [sharing, setSharing] = useState(false);
  // The latest test run in the workout, if there's one.
  const test = tests.data?.tests.find(
    (other) => other.workoutId === workout.id
  );
  const readings =
    (test?.restingLactateMmolPerLiter ?? 0) > 0
      ? (test?.stages.length ?? 0) + 1
      : (test?.stages.length ?? 0);
  const last = test?.stages.at(-1);

  return (
    <Card title="Lactate test">
      {test === undefined ? (
        <div className="flex flex-col gap-3 text-sm text-gray-700">
          <p>
            Run the test on your phone, or send a link to whoever holds the
            lactate meter, so they can enter the readings on theirs. Either way
            the test is saved with this workout.
          </p>
          <p className="text-xs text-gray-500">
            On your Garmin calendar, the run has the test&apos;s stages,
            starting where your last test did: your watch keeps you in each
            stage&apos;s heart rate range, and waits for the lap button after
            each sample.
          </p>
          <div className="flex flex-wrap gap-2">
            <button
              type="button"
              className={primaryButton}
              onClick={() => {
                setSharing(true);
              }}
            >
              Send a recording link
            </button>
            <Link
              to={`/lactate-tests/new?workout=${encodeURIComponent(workout.id)}`}
              className={secondaryButton}
            >
              Record it myself
            </Link>
          </div>
        </div>
      ) : (
        <div className="flex flex-col gap-2 text-sm text-gray-700">
          {test.aerobicThresholdHeartRateBpm > 0 ? (
            <p className="text-base font-semibold text-gray-900">
              Aerobic threshold: {test.aerobicThresholdHeartRateBpm} bpm
            </p>
          ) : (
            <p>
              {readings === 0
                ? "No readings yet."
                : `${readings} ${readings === 1 ? "reading" : "readings"} so far, and no threshold yet.`}
              {last !== undefined &&
                ` The last was ${formatMmol(last.lactateMmolPerLiter)} at ${last.heartRateBpm} bpm.`}
            </p>
          )}
          <div className="flex flex-wrap gap-3">
            <Link to={`/lactate-tests/${test.id}`} className={linkClass}>
              See the test
            </Link>
            <Link to={`/lactate-tests/${test.id}/run`} className={linkClass}>
              Continue it
            </Link>
          </div>
        </div>
      )}
      {sharing && (
        <ShareDialog
          workoutId={workout.id}
          onClose={() => {
            setSharing(false);
          }}
        />
      )}
    </Card>
  );
};

export default WorkoutLactateTest;
