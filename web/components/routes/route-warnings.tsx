import { ExclamationTriangleIcon } from "@heroicons/react/24/outline";
import type { RouteWarning } from "gen/routes/v1/routes_pb";
import React from "react";

// MAX_SHOWN is how many warnings are listed, to keep the map visible.
const MAX_SHOWN = 3;

interface RouteWarningsProps {
  warnings: RouteWarning[];
}

/**
 * RouteWarnings lists what to check about a route being edited: sections
 * that retrace ground it already covered, and waypoints off any mapped
 * path.
 */
const RouteWarnings: React.FC<RouteWarningsProps> = ({ warnings }) => (
  <div className="pointer-events-auto flex max-w-md flex-col gap-0.5 rounded-md bg-amber-50 px-2 py-1 text-xs text-amber-900 shadow ring-1 ring-amber-200">
    {warnings.slice(0, MAX_SHOWN).map((warning) => (
      <p
        key={`${warning.kind}-${warning.startDistanceMeters}`}
        className="flex items-start gap-1"
      >
        <ExclamationTriangleIcon className="mt-px size-3.5 shrink-0" />
        <span>{warning.message}</span>
      </p>
    ))}
    {warnings.length > MAX_SHOWN && (
      <p className="pl-4.5">And {warnings.length - MAX_SHOWN} more.</p>
    )}
  </div>
);

export default RouteWarnings;
