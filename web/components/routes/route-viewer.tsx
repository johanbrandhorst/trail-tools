import ElevationProfile from "@components/charts/elevation-profile";
import SendRoute from "@components/garmin/send-route";
import ExploreControls from "@components/map/explore-controls";
import type { LabelMarker } from "@components/map/layers";
import {
  DistanceMarkers,
  FitBounds,
  HoverMarker,
  PointMarker,
} from "@components/map/layers";
import MapView from "@components/map/map-view";
import RoutePath from "@components/map/route-path";
import {
  SnapProgress,
  createdRouteLink,
} from "@components/routes/route-created";
import Card from "@components/ui/card";
import ConfirmButton from "@components/ui/confirm-button";
import { ErrorText } from "@components/ui/query-error";
import { primaryButton, secondaryButton } from "@components/ui/styles";
import { useMutation } from "@connectrpc/connect-query";
import {
  ArrowDownTrayIcon,
  ArrowPathIcon,
  CloudIcon,
  PencilSquareIcon,
  SparklesIcon,
} from "@heroicons/react/24/outline";
import { useQueryClient } from "@tanstack/react-query";
import {
  createRoute,
  deleteRoute,
} from "gen/routes/v1/routes-RouteService_connectquery";
import type { Route } from "gen/routes/v1/routes_pb";
import { RouteService, TrackConversion } from "gen/routes/v1/routes_pb";
import { formatTimestamp } from "helpers/format";
import { routeSourceLabel } from "helpers/labels";
import { invalidateServices } from "helpers/query";
import type { LatLngTuple } from "leaflet";
import React, { useMemo, useState } from "react";
import { Link, useNavigate } from "react-router";

import { CoverageConfirm, useCoverageCheck } from "./coverage-warning";
import { hasEditableGeometry } from "./route-edits";
import {
  distanceMarkers,
  distanceStepKm,
  toTuples,
  waypointPathIndexes,
} from "./route-geometry";
import { RouteStatsPanel } from "./route-stats";

interface RouteViewerProps {
  route: Route;
  onEdit: () => void;
}

// RouteViewer shows a saved route with its map, profile and statistics.
const RouteViewer: React.FC<RouteViewerProps> = ({ route, onEdit }) => {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  // The path point hovered on the elevation profile, and on the map.
  const [hoverIndex, setHoverIndex] = useState<number | null>(null);
  const [mapHover, setMapHover] = useState<number | null>(null);
  const remove = useMutation(deleteRoute, {
    onSuccess: async () => {
      await invalidateServices(queryClient, [RouteService]);
      void navigate("/routes");
    },
  });
  // Exact imports can be copied as an editable route snapped to trails.
  const makeEditable = useMutation(createRoute, {
    onSuccess: async (response) => {
      await invalidateServices(queryClient, [RouteService]);
      if (response.route !== undefined) {
        void navigate(...createdRouteLink(response.route.id, response.warning));
      }
    },
  });
  const trackCoverageCheck = useCoverageCheck();
  const editable = hasEditableGeometry(route);

  const { points, waypoints } = route;
  const tuples = useMemo(() => toTuples(points), [points]);
  const waypointIndexes = useMemo(
    () => waypointPathIndexes(points, waypoints),
    [points, waypoints]
  );
  const kmMarkers = useMemo(() => {
    const total = points.at(-1)?.distanceMeters ?? 0;
    return distanceMarkers(points, distanceStepKm(total)).map(
      (marker): LabelMarker => ({
        label: `${marker.km} km`,
        position: marker.position,
      })
    );
  }, [points]);
  const mapHoverPoint = mapHover === null ? undefined : points.at(mapHover);
  const [start] = tuples;
  const finish = tuples.at(-1);
  let hoverPosition: LatLngTuple | null = null;
  const hoverPoint = hoverIndex === null ? undefined : points.at(hoverIndex);
  if (hoverPoint !== undefined) {
    hoverPosition = [hoverPoint.lat, hoverPoint.lng];
  }

  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,1fr)_340px]">
      <div className="flex min-w-0 flex-col gap-3">
        <div className="overflow-hidden rounded-lg border border-gray-200">
          <MapView className="h-[55vh] min-h-80" rememberView>
            <ExploreControls />
            {points.length > 1 && (
              <RoutePath
                points={points}
                waypointIndexes={waypointIndexes}
                legProfiles={route.legProfiles}
                waypoints={waypoints}
                onHover={setMapHover}
              />
            )}
            <DistanceMarkers markers={kmMarkers} />
            {start !== undefined && (
              <PointMarker position={start} color="#16a34a" tooltip="Start" />
            )}
            {finish !== undefined && tuples.length > 1 && (
              <PointMarker position={finish} color="#dc2626" tooltip="Finish" />
            )}
            <HoverMarker position={hoverPosition} />
            <FitBounds positions={tuples} fitKey={route.id} />
          </MapView>
        </div>
        <Card title="Elevation profile">
          <ElevationProfile
            points={points}
            onHover={setHoverIndex}
            positionMeters={mapHoverPoint?.distanceMeters}
          />
        </Card>
      </div>
      <div className="flex flex-col gap-4">
        <Card>
          <div className="flex flex-col gap-3">
            <div>
              <h1 className="text-xl font-bold text-gray-900">{route.name}</h1>
              <p className="text-xs text-gray-500">
                {routeSourceLabel(route.source)} · updated{" "}
                {formatTimestamp(route.updateTime)}
              </p>
            </div>
            {route.description !== "" && (
              <p className="text-sm whitespace-pre-line text-gray-700">
                {route.description}
              </p>
            )}
            <div className="flex flex-wrap gap-2">
              <button type="button" className={primaryButton} onClick={onEdit}>
                <PencilSquareIcon className="size-4" />
                Edit
              </button>
              {!editable && (
                <button
                  type="button"
                  className={secondaryButton}
                  disabled={
                    makeEditable.isPending || trackCoverageCheck.isPending
                  }
                  title="Create a copy snapped to trails with as few waypoints as possible, which you can reshape"
                  onClick={() => {
                    const source = {
                      case: "routeId",
                      value: route.id,
                    } as const;
                    trackCoverageCheck.checkAndSnap(source, () => {
                      makeEditable.mutate({
                        conversion: TrackConversion.SNAP,
                        name: `${route.name} (editable)`,
                        source,
                      });
                    });
                  }}
                >
                  {makeEditable.isPending ? (
                    <ArrowPathIcon className="size-4 animate-spin" />
                  ) : (
                    <SparklesIcon className="size-4" />
                  )}
                  {makeEditable.isPending ? "Snapping…" : "Make editable"}
                </button>
              )}
              <a
                href={`/routes/${route.id}.gpx`}
                className={secondaryButton}
                download
              >
                <ArrowDownTrayIcon className="size-4" />
                GPX
              </a>
              <SendRoute routeId={route.id} />
              <Link
                to={`/forecast?route=${route.id}`}
                className={secondaryButton}
              >
                <CloudIcon className="size-4" />
                Weather forecast
              </Link>
              <ConfirmButton
                title="Delete route?"
                message={`This permanently deletes "${route.name}". Workouts and races using it lose their route.`}
                disabled={remove.isPending}
                onConfirm={() => {
                  remove.mutate({ id: route.id });
                }}
              >
                Delete
              </ConfirmButton>
            </div>
            {!editable && <CoverageConfirm check={trackCoverageCheck} />}
            <SnapProgress pending={makeEditable.isPending} />
            <ErrorText error={remove.error ?? makeEditable.error} />
          </div>
        </Card>
        <Card title="Statistics">
          <RouteStatsPanel stats={route.stats} />
        </Card>
      </div>
    </div>
  );
};

export default RouteViewer;
