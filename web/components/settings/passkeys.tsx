import Loading from "@components/loading";
import Card from "@components/ui/card";
import ConfirmButton from "@components/ui/confirm-button";
import { Badge } from "@components/ui/misc";
import QueryError, { ErrorText } from "@components/ui/query-error";
import {
  ghostDangerButton,
  primaryButton,
  secondaryButton,
} from "@components/ui/styles";
import { createClient } from "@connectrpc/connect";
import { useMutation, useQuery, useTransport } from "@connectrpc/connect-query";
import { TrashIcon } from "@heroicons/react/24/outline";
import { useQueryClient } from "@tanstack/react-query";
import {
  createInvitation,
  deletePasskey,
  listPasskeys,
  signOutOtherSessions,
} from "gen/auth/v1/auth-AuthService_connectquery";
import { AuthService, InvitationKind } from "gen/auth/v1/auth_pb";
import { formatTimestamp } from "helpers/format";
import {
  errorMessage,
  isCancelled,
  passkeysSupported,
  registerPasskey,
} from "helpers/passkeys";
import { invalidateServices } from "helpers/query";
import React, { useMemo, useState } from "react";

import InvitationLink from "./invitation-link";

// Passkeys lists the user's passkeys and adds new ones.
const Passkeys: React.FC = () => {
  const queryClient = useQueryClient();
  const transport = useTransport();
  const client = useMemo(
    () => createClient(AuthService, transport),
    [transport]
  );
  const { isPending, isError, data, error, refetch } = useQuery(listPasskeys);
  const [adding, setAdding] = useState(false);
  const [addError, setAddError] = useState("");
  const [inviteUrl, setInviteUrl] = useState("");
  const [signedOut, setSignedOut] = useState<number | undefined>(undefined);
  const onChanged = async () => {
    await invalidateServices(queryClient, [AuthService]);
  };
  const remove = useMutation(deletePasskey, { onSuccess: onChanged });
  const signOutOthers = useMutation(signOutOtherSessions, {
    onSuccess: (response) => {
      setSignedOut(response.signedOutSessions);
    },
  });
  const invite = useMutation(createInvitation, {
    onSuccess: async (response) => {
      setInviteUrl(response.url);
      await onChanged();
    },
  });

  const addHere = () => {
    setAdding(true);
    setAddError("");
    registerPasskey(client, "", "")
      .then(onChanged)
      .catch((error_: unknown) => {
        if (!isCancelled(error_)) {
          setAddError(errorMessage(error_));
        }
      })
      .finally(() => {
        setAdding(false);
      });
  };

  let list: React.ReactNode = null;
  if (isPending) {
    list = <Loading />;
  } else if (isError) {
    list = (
      <QueryError
        error={error}
        onRetry={() => {
          void refetch();
        }}
      />
    );
  } else if (data.passkeys.length === 0) {
    list = <p className="text-sm text-gray-500">No passkeys yet.</p>;
  } else {
    const onlyOne = data.passkeys.length === 1;
    list = (
      <ul className="divide-y divide-gray-100">
        {data.passkeys.map((passkey) => (
          <li
            key={passkey.id}
            className="flex items-center justify-between gap-2 py-2"
          >
            <div className="min-w-0">
              <p className="flex flex-wrap items-center gap-2 text-sm font-medium text-gray-900">
                {passkey.name === "" ? "Passkey" : passkey.name}
                {passkey.backedUp && (
                  <Badge className="bg-sky-50 text-sky-800 ring-sky-200">
                    Synced
                  </Badge>
                )}
              </p>
              <p className="text-xs text-gray-500">
                Added {formatTimestamp(passkey.createTime)} · last used{" "}
                {formatTimestamp(passkey.lastUsedTime)}
              </p>
            </div>
            <ConfirmButton
              className={ghostDangerButton}
              title="Delete passkey?"
              message="You won't be able to sign in with this passkey any more, and other devices signed in with it will be signed out."
              disabled={onlyOne || remove.isPending}
              onConfirm={() => {
                remove.mutate({ id: passkey.id });
              }}
            >
              <TrashIcon
                className="size-4"
                aria-label={
                  onlyOne
                    ? "Your only passkey can't be deleted"
                    : "Delete passkey"
                }
              />
            </ConfirmButton>
          </li>
        ))}
      </ul>
    );
  }

  return (
    <Card title="Passkeys">
      <div className="flex flex-col gap-4">
        <p className="text-sm text-gray-600">
          Passkeys sign you in with your device&apos;s fingerprint, face or
          screen lock. Add one on each device you use, or use a synced passkey.
        </p>
        {list}
        <ErrorText
          error={remove.error ?? invite.error ?? signOutOthers.error}
        />
        {addError !== "" && <p className="text-sm text-red-700">{addError}</p>}
        {inviteUrl !== "" && (
          <InvitationLink
            url={inviteUrl}
            description="Open this link on the other device to add a passkey for your account there."
            onDone={() => {
              setInviteUrl("");
            }}
          />
        )}
        <div className="flex flex-wrap gap-2">
          {passkeysSupported() && (
            <button
              type="button"
              className={primaryButton}
              disabled={adding}
              onClick={addHere}
            >
              {adding ? "Waiting for passkey…" : "Add a passkey on this device"}
            </button>
          )}
          <button
            type="button"
            className={secondaryButton}
            disabled={invite.isPending}
            onClick={() => {
              invite.mutate({ kind: InvitationKind.ADD_PASSKEY, note: "" });
            }}
          >
            Add a passkey on another device
          </button>
          <ConfirmButton
            className={secondaryButton}
            title="Sign out other devices?"
            confirmLabel="Sign out"
            message="Every other device and browser signed in to your account will be signed out. This one stays signed in."
            disabled={signOutOthers.isPending}
            onConfirm={() => {
              setSignedOut(undefined);
              signOutOthers.mutate({});
            }}
          >
            Sign out other devices
          </ConfirmButton>
        </div>
        {signedOut !== undefined && (
          <p className="text-sm text-gray-600">
            {signedOut === 0
              ? "No other devices were signed in."
              : `Signed out ${signedOut.toString()} other ${signedOut === 1 ? "session" : "sessions"}.`}
          </p>
        )}
      </div>
    </Card>
  );
};

export default Passkeys;
