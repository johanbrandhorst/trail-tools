import Card from "@components/ui/card";
import { CopyField } from "@components/ui/misc";
import { ErrorText } from "@components/ui/query-error";
import { linkClass, secondaryButton } from "@components/ui/styles";
import { useMutation } from "@connectrpc/connect-query";
import { ShareIcon } from "@heroicons/react/24/outline";
import { useQueryClient } from "@tanstack/react-query";
import type { Workout } from "gen/plans/v1/plans_pb";
import { TrainingPlanService } from "gen/plans/v1/plans_pb";
import {
  shareWorkout,
  unshareWorkout,
} from "gen/plans/v1/shares-WorkoutShareService_connectquery";
import { invalidateServices } from "helpers/query";
import React from "react";

/**
 * WorkoutShareCard shares a workout by link, for a friend to view it and
 * download its route and files without an account.
 */
const WorkoutShareCard: React.FC<{ workout: Workout }> = ({ workout }) => {
  const queryClient = useQueryClient();
  const onSuccess = async () => {
    await invalidateServices(queryClient, [TrainingPlanService]);
  };
  const share = useMutation(shareWorkout, { onSuccess });
  const unshare = useMutation(unshareWorkout, { onSuccess });
  const shareUrl =
    workout.shareUrl === "" ? (share.data?.shareUrl ?? "") : workout.shareUrl;

  return (
    <Card title="Share">
      <div className="flex flex-col gap-2">
        <p className="text-sm text-gray-600">
          Anyone with the link can view the workout, its route and its
          exercises, and download them for their watch, without an account. They
          don&apos;t see your training plan or your log.
        </p>
        {shareUrl === "" ? (
          <button
            type="button"
            className={`${secondaryButton} self-start`}
            disabled={share.isPending}
            onClick={() => {
              share.mutate({ id: workout.id });
            }}
          >
            <ShareIcon className="size-4" />
            Share with a friend
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
                  unshare.mutate({ id: workout.id });
                }}
              >
                Stop sharing
              </button>
            </div>
          </>
        )}
        <ErrorText error={share.error ?? unshare.error} />
      </div>
    </Card>
  );
};

export default WorkoutShareCard;
