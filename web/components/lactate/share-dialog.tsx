import Loading from "@components/loading";
import { CopyField } from "@components/ui/misc";
import Modal from "@components/ui/modal";
import QrCode from "@components/ui/qr-code";
import { ErrorText } from "@components/ui/query-error";
import {
  ghostDangerButton,
  linkClass,
  primaryButton,
  secondaryButton,
} from "@components/ui/styles";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import {
  createLactateTestShare,
  deleteLactateTestShare,
  listLactateTestShares,
} from "gen/lactate/v1/lactate-LactateTestService_connectquery";
import { LactateTestService } from "gen/lactate/v1/lactate_pb";
import { formatTimestamp } from "helpers/format";
import { invalidateServices } from "helpers/query";
import React from "react";
import { Link } from "react-router";

interface ShareDialogProps {
  // The workout the test is for, to link the recorded test to it, if any.
  workoutId?: string;
  onClose: () => void;
}

/**
 * ShareDialog creates links that let someone else record a lactate test for
 * the user, like a friend holding the lactate meter, and lists the links
 * that still work.
 */
const ShareDialog: React.FC<ShareDialogProps> = ({
  workoutId = "",
  onClose,
}) => {
  const queryClient = useQueryClient();
  const shares = useQuery(listLactateTestShares, {});
  const createShare = useMutation(createLactateTestShare, {
    onSuccess: async () => {
      await invalidateServices(queryClient, [LactateTestService]);
    },
  });
  const deleteShare = useMutation(deleteLactateTestShare, {
    onSuccess: async () => {
      await invalidateServices(queryClient, [LactateTestService]);
    },
  });
  const link = createShare.data?.share?.url ?? "";

  return (
    <Modal open onClose={onClose} title="Let someone record a test">
      <div className="flex flex-col gap-4 text-sm text-gray-700">
        <p>
          Send a link to whoever holds the lactate meter, so they can run the
          guided test on their phone, without signing in. It works for 24 hours,
          in the first browser that opens it, and the test is saved to your
          lactate tests.
        </p>
        {link === "" ? (
          <button
            type="button"
            className={`${primaryButton} self-start`}
            disabled={createShare.isPending}
            onClick={() => {
              createShare.mutate({ workoutId });
            }}
          >
            {createShare.isPending ? "Creating…" : "Create a link"}
          </button>
        ) : (
          <div className="flex flex-col items-start gap-3">
            <CopyField value={link} />
            <QrCode value={link} />
            <p className="text-xs text-gray-500">
              Scan the code with their phone, or send them the link.
            </p>
          </div>
        )}
        <ErrorText error={createShare.error ?? deleteShare.error} />
        <div>
          <h3 className="font-semibold text-gray-900">Links that still work</h3>
          {shares.isPending && <Loading />}
          {shares.data !== undefined && shares.data.shares.length === 0 && (
            <p className="mt-1 text-gray-500">None.</p>
          )}
          <ul className="mt-1 flex flex-col divide-y divide-gray-100">
            {shares.data?.shares.map((share) => (
              <li
                key={share.id}
                className="flex flex-wrap items-center justify-between gap-2 py-2"
              >
                <span>
                  {share.opened ? "Opened" : "Not opened yet"} · until{" "}
                  {formatTimestamp(share.expireTime)}
                  {share.testId !== "" && (
                    <>
                      {" · "}
                      <Link
                        to={`/lactate-tests/${share.testId}`}
                        className={linkClass}
                        onClick={onClose}
                      >
                        The test
                      </Link>
                    </>
                  )}
                </span>
                <button
                  type="button"
                  className={ghostDangerButton}
                  disabled={deleteShare.isPending}
                  onClick={() => {
                    deleteShare.mutate({ id: share.id });
                  }}
                >
                  Delete
                </button>
              </li>
            ))}
          </ul>
        </div>
        <div className="flex justify-end">
          <button type="button" className={secondaryButton} onClick={onClose}>
            Done
          </button>
        </div>
      </div>
    </Modal>
  );
};

export default ShareDialog;
