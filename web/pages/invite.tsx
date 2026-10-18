import Loading from "@components/loading";
import { TextField } from "@components/ui/fields";
import { cardClass, primaryButton } from "@components/ui/styles";
import { Code, createClient } from "@connectrpc/connect";
import { skipToken, useQuery, useTransport } from "@connectrpc/connect-query";
import { KeyIcon } from "@heroicons/react/24/outline";
import { useQueryClient } from "@tanstack/react-query";
import { getInvitation } from "gen/auth/v1/auth-AuthService_connectquery";
import type { Invitation } from "gen/auth/v1/auth_pb";
import { AuthService, InvitationKind } from "gen/auth/v1/auth_pb";
import { UserService } from "gen/users/v1/users_pb";
import { useFragmentToken } from "helpers/hooks";
import {
  errorMessage,
  isCancelled,
  passkeysSupported,
  registerPasskey,
} from "helpers/passkeys";
import { invalidateServices } from "helpers/query";
import type { ReactNode } from "react";
import React, { useMemo, useState } from "react";
import { useNavigate } from "react-router";

interface InviteFrameProps {
  title: string;
  children: ReactNode;
}

const InviteFrame: React.FC<InviteFrameProps> = ({ title, children }) => (
  <div className="flex justify-center py-8">
    <div className={`${cardClass} flex w-full max-w-md flex-col gap-4 p-6`}>
      <h1 className="text-xl font-bold text-gray-900">{title}</h1>
      {children}
    </div>
  </div>
);

const inviteTitle = (invitation: Invitation): string => {
  if (invitation.kind === InvitationKind.ADD_PASSKEY) {
    return `Add a passkey for ${invitation.userName} on this device`;
  }
  if (invitation.bootstrap) {
    return "Create the administrator account";
  }
  return `${invitation.createdByName} invited you to trailrunn.ing`;
};

interface InviteFormProps {
  token: string;
  invitation: Invitation;
}

const InviteForm: React.FC<InviteFormProps> = ({ token, invitation }) => {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const transport = useTransport();
  const client = useMemo(
    () => createClient(AuthService, transport),
    [transport]
  );
  const newUser = invitation.kind !== InvitationKind.ADD_PASSKEY;
  const [name, setName] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");

  const register = () => {
    if (newUser && name.trim() === "") {
      setError("Enter your name.");
      return;
    }
    setPending(true);
    setError("");
    registerPasskey(client, token, name.trim())
      .then(async () => {
        await invalidateServices(queryClient, [UserService, AuthService]);
        void navigate("/");
      })
      .catch((error_: unknown) => {
        if (!isCancelled(error_)) {
          setError(errorMessage(error_));
        }
      })
      .finally(() => {
        setPending(false);
      });
  };

  return (
    <InviteFrame title={inviteTitle(invitation)}>
      {invitation.note !== "" && newUser && (
        <p className="rounded-md bg-gray-50 p-3 text-sm whitespace-pre-line text-gray-700">
          {invitation.note}
        </p>
      )}
      <p className="text-sm text-gray-600">
        {newUser
          ? "trailrunn.ing signs you in with a passkey instead of a password: your device's fingerprint, face or screen lock."
          : "Create a passkey on this device to sign in here with your fingerprint, face or screen lock."}
      </p>
      {passkeysSupported() ? (
        <form
          className="flex flex-col gap-3"
          onSubmit={(event) => {
            event.preventDefault();
            register();
          }}
        >
          {newUser && (
            <TextField
              label="Your name"
              value={name}
              onChange={setName}
              placeholder="e.g. Anna Svensson"
              autoFocus
              required
            />
          )}
          {error !== "" && <p className="text-sm text-red-700">{error}</p>}
          <button
            type="submit"
            className={`${primaryButton} py-2.5 text-base`}
            disabled={pending}
          >
            <KeyIcon className="size-5" />
            {pending ? "Waiting for passkey…" : "Create passkey"}
          </button>
        </form>
      ) : (
        <p className="text-sm text-red-700">
          This browser doesn&apos;t support passkeys. Open the link in an
          up-to-date browser.
        </p>
      )}
    </InviteFrame>
  );
};

// InvitePage lets someone with an invitation link register a passkey.
const InvitePage: React.FC = () => {
  const token = useFragmentToken();
  const { isPending, isError, data, error } = useQuery(
    getInvitation,
    token === "" ? skipToken : { token },
    { retry: false }
  );
  if (token === "") {
    return (
      <InviteFrame title="Invitation not found">
        <p className="text-sm text-gray-600">
          The link is incomplete. Open the whole link you were sent, or ask for
          a new one.
        </p>
      </InviteFrame>
    );
  }
  if (isPending) {
    return <Loading />;
  }
  if (isError) {
    const expected =
      error.code === Code.FailedPrecondition || error.code === Code.NotFound;
    return (
      <InviteFrame title="This invitation can't be used">
        <p className="text-sm text-gray-700">
          {expected
            ? error.rawMessage
            : `Something went wrong: ${error.rawMessage}`}
        </p>
        <p className="text-sm text-gray-500">
          Ask the person who invited you for a new link.
        </p>
      </InviteFrame>
    );
  }
  if (data.invitation === undefined) {
    return (
      <InviteFrame title="Invitation not found">
        <p className="text-sm text-gray-600">Ask for a new link.</p>
      </InviteFrame>
    );
  }
  return <InviteForm token={token} invitation={data.invitation} />;
};

export default InvitePage;
