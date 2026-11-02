import GarminCard from "@components/garmin/garmin-card";
import Loading from "@components/loading";
import AvatarCard from "@components/settings/avatar-card";
import Connections from "@components/settings/connections";
import Invitations from "@components/settings/invitations";
import Passkeys from "@components/settings/passkeys";
import PrivacyZonesCard from "@components/settings/privacy-zones";
import PublicProfileCard from "@components/settings/public-profile";
import StravaCard from "@components/strava/strava-card";
import Card from "@components/ui/card";
import ConfirmButton from "@components/ui/confirm-button";
import { CopyField, PageHeader } from "@components/ui/misc";
import QueryError, { ErrorText } from "@components/ui/query-error";
import {
  ghostButton,
  ghostDangerButton,
  inputClass,
  primaryButton,
} from "@components/ui/styles";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import {
  ExclamationTriangleIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";
import { useQueryClient } from "@tanstack/react-query";
import {
  createAPIToken,
  deleteAPIToken,
  listAPITokens,
} from "gen/users/v1/users-UserService_connectquery";
import type { User } from "gen/users/v1/users_pb";
import { UserService } from "gen/users/v1/users_pb";
import { formatTimestamp } from "helpers/format";
import { invalidateServices } from "helpers/query";
import React, { useState } from "react";

interface NewToken {
  name: string;
  secret: string;
}

const ApiTokens: React.FC = () => {
  const queryClient = useQueryClient();
  const { isPending, isError, data, error, refetch } = useQuery(listAPITokens);
  const [name, setName] = useState("");
  const [created, setCreated] = useState<NewToken | null>(null);
  const create = useMutation(createAPIToken, {
    onSuccess: async (response) => {
      setCreated({ name: response.token?.name ?? "", secret: response.secret });
      setName("");
      await invalidateServices(queryClient, [UserService]);
    },
  });
  const remove = useMutation(deleteAPIToken, {
    onSuccess: async () => {
      await invalidateServices(queryClient, [UserService]);
    },
  });

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
  } else if (data.tokens.length === 0) {
    list = <p className="text-sm text-gray-500">No API tokens yet.</p>;
  } else {
    list = (
      <ul className="divide-y divide-gray-100">
        {data.tokens.map((token) => (
          <li
            key={token.id}
            className="flex items-center justify-between gap-2 py-2"
          >
            <div className="min-w-0">
              <p className="truncate text-sm font-medium text-gray-900">
                {token.name}
              </p>
              <p className="text-xs text-gray-500">
                Created {formatTimestamp(token.createTime)} · last used{" "}
                {formatTimestamp(token.lastUsedTime)}
              </p>
            </div>
            <ConfirmButton
              className={ghostDangerButton}
              title="Delete API token?"
              message={`Clients using "${token.name}" will stop working.`}
              disabled={remove.isPending}
              onConfirm={() => {
                remove.mutate({ id: token.id });
              }}
            >
              <TrashIcon className="size-4" aria-label="Delete token" />
            </ConfirmButton>
          </li>
        ))}
      </ul>
    );
  }

  return (
    <Card title="API tokens">
      <div className="flex flex-col gap-4">
        <p className="text-sm text-gray-600">
          API tokens let AI agents and other clients use trailrunn.ing on your
          behalf with an{" "}
          <code className="rounded bg-gray-100 px-1">
            Authorization: Bearer &lt;token&gt;
          </code>{" "}
          header.
        </p>
        {created !== null && (
          <div className="flex flex-col gap-2 rounded-md border border-amber-300 bg-amber-50 p-3">
            <p className="flex items-center gap-2 text-sm font-semibold text-amber-900">
              <ExclamationTriangleIcon className="size-5" />
              Copy the token &quot;{created.name}&quot; now. It is not stored
              and will not be shown again.
            </p>
            <CopyField value={created.secret} />
            <button
              type="button"
              className={`${ghostButton} self-start`}
              onClick={() => {
                setCreated(null);
              }}
            >
              I have copied it
            </button>
          </div>
        )}
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            if (name.trim() !== "") {
              create.mutate({ name: name.trim() });
            }
          }}
        >
          <label className="flex min-w-48 grow flex-col gap-1">
            <span className="text-xs font-medium text-gray-600">
              Token name
            </span>
            <input
              value={name}
              className={inputClass}
              placeholder="e.g. Claude Code on my laptop"
              onChange={(event) => {
                setName(event.target.value);
              }}
            />
          </label>
          <button
            type="submit"
            className={primaryButton}
            disabled={name.trim() === "" || create.isPending}
          >
            Create token
          </button>
        </form>
        <ErrorText error={create.error ?? remove.error} />
        {list}
      </div>
    </Card>
  );
};

const McpInstructions: React.FC = () => {
  const endpoint = `${globalThis.location.origin}/mcp`;
  const command = `claude mcp add --transport http trail-tools ${endpoint} --header "Authorization: Bearer <token>"`;
  const json = JSON.stringify(
    {
      mcpServers: {
        "trail-tools": {
          headers: { Authorization: "Bearer <token>" },
          type: "http",
          url: endpoint,
        },
      },
    },
    null,
    2
  );
  return (
    <Card title="Connect your AI agent (MCP)">
      <div className="flex flex-col gap-4 text-sm text-gray-700">
        <p>
          trailrunn.ing is also an{" "}
          <a
            href="https://modelcontextprotocol.io"
            target="_blank"
            rel="noreferrer"
            className="font-medium text-brand-600 hover:underline"
          >
            MCP server
          </a>
          . Connect an AI agent to analyse your training, build training plans,
          design routes and work through your race plans with you.
        </p>
        <div className="flex flex-col gap-1">
          <span className="text-xs font-medium text-gray-600 uppercase">
            Endpoint (streamable HTTP)
          </span>
          <CopyField value={endpoint} />
        </div>
        <div className="flex flex-col gap-1">
          <span className="text-xs font-medium text-gray-600 uppercase">
            claude.ai, Claude Desktop and other OAuth clients
          </span>
          <p>
            Add a custom connector with the endpoint above. You&apos;ll be asked
            to sign in to trailrunn.ing and approve the connection, and the app
            shows up under connected apps.
          </p>
        </div>
        <p>
          Clients can also use an API token instead, which is the simplest
          option for Claude Code: create one above and use it in place of{" "}
          <code className="rounded bg-gray-100 px-1">&lt;token&gt;</code>.
        </p>
        <div className="flex flex-col gap-1">
          <span className="text-xs font-medium text-gray-600 uppercase">
            Claude Code
          </span>
          <CopyField value={command} multiline />
        </div>
        <div className="flex flex-col gap-1">
          <span className="text-xs font-medium text-gray-600 uppercase">
            Other MCP clients
          </span>
          <p className="text-xs text-gray-500">
            Most clients accept a configuration like this for a remote server
            using the streamable HTTP transport.
          </p>
          <CopyField value={json} multiline />
        </div>
      </div>
    </Card>
  );
};

interface SettingsPageProps {
  user: User;
}

const SettingsPage: React.FC<SettingsPageProps> = ({ user }) => (
  <div>
    <PageHeader title="Settings" />
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
      <div className="flex flex-col gap-4">
        <AvatarCard user={user} />
        <Passkeys />
        <Invitations admin={user.admin} />
        <StravaCard showDisconnect />
        <GarminCard />
      </div>
      <div className="flex flex-col gap-4">
        <PublicProfileCard />
        <PrivacyZonesCard />
        <ApiTokens />
        <Connections />
        <McpInstructions />
      </div>
    </div>
  </div>
);

export default SettingsPage;
