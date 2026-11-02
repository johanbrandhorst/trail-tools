import Loading from "@components/loading";
import { ErrorText } from "@components/ui/query-error";
import {
  cardClass,
  primaryButton,
  secondaryButton,
} from "@components/ui/styles";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { ExclamationTriangleIcon } from "@heroicons/react/24/outline";
import {
  approveAuthorization,
  denyAuthorization,
  getAuthorization,
} from "gen/oauth/v1/oauth-OAuthService_connectquery";
import { formatRedirectTarget } from "helpers/format";
import React, { useState } from "react";
import { useLocation } from "react-router";

/**
 * OAuthConsentPage asks the user to approve an OAuth client that wants to use
 * trailrunn.ing on their behalf, such as a custom connector of claude.ai.
 */
// The server validates the authorization request before serving this page.
const OAuthConsentPage: React.FC = () => {
  const query = useLocation().search.replace(/^\?/u, "");
  const [redirecting, setRedirecting] = useState(false);
  const { isPending, isError, data, error } = useQuery(
    getAuthorization,
    { query },
    { retry: false }
  );
  const onDone = (response: { redirectUri: string }) => {
    setRedirecting(true);
    globalThis.location.assign(response.redirectUri);
  };
  const approve = useMutation(approveAuthorization, { onSuccess: onDone });
  const deny = useMutation(denyAuthorization, { onSuccess: onDone });

  let content: React.ReactNode = null;
  if (isPending) {
    content = <Loading />;
  } else if (isError) {
    content = (
      <>
        <h1 className="text-xl font-bold text-gray-900">
          This connection request can&apos;t be used
        </h1>
        <p className="text-sm text-gray-700">{error.rawMessage}</p>
        <p className="text-sm text-gray-500">
          Go back to the app you were connecting and try again.
        </p>
      </>
    );
  } else {
    const pending = approve.isPending || deny.isPending || redirecting;
    content = (
      <>
        <h1 className="text-xl font-bold text-gray-900">
          Connect {data.clientName} to trailrunn.ing?
        </h1>
        <p className="text-sm text-gray-700">
          <span className="font-semibold">{data.clientName}</span> will be able
          to see and change your routes, training plans, races, race plans and
          activities on your behalf, including activities from Garmin and
          Strava, until you disconnect it in Settings. Connecting it is your
          consent to that; disconnecting withdraws it.
        </p>
        <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 rounded-md bg-gray-50 p-3 text-sm">
          <dt className="text-gray-500">App</dt>
          <dd className="font-medium break-all text-gray-900">
            {data.clientName}
          </dd>
          <dt className="text-gray-500">Returns to</dt>
          <dd className="font-medium break-all text-gray-900">
            {formatRedirectTarget(data.redirectUri)}
          </dd>
        </dl>
        <p className="flex gap-2 text-xs text-gray-500">
          <ExclamationTriangleIcon className="size-4 shrink-0" />
          The app chooses its own name. Only connect it if you just started
          connecting it yourself.
        </p>
        <ErrorText error={approve.error ?? deny.error} />
        {redirecting && (
          <p className="text-sm text-gray-600">
            Returning to {formatRedirectTarget(data.redirectUri)}…
          </p>
        )}
        <div className="flex flex-wrap justify-end gap-2">
          <button
            type="button"
            className={secondaryButton}
            disabled={pending}
            onClick={() => {
              deny.mutate({ query });
            }}
          >
            Cancel
          </button>
          <button
            type="button"
            className={primaryButton}
            disabled={pending}
            onClick={() => {
              approve.mutate({ query });
            }}
          >
            Connect
          </button>
        </div>
      </>
    );
  }

  return (
    <div className="flex justify-center py-8">
      <div className={`${cardClass} flex w-full max-w-md flex-col gap-4 p-6`}>
        {content}
      </div>
    </div>
  );
};

export default OAuthConsentPage;
