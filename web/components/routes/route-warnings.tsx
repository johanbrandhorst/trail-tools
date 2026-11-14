import { ExclamationTriangleIcon } from "@heroicons/react/24/outline";
import type { RouteWarning } from "gen/routes/v1/routes_pb";
import React from "react";

interface RouteWarningsProps {
  warnings: RouteWarning[];
}

/**
 * RouteWarnings lists what to check about a route being edited: sections
 * that retrace ground it already covered, and waypoints off any mapped
 * path. It lists them all, as it sits below the map, not over it.
 */
const RouteWarnings: React.FC<RouteWarningsProps> = ({ warnings }) => (
  <div className="flex flex-col gap-1 rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-900 ring-1 ring-amber-200">
    {warnings.map((warning) => (
      <p
        key={`${warning.kind}-${warning.startDistanceMeters}`}
        className="flex items-start gap-1.5"
      >
        <ExclamationTriangleIcon className="mt-0.5 size-4 shrink-0" />
        <span>{warning.message}</span>
      </p>
    ))}
  </div>
);

export default RouteWarnings;
