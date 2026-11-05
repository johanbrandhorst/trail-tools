import Loading from "@components/loading";
import Card from "@components/ui/card";
import ConfirmButton from "@components/ui/confirm-button";
import { CopyField } from "@components/ui/misc";
import QueryError, { ErrorText } from "@components/ui/query-error";
import { primaryButton } from "@components/ui/styles";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { CalendarDaysIcon } from "@heroicons/react/24/outline";
import { useQueryClient } from "@tanstack/react-query";
import {
  createCalendarFeed,
  deleteCalendarFeed,
  getCalendarFeed,
} from "gen/plans/v1/shares-WorkoutShareService_connectquery";
import { WorkoutShareService } from "gen/plans/v1/shares_pb";
import { invalidateServices } from "helpers/query";
import React from "react";

/**
 * CalendarFeedCard manages the calendar feed of upcoming workouts, which
 * calendar apps and home automation subscribe to by its secret URL.
 */
const CalendarFeedCard: React.FC = () => {
  const queryClient = useQueryClient();
  const { isPending, isError, data, error, refetch } =
    useQuery(getCalendarFeed);
  const onSuccess = async () => {
    await invalidateServices(queryClient, [WorkoutShareService]);
  };
  const create = useMutation(createCalendarFeed, { onSuccess });
  const remove = useMutation(deleteCalendarFeed, { onSuccess });

  let content: React.ReactNode = <Loading />;
  if (isError) {
    content = (
      <QueryError
        error={error}
        onRetry={() => {
          void refetch();
        }}
      />
    );
  } else if (!isPending) {
    const { feed } = data;
    content =
      feed === undefined ? (
        <button
          type="button"
          className={`${primaryButton} self-start`}
          disabled={create.isPending}
          onClick={() => {
            create.mutate({});
          }}
        >
          <CalendarDaysIcon className="size-4" />
          Create calendar feed
        </button>
      ) : (
        <>
          <CopyField value={feed.url} />
          <p className="text-xs text-gray-600">
            If your app supports a user name and password, this URL keeps the
            secret out of server logs:
          </p>
          <CopyField value={feed.basicAuthUrl} />
          <div className="flex flex-wrap gap-3 text-sm">
            <ConfirmButton
              title="Replace the calendar feed?"
              message="The current URL stops working, so update it in every app that uses it."
              confirmLabel="Replace"
              disabled={create.isPending}
              onConfirm={() => {
                create.mutate({});
              }}
            >
              Replace URL
            </ConfirmButton>
            <ConfirmButton
              title="Delete the calendar feed?"
              message="Apps subscribed to it stop getting your workouts."
              confirmLabel="Delete"
              disabled={remove.isPending}
              onConfirm={() => {
                remove.mutate({});
              }}
            >
              Delete
            </ConfirmButton>
          </div>
        </>
      );
  }

  return (
    <Card title="Calendar feed">
      <div className="flex flex-col gap-3">
        <p className="text-sm text-gray-600">
          Subscribe to your upcoming workouts in a calendar app, or in Home
          Assistant to warm the house for when you&apos;re back. Workouts with a
          start time and a target duration are listed for the next four weeks.
          Anyone with the URL can see them, so keep it private.
        </p>
        {content}
        <ErrorText error={create.error ?? remove.error} />
      </div>
    </Card>
  );
};

export default CalendarFeedCard;
