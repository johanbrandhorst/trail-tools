import ElevationProfile from "@components/charts/elevation-profile";
import ExploreControls from "@components/map/explore-controls";
import type { LabelMarker } from "@components/map/layers";
import {
  DistanceMarkers,
  FitBounds,
  HoverMarker,
  MapClick,
} from "@components/map/layers";
import MapView from "@components/map/map-view";
import RoutePath from "@components/map/route-path";
import WaypointMarkers from "@components/map/waypoint-markers";
import Card from "@components/ui/card";
import { TextAreaField, TextField } from "@components/ui/fields";
import { ErrorText } from "@components/ui/query-error";
import {
  iconButton,
  primaryButton,
  secondaryButton,
} from "@components/ui/styles";
import { ConnectError, createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { XMarkIcon } from "@heroicons/react/24/outline";
import type { Route, RoutePoint } from "gen/routes/v1/routes_pb";
import { RouteService, RoutingProfile } from "gen/routes/v1/routes_pb";
import { PROFILE_OPTIONS } from "helpers/labels";
import type { LatLng, LatLngTuple } from "leaflet";
import React, {
  useEffect,
  useEffectEvent,
  useMemo,
  useRef,
  useState,
} from "react";

import { EditorToolbar, LiveStats } from "./editor-toolbar";
import type { EditHistory, RouteShape } from "./route-edits";
import {
  appendWaypoint,
  insertWaypoint,
  isLoop,
  moveWaypoint,
  newHistory,
  newShape,
  outAndBack,
  push,
  redo,
  removeWaypoint,
  returnToStart,
  reverseRoute,
  undo,
} from "./route-edits";
import type { LatLngValue } from "./route-geometry";
import {
  distanceMarkers,
  distanceStepKm,
  toTuples,
  waypointPathIndexes,
} from "./route-geometry";
import { RouteStatsPanel } from "./route-stats";

export interface RouteDraft {
  name: string;
  description: string;
  waypoints: LatLngValue[];
  // The routing profile of each leg, one fewer than waypoints.
  legProfiles: RoutingProfile[];
  // The routing mode last used, saved as the route's default profile.
  profile: RoutingProfile;
  // Whether the waypoints or leg profiles changed from the initial values.
  geometryChanged: boolean;
}

interface RouteEditorProps {
  // The saved route, or undefined for a new route.
  route: Route | undefined;
  // The initial name of a new route.
  defaultName?: string;
  /**
   * Whether the waypoints can be edited. Routes imported from GPX or
   * activities only allow editing the name and description.
   */
  geometryEditable: boolean;
  saving: boolean;
  error: ConnectError | null;
  onSave: (draft: RouteDraft) => void;
  onCancel: () => void;
}

const roundCoordinate = (value: number): number =>
  Math.round(value * 1e6) / 1e6;

const toWaypoint = (position: LatLng): LatLngValue => ({
  lat: roundCoordinate(position.lat),
  lng: roundCoordinate(position.lng),
});

const sameWaypoints = (
  first: LatLngValue[],
  second: LatLngValue[]
): boolean => {
  if (first.length !== second.length) {
    return false;
  }
  for (const [index, point] of first.entries()) {
    const other = second[index];
    if (other?.lat !== point.lat || other.lng !== point.lng) {
      return false;
    }
  }
  return true;
};

// IsTyping reports whether a keyboard event is aimed at a text field.
const isTyping = (event: KeyboardEvent): boolean => {
  const { target } = event;
  if (!(target instanceof HTMLElement)) {
    return false;
  }
  return (
    target.isContentEditable ||
    target.tagName === "INPUT" ||
    target.tagName === "TEXTAREA" ||
    target.tagName === "SELECT"
  );
};

const profileLabel = (profile: RoutingProfile | undefined): string => {
  for (const option of PROFILE_OPTIONS) {
    if (option.value === profile) {
      return option.label;
    }
  }
  return "Imported";
};

const waypointLabel = (index: number, count: number): string => {
  if (index === 0) {
    return "Start";
  }
  if (index === count - 1) {
    return "Finish";
  }
  return `Waypoint ${index + 1}`;
};

/**
 * UniqueKeys returns a React key for each waypoint. Waypoints repeat in loops
 * and out and back routes, so repeats are numbered.
 */
const uniqueKeys = (waypoints: LatLngValue[]): string[] => {
  const seen = new Map<string, number>();
  return waypoints.map((waypoint) => {
    const key = `${waypoint.lat},${waypoint.lng}`;
    const count = seen.get(key) ?? 0;
    seen.set(key, count + 1);
    return `${key}#${count}`;
  });
};

const EMPTY_POINTS: RoutePoint[] = [];

// PreviewResult is the outcome of the latest preview of a shape.
interface PreviewResult {
  shape: RouteShape;
  route: Route | undefined;
  error: string;
}

/**
 * RouteEditor edits a route's waypoints on a map, with a live preview of the
 * routed path, its statistics and elevation profile. Each leg is routed with
 * the mode that was selected when it was drawn, so trail, road, manual and
 * heatmap sections can be mixed.
 */
const RouteEditor: React.FC<RouteEditorProps> = ({
  route,
  defaultName = "",
  geometryEditable,
  saving,
  error,
  onSave,
  onCancel,
}) => {
  const transport = useTransport();
  const client = useMemo(
    () => createClient(RouteService, transport),
    [transport]
  );
  const initialProfile =
    route === undefined || route.profile === RoutingProfile.UNSPECIFIED
      ? RoutingProfile.TRAIL
      : route.profile;
  const [initialShape] = useState<RouteShape>(() =>
    route === undefined
      ? newShape([], [], initialProfile)
      : newShape(
          route.waypoints.map((point) => ({ lat: point.lat, lng: point.lng })),
          route.legProfiles,
          initialProfile
        )
  );

  const [name, setName] = useState(route?.name ?? defaultName);
  const [description, setDescription] = useState(route?.description ?? "");
  const [history, setHistory] = useState<EditHistory>(() =>
    newHistory(initialShape)
  );
  const [mode, setMode] = useState<RoutingProfile>(initialProfile);
  const [selected, setSelected] = useState<number | null>(null);
  const [result, setResult] = useState<PreviewResult>(() => ({
    error: "",
    route,
    shape: initialShape,
  }));
  // The path point hovered on the elevation profile, and on the map.
  const [profileHover, setProfileHover] = useState<number | null>(null);
  const [mapHover, setMapHover] = useState<number | null>(null);
  const [validation, setValidation] = useState("");
  const requestRef = useRef(0);
  const shape = history.present;

  /**
   * Recompute the preview when the shape changes, debounced. Responses to
   * outdated requests are ignored. Unchanged legs are reused by the server.
   */
  useEffect(() => {
    if (!geometryEditable) {
      return undefined;
    }
    requestRef.current += 1;
    const requestId = requestRef.current;
    if (shape === initialShape || shape.waypoints.length < 2) {
      return undefined;
    }
    const timer = globalThis.setTimeout(() => {
      client
        .previewRoute({
          includePoints: true,
          legProfiles: shape.legProfiles,
          routeId: route?.id ?? "",
          waypoints: shape.waypoints,
        })
        .then((response) => {
          if (requestRef.current === requestId) {
            setResult({ error: "", route: response.route, shape });
          }
        })
        .catch((error_: unknown) => {
          if (requestRef.current === requestId) {
            const message = ConnectError.from(error_).rawMessage;
            setResult((previous) => ({
              error: message,
              route: previous.route,
              shape,
            }));
          }
        });
    }, 200);
    return () => {
      globalThis.clearTimeout(timer);
    };
  }, [client, shape, geometryEditable, initialShape, route?.id]);

  const edit = (change: (current: RouteShape) => RouteShape) => {
    setHistory((current) => push(current, change(current.present)));
  };
  const undoEdit = () => {
    setHistory(undo);
    setSelected(null);
  };
  const redoEdit = () => {
    setHistory(redo);
    setSelected(null);
  };
  const removeAt = (index: number) => {
    edit((current) => removeWaypoint(current, index, mode));
    setSelected(null);
  };

  const handleKey = useEffectEvent((event: KeyboardEvent) => {
    if (isTyping(event)) {
      return;
    }
    const key = event.key.toLowerCase();
    const modifier = event.metaKey || event.ctrlKey;
    if (modifier && key === "z") {
      event.preventDefault();
      if (event.shiftKey) {
        redoEdit();
      } else {
        undoEdit();
      }
      return;
    }
    if (event.ctrlKey && key === "y") {
      event.preventDefault();
      redoEdit();
      return;
    }
    if ((key === "delete" || key === "backspace") && selected !== null) {
      event.preventDefault();
      removeAt(selected);
      return;
    }
    if (key === "escape") {
      setSelected(null);
    }
  });
  useEffect(() => {
    if (!geometryEditable) {
      return undefined;
    }
    const listener = (event: KeyboardEvent) => {
      handleKey(event);
    };
    globalThis.addEventListener("keydown", listener);
    return () => {
      globalThis.removeEventListener("keydown", listener);
    };
  }, [geometryEditable]);

  // Without two waypoints there is no route, whatever the last preview was.
  const hasRoute = !geometryEditable || shape.waypoints.length >= 2;
  const preview = shape === initialShape ? route : result.route;
  const shownPreview = hasRoute ? preview : undefined;
  const previewing =
    hasRoute && shape !== initialShape && result.shape !== shape;
  const previewError = result.shape === shape ? result.error : "";
  const points = shownPreview?.points ?? EMPTY_POINTS;
  const previewWaypoints = shownPreview?.waypoints;
  const waypointIndexes = useMemo(
    () =>
      previewWaypoints === undefined
        ? []
        : waypointPathIndexes(points, previewWaypoints),
    [points, previewWaypoints]
  );
  // The path can only be edited when it matches the current waypoints.
  const previewCurrent =
    previewWaypoints !== undefined &&
    sameWaypoints(previewWaypoints, shape.waypoints);
  const kmMarkers = useMemo(() => {
    const total = points.at(-1)?.distanceMeters ?? 0;
    return distanceMarkers(points, distanceStepKm(total)).map(
      (marker): LabelMarker => ({
        label: `${marker.km} km`,
        position: marker.position,
      })
    );
  }, [points]);
  const waypointKeys = useMemo(
    () => uniqueKeys(shape.waypoints),
    [shape.waypoints]
  );
  const waypointTuples = useMemo(
    () => toTuples(shape.waypoints),
    [shape.waypoints]
  );
  const pathTuples = useMemo(() => toTuples(points), [points]);
  const fitTuples = pathTuples.length > 0 ? pathTuples : waypointTuples;
  let hoverPosition: LatLngTuple | null = null;
  const hoverPoint =
    profileHover === null ? undefined : points.at(profileHover);
  if (hoverPoint !== undefined) {
    hoverPosition = [hoverPoint.lat, hoverPoint.lng];
  }
  const mapHoverPoint = mapHover === null ? undefined : points.at(mapHover);

  const save = () => {
    if (name.trim() === "") {
      setValidation("Give the route a name.");
      return;
    }
    if (geometryEditable && shape.waypoints.length < 2) {
      setValidation("Add at least two waypoints by clicking on the map.");
      return;
    }
    setValidation("");
    onSave({
      description,
      geometryChanged: shape !== initialShape,
      legProfiles: shape.legProfiles,
      name: name.trim(),
      profile: mode,
      waypoints: shape.waypoints,
    });
  };

  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,1fr)_340px]">
      <div className="flex min-w-0 flex-col gap-3">
        <div className="overflow-hidden rounded-lg border border-gray-200">
          <MapView className="h-[60vh] min-h-96" rememberView>
            <ExploreControls
              showHeatmap={geometryEditable && mode === RoutingProfile.HEATMAP}
            />
            {geometryEditable && (
              <div className="pointer-events-none absolute top-12.5 right-16.5 left-13 z-[998] flex flex-col items-start gap-1">
                <div className="pointer-events-auto max-w-full">
                  <EditorToolbar
                    mode={mode}
                    onModeChange={setMode}
                    canUndo={history.past.length > 0}
                    canRedo={history.future.length > 0}
                    waypointCount={shape.waypoints.length}
                    isLoop={isLoop(shape)}
                    selected={selected}
                    onUndo={undoEdit}
                    onRedo={redoEdit}
                    onReturnToStart={() => {
                      edit((current) => returnToStart(current, mode));
                    }}
                    onOutAndBack={() => {
                      edit(outAndBack);
                    }}
                    onReverse={() => {
                      edit(reverseRoute);
                      setSelected(null);
                    }}
                    onClear={() => {
                      edit(() => newShape([], [], mode));
                      setSelected(null);
                    }}
                    onRemoveSelected={() => {
                      if (selected !== null) {
                        removeAt(selected);
                      }
                    }}
                  />
                </div>
                {hasRoute && previewError !== "" && (
                  <p className="pointer-events-auto max-w-md rounded-md bg-red-50 px-2 py-1 text-xs text-red-800 shadow ring-1 ring-red-200">
                    {previewError}
                  </p>
                )}
              </div>
            )}
            {geometryEditable && (
              <div className="absolute right-2 bottom-6 z-[1000]">
                <LiveStats stats={shownPreview?.stats} updating={previewing} />
              </div>
            )}
            {points.length > 1 && (
              <RoutePath
                points={points}
                waypointIndexes={waypointIndexes}
                legProfiles={shownPreview?.legProfiles ?? []}
                waypoints={shape.waypoints}
                onInsert={
                  geometryEditable && previewCurrent
                    ? (index, position) => {
                        edit((current) =>
                          insertWaypoint(
                            current,
                            index,
                            toWaypoint(position),
                            mode
                          )
                        );
                        setSelected(null);
                      }
                    : undefined
                }
                onAppend={
                  geometryEditable && previewCurrent
                    ? (position) => {
                        edit((current) =>
                          appendWaypoint(current, toWaypoint(position), mode)
                        );
                        setSelected(null);
                      }
                    : undefined
                }
                onHover={setMapHover}
              />
            )}
            <DistanceMarkers markers={kmMarkers} />
            {geometryEditable && (
              <>
                <MapClick
                  onClick={(position) => {
                    edit((current) =>
                      appendWaypoint(current, toWaypoint(position), mode)
                    );
                    setSelected(null);
                  }}
                />
                <WaypointMarkers
                  waypoints={shape.waypoints}
                  selected={selected}
                  onMove={(index, position) => {
                    edit((current) =>
                      moveWaypoint(current, index, toWaypoint(position), mode)
                    );
                  }}
                  onSelect={setSelected}
                />
              </>
            )}
            <HoverMarker position={hoverPosition} />
            {route !== undefined && (
              <FitBounds positions={fitTuples} fitKey={route.id} />
            )}
          </MapView>
        </div>
        {geometryEditable && (
          <p className="text-xs text-gray-500">
            Click the map to add a waypoint using the selected mode. Click the
            route to add a waypoint on it, or to continue the route back along
            it. Drag the route to reshape it, drag a waypoint to move it, or
            click a waypoint to select it and press Delete to remove it. Manual
            sections are dashed.
          </p>
        )}
        <Card title="Elevation profile">
          <ElevationProfile
            points={points}
            onHover={setProfileHover}
            positionMeters={mapHoverPoint?.distanceMeters}
          />
        </Card>
      </div>
      <div className="flex flex-col gap-4">
        <Card title={route === undefined ? "New route" : "Edit route"}>
          <form
            className="flex flex-col gap-3"
            onSubmit={(event) => {
              event.preventDefault();
              save();
            }}
          >
            <TextField
              label="Name"
              value={name}
              onChange={setName}
              placeholder="e.g. Three peaks loop"
              required
            />
            <TextAreaField
              label="Description"
              value={description}
              onChange={setDescription}
              placeholder="Purpose, highlights, navigation notes"
            />
            {validation !== "" && (
              <p className="text-sm text-red-700">{validation}</p>
            )}
            <ErrorText error={error} />
            <div className="flex gap-2">
              <button type="submit" className={primaryButton} disabled={saving}>
                {saving ? "Saving…" : "Save route"}
              </button>
              <button
                type="button"
                className={secondaryButton}
                onClick={onCancel}
              >
                Cancel
              </button>
            </div>
          </form>
        </Card>
        <Card title="Statistics">
          <RouteStatsPanel stats={shownPreview?.stats} />
        </Card>
        {geometryEditable && (
          <Card title={`Waypoints (${shape.waypoints.length})`}>
            {shape.waypoints.length === 0 ? (
              <p className="text-sm text-gray-500">
                Click on the map to place the start.
              </p>
            ) : (
              <ol className="flex max-h-72 flex-col gap-1 overflow-y-auto text-sm">
                {shape.waypoints.map((_waypoint, index) => {
                  const label = waypointLabel(index, shape.waypoints.length);
                  return (
                    <li
                      key={waypointKeys[index]}
                      className={`flex items-center justify-between gap-2 rounded px-1 ${index === selected ? "bg-amber-50 ring-1 ring-amber-300" : "hover:bg-gray-50"}`}
                    >
                      <button
                        type="button"
                        className="min-w-0 grow text-left"
                        onClick={() => {
                          setSelected(index);
                        }}
                      >
                        <span className="font-medium">{label}</span>{" "}
                        {index > 0 && (
                          <span className="text-xs text-gray-500">
                            via {profileLabel(shape.legProfiles[index - 1])}
                          </span>
                        )}
                      </button>
                      <button
                        type="button"
                        className={iconButton}
                        aria-label={`Remove ${label}`}
                        onClick={() => {
                          removeAt(index);
                        }}
                      >
                        <XMarkIcon className="size-4" />
                      </button>
                    </li>
                  );
                })}
              </ol>
            )}
          </Card>
        )}
      </div>
    </div>
  );
};

export default RouteEditor;
