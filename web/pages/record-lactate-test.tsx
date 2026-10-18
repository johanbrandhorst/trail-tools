import { create } from "@bufbuild/protobuf";
import Loading from "@components/loading";
import Card from "@components/ui/card";
import { PageHeader } from "@components/ui/misc";
import QueryError from "@components/ui/query-error";
import type { ConnectError, Transport } from "@connectrpc/connect";
import { Code } from "@connectrpc/connect";
import {
  createConnectQueryKey,
  skipToken,
  useMutation,
  useQuery,
  useTransport,
} from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import {
  createLactateTestRecording,
  getLactateTestRecording,
  recordLactateTest,
} from "gen/lactate/v1/lactate-LactateTestService_connectquery";
import type { LactateStage, LactateTest } from "gen/lactate/v1/lactate_pb";
import { GetLactateTestRecordingResponseSchema } from "gen/lactate/v1/lactate_pb";
import { today } from "helpers/dates";
import { formatTimestamp } from "helpers/format";
import { useFragmentToken } from "helpers/hooks";
import { GuidedTest } from "pages/lactate-test-run";
import React, { useEffect, useState } from "react";
import { useNavigate } from "react-router";

/**
 * RecordPath is the page's path, and with a token in the fragment, the
 * recording link.
 */
const RecordPath = "/record/lactate-test";

// RecordingKey is the query key of the recording of this browser.
const recordingKey = (transport: Transport) =>
  createConnectQueryKey({
    cardinality: "finite",
    input: {},
    schema: getLactateTestRecording,
    transport,
  });

interface RecordingErrorProps {
  error: ConnectError;
  onRetry: () => void;
}

// RecordingError says why the page can't record with its link.
const RecordingError: React.FC<RecordingErrorProps> = ({ error, onRetry }) => {
  if (error.code === Code.PermissionDenied) {
    return (
      <div className="py-10 text-center">
        <h1 className="text-xl font-semibold">Recording link not available</h1>
        <p className="mt-2 text-gray-600">
          This recording link has expired, or was opened in another browser. Ask
          for a new link.
        </p>
      </div>
    );
  }
  return <QueryError error={error} onRetry={onRetry} />;
};

/**
 * RecordLactateTestPage records a lactate test for someone else, with the
 * recording link they sent. It opens the link from the token in its URL's
 * fragment, which gives this browser a cookie to record with, and then drops
 * the token from the URL. It works without signing in, and saves the test to
 * their lactate tests.
 */
const RecordLactateTestPage: React.FC = () => {
  const queryClient = useQueryClient();
  const transport = useTransport();
  const navigate = useNavigate();
  const token = useFragmentToken();
  /**
   * Opening the link is a query rather than a mutation so that it's sent
   * once, even when the page renders twice: a second request could find the
   * link opened before the first one's cookie arrives.
   */
  const opened = useQuery(
    createLactateTestRecording,
    token === "" ? skipToken : { token },
    {
      refetchOnWindowFocus: false,
      retry: (failureCount, err) =>
        err.code !== Code.PermissionDenied && failureCount < 3,
      staleTime: Infinity,
    }
  );
  useEffect(() => {
    if (opened.data === undefined) {
      return;
    }
    queryClient.setQueryData(
      recordingKey(transport),
      create(GetLactateTestRecordingResponseSchema, {
        expireTime: opened.data.expireTime,
        ownerName: opened.data.ownerName,
        test: opened.data.test,
      })
    );
    void navigate(RecordPath, { replace: true });
  }, [opened.data, navigate, queryClient, transport]);
  const recording = useQuery(
    getLactateTestRecording,
    token === "" ? {} : skipToken,
    {
      retry: (failureCount, err) =>
        err.code !== Code.PermissionDenied && failureCount < 3,
    }
  );
  const [finished, setFinished] = useState(false);
  const record = useMutation(recordLactateTest, {
    onSuccess: (response) => {
      // The page shows the saved test without fetching it again.
      queryClient.setQueryData(
        recordingKey(transport),
        create(GetLactateTestRecordingResponseSchema, {
          expireTime: recording.data?.expireTime,
          ownerName: recording.data?.ownerName ?? "",
          test: response.test,
        })
      );
    },
  });

  if (token !== "") {
    if (opened.isError) {
      return (
        <RecordingError
          error={opened.error}
          onRetry={() => {
            void opened.refetch();
          }}
        />
      );
    }
    return <Loading />;
  }
  if (recording.isPending) {
    return <Loading />;
  }
  if (recording.isError) {
    return (
      <RecordingError
        error={recording.error}
        onRetry={() => {
          void recording.refetch();
        }}
      />
    );
  }
  const { ownerName, expireTime, test } = recording.data;
  // Saves replace the whole test, so they carry its other fields.
  const save = async (fields: {
    restingLactateMmolPerLiter: number;
    stages: LactateStage[];
  }): Promise<void> => {
    await record.mutateAsync({
      test: {
        date: test?.date ?? today(),
        notes: test?.notes ?? "",
        ...fields,
      },
    });
  };

  return (
    <div className="mx-auto max-w-2xl">
      <PageHeader
        title={`Lactate test for ${ownerName}`}
        subtitle={`The readings are saved to ${ownerName}'s lactate tests as you enter them. The link works until ${formatTimestamp(expireTime)}.`}
      />
      {finished && test !== undefined ? (
        <Card title="Test saved">
          <p className="text-sm text-gray-700">
            Thanks! The test is saved for {ownerName}
            {test.aerobicThresholdHeartRateBpm > 0 &&
              `, with an aerobic threshold of ${test.aerobicThresholdHeartRateBpm} bpm`}
            .
          </p>
        </Card>
      ) : (
        <GuidedTest
          test={test}
          previous={undefined}
          pending={record.isPending}
          error={record.error}
          create={async (mmolPerLiter) => {
            await save({
              restingLactateMmolPerLiter: mmolPerLiter,
              stages: [],
            });
          }}
          saveStages={async (current: LactateTest, stages) => {
            await save({
              restingLactateMmolPerLiter: current.restingLactateMmolPerLiter,
              stages,
            });
          }}
          onFinish={() => {
            setFinished(true);
          }}
        />
      )}
    </div>
  );
};

export default RecordLactateTestPage;
