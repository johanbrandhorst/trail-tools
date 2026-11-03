import { ErrorText } from "@components/ui/query-error";
import { secondaryButton } from "@components/ui/styles";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import {
  ArrowDownTrayIcon,
  ArrowPathIcon,
  CheckCircleIcon,
  PaperAirplaneIcon,
} from "@heroicons/react/24/outline";
import { useQueryClient } from "@tanstack/react-query";
import {
  getGarminConnection,
  getRouteGarminStatus,
  removeRouteFromGarmin,
  sendRouteToGarmin,
} from "gen/garmin/v1/garmin-GarminService_connectquery";
import { GarminService } from "gen/garmin/v1/garmin_pb";
import { invalidateServices } from "helpers/query";
import React from "react";

interface SendRouteProps {
  routeId: string;
}

/**
 * SendRoute puts a route on the user's Garmin account as a course, for their
 * watch to navigate, or removes it again. Without a Garmin connection that
 * allows courses, it offers the course as a file instead, for other watches.
 */
const SendRoute: React.FC<SendRouteProps> = ({ routeId }) => {
  const queryClient = useQueryClient();
  const garmin = useQuery(getGarminConnection);
  const connection = garmin.data?.connection;
  const sends = connection?.connected === true && connection.coursesAllowed;
  const status = useQuery(
    getRouteGarminStatus,
    { routeId },
    { enabled: sends }
  );
  const onSuccess = async (): Promise<void> => {
    await invalidateServices(queryClient, [GarminService]);
  };
  const send = useMutation(sendRouteToGarmin, { onSuccess });
  const remove = useMutation(removeRouteFromGarmin, { onSuccess });

  if (!sends) {
    return (
      <a
        href={`/routes/${routeId}.fit`}
        className={secondaryButton}
        title="A course for Garmin and other watches, with a virtual partner at an easy pace"
        download
      >
        <ArrowDownTrayIcon className="size-4" />
        Course file
      </a>
    );
  }
  const current = status.data?.status;
  const pending = send.isPending || remove.isPending;
  let sendLabel = "Send to Garmin";
  if (send.isPending) {
    sendLabel = "Sending…";
  } else if (current?.onGarmin === true) {
    sendLabel = "On Garmin for a run · Keep it";
  }
  return (
    <>
      {current?.sent === true ? (
        <button
          type="button"
          className={secondaryButton}
          disabled={pending}
          title="The course stays on your Garmin account, and your watch, until you remove it"
          onClick={() => {
            remove.mutate({ routeId });
          }}
        >
          <CheckCircleIcon className="size-4 text-emerald-600" />
          {remove.isPending ? "Removing…" : "On Garmin · Remove"}
        </button>
      ) : (
        <button
          type="button"
          className={secondaryButton}
          disabled={pending || status.isPending}
          title={
            current?.onGarmin === true
              ? "On Garmin for an upcoming run; send it to keep it there after the run"
              : "Put the route on your Garmin account as a course, for your watch to navigate"
          }
          onClick={() => {
            send.mutate({ routeId });
          }}
        >
          {send.isPending ? (
            <ArrowPathIcon className="size-4 animate-spin" />
          ) : (
            <PaperAirplaneIcon className="size-4" />
          )}
          {sendLabel}
        </button>
      )}
      <ErrorText error={send.error ?? remove.error} />
    </>
  );
};

export default SendRoute;
