import Loading from "@components/loading";
import {
  ConnectResultNotice,
  useConnectResult,
} from "@components/strava/connect-result";
import Card from "@components/ui/card";
import ConfirmButton from "@components/ui/confirm-button";
import { Stat } from "@components/ui/misc";
import QueryError, { ErrorText } from "@components/ui/query-error";
import { primaryButton } from "@components/ui/styles";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import {
  ArrowPathIcon,
  CheckCircleIcon,
  XCircleIcon,
} from "@heroicons/react/24/outline";
import { useQueryClient } from "@tanstack/react-query";
import { ActivityService } from "gen/activities/v1/activities_pb";
import {
  disconnectGarmin,
  getGarminConnection,
  syncGarmin,
} from "gen/garmin/v1/garmin-GarminService_connectquery";
import { GarminService } from "gen/garmin/v1/garmin_pb";
import { TrainingPlanService } from "gen/plans/v1/plans_pb";
import { formatTimestamp } from "helpers/format";
import { invalidateServices } from "helpers/query";
import React from "react";

interface PermissionProps {
  allowed: boolean;
  label: string;
}

// Permission shows whether the user granted one of Garmin's permissions.
const Permission: React.FC<PermissionProps> = ({ allowed, label }) => (
  <li className="flex items-center gap-1.5">
    {allowed ? (
      <CheckCircleIcon className="size-4 shrink-0 text-emerald-600" />
    ) : (
      <XCircleIcon className="size-4 shrink-0 text-gray-400" />
    )}
    <span className={allowed ? "text-gray-800" : "text-gray-500"}>{label}</span>
  </li>
);

interface GarminCardProps {
  // Show the disconnect button, used on the settings page.
  showDisconnect?: boolean;
  /**
   * Show nothing if the server has no Garmin credentials, rather than how to
   * add them, used on the dashboard.
   */
  hideUnconfigured?: boolean;
}

/**
 * GarminCard shows the Garmin Connect connection: what the user allowed,
 * how many workouts are on their Garmin calendar, and the last sync, with
 * buttons to connect, sync and disconnect.
 */
const GarminCard: React.FC<GarminCardProps> = ({
  showDisconnect = false,
  hideUnconfigured = false,
}) => {
  const queryClient = useQueryClient();
  const connectResult = useConnectResult("garmin");
  const { isPending, isError, data, error, refetch } =
    useQuery(getGarminConnection);
  const sync = useMutation(syncGarmin, {
    onSuccess: async () => {
      await invalidateServices(queryClient, [
        GarminService,
        ActivityService,
        TrainingPlanService,
      ]);
    },
  });
  const disconnect = useMutation(disconnectGarmin, {
    onSuccess: async () => {
      await invalidateServices(queryClient, [GarminService]);
    },
  });

  if (isPending) {
    return (
      <Card title="Garmin Connect">
        <Loading />
      </Card>
    );
  }
  if (isError) {
    return (
      <Card title="Garmin Connect">
        <QueryError
          error={error}
          onRetry={() => {
            void refetch();
          }}
        />
      </Card>
    );
  }

  const { connection } = data;
  if (connection === undefined || !connection.configured) {
    if (hideUnconfigured) {
      return null;
    }
    return (
      <Card title="Garmin Connect">
        <p className="text-sm text-gray-700">
          Garmin is not configured on this server. Start the server with the{" "}
          <code className="rounded bg-gray-100 px-1">-garmin-client-id</code>,{" "}
          <code className="rounded bg-gray-100 px-1">
            -garmin-client-secret
          </code>{" "}
          and{" "}
          <code className="rounded bg-gray-100 px-1">
            -garmin-webhook-secret
          </code>{" "}
          flags to enable it.
        </p>
      </Card>
    );
  }

  if (!connection.connected) {
    return (
      <Card title="Garmin Connect">
        <div className="flex flex-col items-start gap-3">
          <ConnectResultNotice
            result={connectResult}
            service="Garmin"
            connectedText=""
          />
          <p className="text-sm text-gray-700">
            Connect your Garmin account to get activities straight from your
            watch, with the sets of your strength workouts, and to put your
            upcoming runs and strength workouts on your Garmin calendar, with
            their routes as courses to navigate. If you use Strava too,
            activities on both are kept once.
          </p>
          <a
            href="/garmin/connect"
            className="inline-flex items-center rounded-md bg-ink px-3 py-1.5 text-sm font-semibold text-white shadow-sm hover:bg-gray-800"
          >
            Connect Garmin
          </a>
        </div>
      </Card>
    );
  }

  return (
    <Card title="Garmin Connect">
      <div className="flex flex-col gap-4">
        <ConnectResultNotice
          result={connectResult}
          service="Garmin"
          connectedText="Your recent activities arrive over the next few minutes."
        />
        <ul className="flex flex-col gap-1 text-sm">
          <Permission
            allowed={connection.activitiesAllowed}
            label="Activities from your watch"
          />
          <Permission
            allowed={connection.workoutsAllowed}
            label="Workouts on your Garmin calendar"
          />
          <Permission
            allowed={connection.coursesAllowed}
            label="Routes of upcoming runs and races as courses"
          />
        </ul>
        {(!connection.activitiesAllowed ||
          !connection.workoutsAllowed ||
          !connection.coursesAllowed) && (
          <p className="text-xs text-gray-600">
            Change what trailrunn.ing may do in Garmin Connect, under Connected
            apps, or connect again.
          </p>
        )}
        <div className="grid grid-cols-2 gap-3">
          <Stat label="Workouts on Garmin" value={connection.workoutCount} />
          <Stat label="Courses on Garmin" value={connection.courseCount} />
          <Stat
            label="Activity files waiting"
            value={connection.pendingFileCount}
          />
        </div>
        {(connection.workoutsAllowed || connection.coursesAllowed) && (
          <div className="text-xs text-gray-600">
            Last updated: {formatTimestamp(connection.workoutsSyncTime)}
          </div>
        )}
        {connection.syncMessage !== "" && (
          <p className="rounded-md bg-gray-50 p-2 text-xs text-gray-700">
            {connection.syncMessage}
          </p>
        )}
        <ErrorText error={sync.error ?? disconnect.error} />
        <div className="flex flex-wrap gap-2">
          <button
            type="button"
            className={primaryButton}
            disabled={sync.isPending}
            onClick={() => {
              sync.mutate({});
            }}
          >
            <ArrowPathIcon
              className={`size-4 ${sync.isPending ? "animate-spin" : ""}`}
            />
            {sync.isPending ? "Syncing" : "Sync now"}
          </button>
          {showDisconnect && (
            <ConfirmButton
              title="Disconnect Garmin?"
              message="Upcoming workouts are removed from your Garmin calendar. Synced activities are kept."
              confirmLabel="Disconnect"
              disabled={disconnect.isPending}
              onConfirm={() => {
                disconnect.mutate({});
              }}
            >
              Disconnect
            </ConfirmButton>
          )}
        </div>
      </div>
    </Card>
  );
};

export default GarminCard;
