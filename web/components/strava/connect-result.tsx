import React, { useEffect, useState } from "react";
import { useSearchParams } from "react-router";

export interface ConnectResult {
  connected: boolean;
  message: string;
}

/**
 * UseConnectResult returns the result of connecting a service, which its
 * OAuth callback passes to the settings page in the service's query
 * parameter, "strava" or "garmin", and "message". It removes them from the
 * URL, so reloading the page doesn't show the result again.
 */
export const useConnectResult = (param: string): ConnectResult | null => {
  const [searchParams, setSearchParams] = useSearchParams();
  const [result] = useState<ConnectResult | null>(() => {
    const status = searchParams.get(param);
    if (status === null) {
      return null;
    }
    return {
      connected: status === "connected",
      message: searchParams.get("message") ?? "",
    };
  });
  useEffect(() => {
    if (searchParams.has(param)) {
      setSearchParams(
        (params) => {
          params.delete(param);
          params.delete("message");
          return params;
        },
        { replace: true }
      );
    }
  }, [param, searchParams, setSearchParams]);
  return result;
};

interface ConnectResultNoticeProps {
  result: ConnectResult | null;
  // The service's name, like "Strava".
  service: string;
  // What happens once connected.
  connectedText: string;
}

// ConnectResultNotice shows whether connecting a service worked.
export const ConnectResultNotice: React.FC<ConnectResultNoticeProps> = ({
  result,
  service,
  connectedText,
}) => {
  if (result === null) {
    return null;
  }
  if (result.connected) {
    return (
      <p className="rounded-md bg-emerald-50 p-2 text-sm text-emerald-900">
        {service} is connected. {connectedText}
      </p>
    );
  }
  return (
    <p className="rounded-md bg-red-50 p-2 text-sm text-red-800">
      {result.message === "" ? `Failed to connect ${service}.` : result.message}
    </p>
  );
};
