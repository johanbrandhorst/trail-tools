import {
  FitBounds,
  FlyTo,
  HoverMarker,
  MapClick,
  PointMarker,
  PolylineLayer,
} from "@components/map/layers";
import type { MapFocus } from "@components/map/layers";
import MapView, { useMap } from "@components/map/map-view";
import PhotoMarkers from "@components/map/photo-markers";
import type { MapPhoto } from "@components/map/photo-markers";
import type { PrivacyZone } from "gen/users/v1/users_pb";
import type { LatLng, LatLngTuple } from "leaflet";
import * as leaflet from "leaflet";
import React, { useEffect, useMemo, useRef } from "react";

import { splitAt } from "./sections";

interface TripMapProps {
  track: LatLngTuple[];
  photos: MapPhoto[];
  selectedId: string | null;
  // The position hovered on the elevation profile.
  hoverPosition?: LatLngTuple | null;
  onSelectPhoto?: (id: string) => void;
  // Makes the photo markers draggable.
  onMovePhoto?: (id: string, position: LatLng) => void;
  /**
   * The indexes of the track's points that start a new piece after a
   * stretch hidden by privacy zones, which isn't drawn across.
   */
  breaks?: readonly number[];
  // Shows the user's privacy zones, which are hidden on public pages.
  privacyZones?: PrivacyZone[];
  // Called with clicks on the map, e.g. to place a photo.
  onMapClick?: ((position: LatLng) => void) | undefined;
  // Zooms the map to a position, e.g. of a photo shown on the map.
  focus?: MapFocus | null;
  // A stretch of the track to highlight, e.g. the section being read, in pieces.
  highlight?: LatLngTuple[][];
  /**
   * The map moves to fit the highlight, or the whole track without one, when
   * the key changes.
   */
  highlightKey?: string;
  fitKey: string;
  className?: string;
  /**
   * Draws a ski tour: an icy track on a map washed out like snow, rather
   * than a trail's orange on a summer map.
   */
  snow?: boolean;
}

// TrackColors are the colors of a track and its highlighted stretch.
interface TrackColors {
  track: string;
  highlight: string;
}

const TRAIL_COLORS: TrackColors = { highlight: "#c2410c", track: "#ea580c" };
const SNOW_COLORS: TrackColors = { highlight: "#1e3a8a", track: "#0369a1" };

interface HiddenAreaProps {
  center: LatLngTuple;
  radiusMeters: number;
}

// HiddenArea shades an area hidden on public pages.
const HiddenArea: React.FC<HiddenAreaProps> = ({ center, radiusMeters }) => {
  const map = useMap();
  const [lat, lng] = center;
  useEffect(() => {
    const circle = leaflet
      .circle([lat, lng], {
        color: "#4b5563",
        dashArray: "4 4",
        fillOpacity: 0.15,
        interactive: false,
        radius: radiusMeters,
        weight: 1.5,
      })
      .addTo(map);
    return () => {
      circle.remove();
    };
  }, [map, lat, lng, radiusMeters]);
  return null;
};

interface FitHighlightProps {
  positions: LatLngTuple[];
  highlightKey: string;
}

// FitHighlight moves the map to fit positions when the key changes.
const FitHighlight: React.FC<FitHighlightProps> = ({
  positions,
  highlightKey,
}) => {
  const map = useMap();
  const fittedKey = useRef(highlightKey);
  useEffect(() => {
    if (fittedKey.current === highlightKey || positions.length < 2) {
      return;
    }
    fittedKey.current = highlightKey;
    const bounds = leaflet.latLngBounds(positions);
    const options = { maxZoom: 15, padding: [32, 32] as [number, number] };
    if (globalThis.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      map.fitBounds(bounds, options);
    } else {
      map.flyToBounds(bounds, { ...options, duration: 0.8 });
    }
  }, [map, positions, highlightKey]);
  return null;
};

const NO_HIGHLIGHT: LatLngTuple[][] = [];
const NO_BREAKS: readonly number[] = [];
const NO_ZONES: PrivacyZone[] = [];

/**
 * TripMap shows a trip's track with its photos. Without a track, it fits the
 * photos.
 */
const TripMap: React.FC<TripMapProps> = ({
  track,
  photos,
  selectedId,
  hoverPosition = null,
  onSelectPhoto,
  onMovePhoto,
  onMapClick,
  breaks = NO_BREAKS,
  privacyZones = NO_ZONES,
  focus = null,
  highlight = NO_HIGHLIGHT,
  highlightKey,
  fitKey,
  className = "h-96",
  snow = false,
}) => {
  const colors = snow ? SNOW_COLORS : TRAIL_COLORS;
  const [start] = track;
  const finish = track.at(-1);
  const fitPositions =
    track.length > 1 ? track : photos.map((photo) => photo.position);
  const pieces = useMemo(() => splitAt(track, breaks), [track, breaks]);
  const highlighted = useMemo(() => highlight.flat(), [highlight]);
  return (
    <MapView className={snow ? `snow-map ${className}` : className}>
      {track.length > 1 && (
        <PolylineLayer
          positions={pieces}
          color={colors.track}
          weight={4}
          opacity={highlight.length > 0 ? 0.35 : 0.9}
        />
      )}
      {highlight.length > 0 && (
        <PolylineLayer
          positions={highlight}
          color={colors.highlight}
          weight={6}
          opacity={1}
        />
      )}
      {start !== undefined && (
        <PointMarker position={start} color="#16a34a" tooltip="Start" />
      )}
      {finish !== undefined && track.length > 1 && (
        <PointMarker position={finish} color="#dc2626" tooltip="Finish" />
      )}
      {privacyZones.map((zone) => (
        <HiddenArea
          key={zone.id}
          center={[zone.lat, zone.lng]}
          radiusMeters={zone.radiusMeters}
        />
      ))}
      <HoverMarker position={hoverPosition} />
      <PhotoMarkers
        photos={photos}
        selectedId={selectedId}
        onSelect={onSelectPhoto}
        onMove={onMovePhoto}
      />
      {onMapClick !== undefined && <MapClick onClick={onMapClick} />}
      <FitBounds positions={fitPositions} fitKey={fitKey} />
      <FlyTo focus={focus} />
      {highlightKey !== undefined && (
        <FitHighlight
          positions={highlighted.length > 1 ? highlighted : fitPositions}
          highlightKey={highlightKey}
        />
      )}
    </MapView>
  );
};

export default TripMap;
