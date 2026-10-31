import {
  ArrowPathRoundedSquareIcon,
  ArrowUturnLeftIcon,
  ArrowUturnRightIcon,
  ArrowsRightLeftIcon,
  ArrowsUpDownIcon,
  TrashIcon,
  XMarkIcon,
} from "@heroicons/react/24/outline";
import type { RouteStats, RoutingProfile } from "gen/routes/v1/routes_pb";
import { formatKm, formatMeters } from "helpers/format";
import { PROFILE_OPTIONS } from "helpers/labels";
import type { ReactNode } from "react";
import React from "react";

// The platform's modifier key, for keyboard shortcut hints.
const MODIFIER = /Mac|iPhone|iPad/u.test(globalThis.navigator.userAgent)
  ? "⌘"
  : "Ctrl+";

interface ToolButtonProps {
  title: string;
  disabled?: boolean;
  onClick: () => void;
  children: ReactNode;
}

const ToolButton: React.FC<ToolButtonProps> = ({
  title,
  disabled = false,
  onClick,
  children,
}) => (
  <button
    type="button"
    className="inline-flex items-center gap-1 rounded px-1.5 py-1 text-xs font-medium text-gray-700 hover:bg-gray-100 hover:text-gray-900 disabled:cursor-not-allowed disabled:opacity-40"
    title={title}
    aria-label={title}
    disabled={disabled}
    onClick={onClick}
  >
    {children}
  </button>
);

const Divider: React.FC = () => (
  <span className="mx-0.5 h-5 w-px self-center bg-gray-200" />
);

interface EditorToolbarProps {
  mode: RoutingProfile;
  onModeChange: (mode: RoutingProfile) => void;
  canUndo: boolean;
  canRedo: boolean;
  waypointCount: number;
  isLoop: boolean;
  selected: number | null;
  onUndo: () => void;
  onRedo: () => void;
  onReturnToStart: () => void;
  onOutAndBack: () => void;
  onReverse: () => void;
  onClear: () => void;
  onRemoveSelected: () => void;
}

/**
 * EditorToolbar floats over the route editor's map with the routing mode
 * and editing actions.
 */
export const EditorToolbar: React.FC<EditorToolbarProps> = ({
  mode,
  onModeChange,
  canUndo,
  canRedo,
  waypointCount,
  isLoop,
  selected,
  onUndo,
  onRedo,
  onReturnToStart,
  onOutAndBack,
  onReverse,
  onClear,
  onRemoveSelected,
}) => (
  <div className="flex flex-wrap items-center gap-1 rounded-lg bg-white/95 p-1 shadow-md ring-1 ring-black/10">
    <div
      className="inline-flex overflow-hidden rounded-md ring-1 ring-gray-300"
      role="radiogroup"
      aria-label="Routing mode"
    >
      {PROFILE_OPTIONS.map((option) => (
        <button
          key={option.value}
          type="button"
          role="radio"
          aria-checked={mode === option.value}
          title={option.description}
          className={`px-2 py-1 text-xs font-medium ${mode === option.value ? "bg-ink text-white" : "bg-white text-gray-700 hover:bg-gray-50"}`}
          onClick={() => {
            onModeChange(option.value);
          }}
        >
          {option.label}
        </button>
      ))}
    </div>
    {/*
     * The actions wrap as one group, below the modes, rather than one by
     * one, which would leave a button alone on a row.
     */}
    <div className="flex items-center gap-1">
      <ToolButton
        title={`Undo (${MODIFIER}Z)`}
        disabled={!canUndo}
        onClick={onUndo}
      >
        <ArrowUturnLeftIcon className="size-4" />
      </ToolButton>
      <ToolButton
        title={`Redo (${MODIFIER}Shift+Z)`}
        disabled={!canRedo}
        onClick={onRedo}
      >
        <ArrowUturnRightIcon className="size-4" />
      </ToolButton>
      <Divider />
      <ToolButton
        title="Return to start: route back to the start to close the loop"
        disabled={waypointCount < 2 || isLoop}
        onClick={onReturnToStart}
      >
        <ArrowPathRoundedSquareIcon className="size-4" />
        <span className="hidden lg:inline">Loop</span>
      </ToolButton>
      <ToolButton
        title="Out and back: return to the start the same way"
        disabled={waypointCount < 2}
        onClick={onOutAndBack}
      >
        <ArrowsRightLeftIcon className="size-4" />
        <span className="hidden lg:inline">Out &amp; back</span>
      </ToolButton>
      <ToolButton
        title="Reverse the direction of the route"
        disabled={waypointCount < 2}
        onClick={onReverse}
      >
        <ArrowsUpDownIcon className="size-4" />
      </ToolButton>
      <ToolButton
        title="Remove all waypoints"
        disabled={waypointCount === 0}
        onClick={onClear}
      >
        <TrashIcon className="size-4" />
      </ToolButton>
    </div>
    {selected !== null && (
      <ToolButton
        title="Remove the selected waypoint (Delete)"
        onClick={onRemoveSelected}
      >
        <XMarkIcon className="size-4" />
        <span>Remove waypoint {selected + 1}</span>
      </ToolButton>
    )}
  </div>
);

interface LiveStatsProps {
  stats: RouteStats | undefined;
  updating: boolean;
}

// LiveStats shows the route's running totals while editing.
export const LiveStats: React.FC<LiveStatsProps> = ({ stats, updating }) => (
  <div className="flex items-center gap-3 rounded-lg bg-white/95 px-3 py-1.5 text-sm shadow-md ring-1 ring-black/10 tabular-nums">
    <span title="Distance">
      <span className="font-semibold text-gray-900">
        {formatKm(stats?.distanceMeters ?? 0)}
      </span>
    </span>
    <span title="Elevation gain" className="text-gray-700">
      ↑ {formatMeters(stats?.elevationGainMeters ?? 0)}
    </span>
    <span title="Elevation loss" className="text-gray-700">
      ↓ {formatMeters(stats?.elevationLossMeters ?? 0)}
    </span>
    {(stats?.repeatedDistanceMeters ?? 0) > 0 && (
      <span
        title="Distance that retraces the route, in either direction"
        className="text-gray-700"
      >
        ↺ {formatKm(stats?.repeatedDistanceMeters ?? 0)}
      </span>
    )}
    {updating && (
      <span className="size-3 animate-spin rounded-full border-2 border-brand-700 border-t-transparent" />
    )}
  </div>
);
