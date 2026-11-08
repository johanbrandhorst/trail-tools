import type {
  LatLng,
  LatLngBounds,
  LatLngTuple,
  LeafletMouseEvent,
} from "leaflet";
import * as leaflet from "leaflet";
import React, {
  useEffect,
  useEffectEvent,
  useMemo,
  useRef,
  useState,
} from "react";

import { useMap } from "./map-view";
import { overlayLayer, setOverlayVisible } from "./tile-layers";

/**
 * TextElement wraps text in an element, so that it can be passed to Leaflet
 * tooltips without being interpreted as HTML.
 */
export const textElement = (text: string): HTMLElement => {
  const element = document.createElement("span");
  element.textContent = text;
  return element;
};

interface PolylineLayerProps {
  // The line's positions, or the positions of each of its pieces.
  positions: LatLngTuple[] | LatLngTuple[][];
  color: string;
  weight?: number;
  opacity?: number;
  dashArray?: string;
  /**
   * Called with the clicked location when the line is clicked. Clicks on the
   * line do not reach the map's click handler.
   */
  onClick?: ((position: LatLng) => void) | undefined;
}

// PolylineLayer draws a line on the map.
export const PolylineLayer: React.FC<PolylineLayerProps> = ({
  positions,
  color,
  weight = 4,
  opacity = 0.9,
  dashArray,
  onClick,
}) => {
  const map = useMap();
  const clickable = onClick !== undefined;
  const handleClick = useEffectEvent((position: LatLng) => {
    if (onClick !== undefined) {
      onClick(position);
    }
  });

  // The line is recreated when anything changes, which is cheap.
  useEffect(() => {
    const line = leaflet.polyline(positions, {
      bubblingMouseEvents: !clickable,
      className: clickable ? "cursor-copy" : "",
      color,
      dashArray,
      interactive: clickable,
      opacity,
      weight,
    });
    line.on("click", (event: LeafletMouseEvent) => {
      handleClick(event.latlng);
    });
    line.addTo(map);
    return () => {
      line.remove();
    };
  }, [map, positions, color, weight, opacity, dashArray, clickable]);

  return null;
};

interface PointMarkerProps {
  position: LatLngTuple;
  color: string;
  radius?: number;
  tooltip?: string;
}

// PointMarker draws a small circle, e.g. for a start or finish.
export const PointMarker: React.FC<PointMarkerProps> = ({
  position,
  color,
  radius = 7,
  tooltip,
}) => {
  const map = useMap();
  const [lat, lng] = position;
  useEffect(() => {
    const marker = leaflet.circleMarker([lat, lng], {
      color: "#ffffff",
      fillColor: color,
      fillOpacity: 1,
      radius,
      weight: 2,
    });
    if (tooltip !== undefined) {
      marker.bindTooltip(textElement(tooltip));
    }
    marker.addTo(map);
    return () => {
      marker.remove();
    };
  }, [map, lat, lng, color, radius, tooltip]);
  return null;
};

export interface LabelMarker {
  label: string;
  position: LatLngTuple;
}

interface LabelMarkersProps {
  markers: LabelMarker[];
}

/**
 * DistanceMarkers draws small labels along a route, such as the distance in
 * kilometers.
 */
export const DistanceMarkers: React.FC<LabelMarkersProps> = ({ markers }) => {
  const map = useMap();
  useEffect(() => {
    const group = leaflet.layerGroup();
    for (const marker of markers) {
      const label = document.createElement("div");
      label.className = "distance-marker";
      label.textContent = marker.label;
      group.addLayer(
        leaflet.marker(marker.position, {
          icon: leaflet.divIcon({
            className: "",
            html: label,
            // The label centers itself on the point, whatever its width.
            iconAnchor: [0, 7],
            iconSize: [0, 14],
          }),
          interactive: false,
          keyboard: false,
          zIndexOffset: -2000,
        })
      );
    }
    group.addTo(map);
    return () => {
      group.remove();
    };
  }, [map, markers]);
  return null;
};

/*
 * ARROW_MIN_ZOOM is the zoom from which direction arrows show, close enough
 * to tell which way a loop goes without cluttering the whole course.
 */
const ARROW_MIN_ZOOM = 14;

/*
 * ARROW_SPACING is the distance between direction arrows along a line, in
 * screen pixels, so there are as many at any zoom.
 */
const ARROW_SPACING = 90;

// ARROW_BASE_METERS is the shortest spacing of direction arrows along a line.
const ARROW_BASE_METERS = 25;

// MAX_ARROWS limits the arrows drawn at once, for very long lines.
const MAX_ARROWS = 300;

// ArrowMarkersProps are the line DirectionArrows marks.
interface ArrowMarkersProps {
  positions: LatLngTuple[];
}

/**
 * DirectionArrows draws arrows along a line pointing the way it goes, once
 * the map is zoomed in, every ARROW_SPACING pixels in view. They're placed
 * again as the map moves or zooms, as their spacing is on screen.
 */
export const DirectionArrows: React.FC<ArrowMarkersProps> = ({ positions }) => {
  const map = useMap();
  // The distance along the line to each of its points, in meters.
  const distances = useMemo(() => {
    const result = [0];
    for (let index = 1; index < positions.length; index++) {
      const from = positions[index - 1] ?? [0, 0];
      const to = positions[index] ?? [0, 0];
      result.push((result.at(-1) ?? 0) + map.distance(from, to));
    }
    return result;
  }, [map, positions]);
  useEffect(() => {
    const group = leaflet.layerGroup().addTo(map);
    const draw = () => {
      group.clearLayers();
      const zoom = map.getZoom();
      const total = distances.at(-1) ?? 0;
      if (zoom < ARROW_MIN_ZOOM || positions.length < 2 || total === 0) {
        return;
      }
      /*
       * The arrows are at whole multiples of a spacing along the line, so
       * they stay in place as the map zooms: the spacing closest to
       * ARROW_SPACING on screen of 25 m doubled any number of times, so
       * zooming in adds arrows between them and zooming out takes every
       * other away.
       */
      const [lat] = positions[0] ?? [0, 0];
      const metersPerPixel =
        (40_075_016 * Math.cos((lat * Math.PI) / 180)) / 2 ** (zoom + 8);
      const spacing =
        ARROW_BASE_METERS *
        2 **
          Math.max(
            0,
            Math.round(
              Math.log2((ARROW_SPACING * metersPerPixel) / ARROW_BASE_METERS)
            )
          );
      // Only arrows in or just around the view are drawn.
      const view = map.getBounds().pad(0.2);
      let segment = 1;
      let count = 0;
      for (
        let along = spacing;
        along < total && count < MAX_ARROWS;
        along += spacing
      ) {
        while ((distances[segment] ?? total) < along) {
          segment += 1;
        }
        const from = positions[segment - 1];
        const to = positions[segment];
        const start = distances[segment - 1] ?? 0;
        const length = (distances[segment] ?? start) - start;
        if (from === undefined || to === undefined || length === 0) {
          continue;
        }
        const fraction = (along - start) / length;
        const at = leaflet.latLng(
          from[0] + (to[0] - from[0]) * fraction,
          from[1] + (to[1] - from[1]) * fraction
        );
        if (!view.contains(at)) {
          continue;
        }
        const tail = map.latLngToLayerPoint(from);
        const head = map.latLngToLayerPoint(to);
        const angle =
          (Math.atan2(head.y - tail.y, head.x - tail.x) * 180) / Math.PI;
        const arrow = document.createElement("div");
        arrow.className = "direction-arrow";
        arrow.style.transform = `rotate(${angle}deg)`;
        group.addLayer(
          leaflet.marker(at, {
            icon: leaflet.divIcon({
              className: "",
              html: arrow,
              iconAnchor: [7, 7],
              iconSize: [14, 14],
            }),
            interactive: false,
            keyboard: false,
          })
        );
        count += 1;
      }
    };
    draw();
    map.on("zoomend moveend", draw);
    return () => {
      map.off("zoomend moveend", draw);
      group.remove();
    };
  }, [map, positions, distances]);
  return null;
};

interface HoverMarkerProps {
  position: LatLngTuple | null;
}

// HoverMarker shows the point currently hovered on an elevation profile.
export const HoverMarker: React.FC<HoverMarkerProps> = ({ position }) => {
  const map = useMap();
  const hasPosition = position !== null;
  const [lat, lng] = position ?? [0, 0];
  useEffect(() => {
    if (!hasPosition) {
      return undefined;
    }
    const marker = leaflet.circleMarker([lat, lng], {
      color: "#111827",
      fillColor: "#facc15",
      fillOpacity: 1,
      interactive: false,
      radius: 6,
      weight: 2,
    });
    marker.addTo(map);
    return () => {
      marker.remove();
    };
  }, [map, hasPosition, lat, lng]);
  return null;
};

interface FitBoundsProps {
  positions: LatLngTuple[];
  /**
   * The map is only fitted again when the key changes, so that it does not
   * jump around while the positions are being edited.
   */
  fitKey: string;
}

// FitBounds zooms the map to show all positions.
export const FitBounds: React.FC<FitBoundsProps> = ({ positions, fitKey }) => {
  const map = useMap();
  const fittedKey = useRef("");
  useEffect(() => {
    if (positions.length === 0 || fittedKey.current === fitKey) {
      return;
    }
    fittedKey.current = fitKey;
    map.fitBounds(leaflet.latLngBounds(positions), {
      maxZoom: 15,
      padding: [24, 24],
    });
  }, [map, positions, fitKey]);
  return null;
};

// MapFocus is a position to zoom to, and a key that changes on every request.
export interface MapFocus {
  position: LatLngTuple;
  key: number;
}

/**
 * FlyTo zooms the map to a position each time the focus changes, to at
 * least zoom level 15.
 */
export const FlyTo: React.FC<{ focus: MapFocus | null }> = ({ focus }) => {
  const map = useMap();
  useEffect(() => {
    if (focus !== null) {
      map.flyTo(focus.position, Math.max(map.getZoom(), 15), {
        duration: 0.6,
      });
    }
  }, [map, focus]);
  return null;
};

interface MapClickProps {
  onClick: (position: LatLng) => void;
}

/**
 * MapClick calls onClick with the location of clicks on the map. A click that
 * closes a popup only closes it, so dismissing a popup doesn't also act on the
 * map.
 */
export const MapClick: React.FC<MapClickProps> = ({ onClick }) => {
  const map = useMap();
  const handleClick = useEffectEvent((position: LatLng) => {
    onClick(position);
  });
  useEffect(() => {
    let popupOpen = false;
    let closingPopup = false;
    const onPopupOpen = () => {
      popupOpen = true;
    };
    const onPopupClose = () => {
      popupOpen = false;
    };
    // Popups close on preclick, so whether one was open is noted before.
    const onPreclick = () => {
      closingPopup = popupOpen;
    };
    const handler = (event: LeafletMouseEvent) => {
      if (closingPopup) {
        closingPopup = false;
        return;
      }
      handleClick(event.latlng);
    };
    map.on("popupopen", onPopupOpen);
    map.on("popupclose", onPopupClose);
    map.on("preclick", onPreclick);
    map.on("click", handler);
    const container = map.getContainer();
    container.style.cursor = "crosshair";
    return () => {
      map.off("popupopen", onPopupOpen);
      map.off("popupclose", onPopupClose);
      map.off("preclick", onPreclick);
      map.off("click", handler);
      container.style.cursor = "";
    };
  }, [map]);
  return null;
};

export interface Viewport {
  bounds: LatLngBounds;
  center: LatLng;
  zoom: number;
}

const currentViewport = (map: leaflet.Map): Viewport => ({
  bounds: map.getBounds(),
  center: map.getCenter(),
  zoom: map.getZoom(),
});

// UseViewport returns the map's viewport, updated when the map stops moving.
export const useViewport = (): Viewport => {
  const map = useMap();
  const [viewport, setViewport] = useState<Viewport>(() =>
    currentViewport(map)
  );
  useEffect(() => {
    const handler = () => {
      setViewport(currentViewport(map));
    };
    map.on("moveend", handler);
    return () => {
      map.off("moveend", handler);
    };
  }, [map]);
  return viewport;
};

interface OverlayVisibilityProps {
  // The overlay's ID, like SLOPE_OVERLAY_ID.
  id: string;
  visible: boolean;
  // Called when the user shows or hides the overlay in the layers control.
  onChange: (visible: boolean) => void;
}

/**
 * OverlayVisibility shows or hides one of the map's overlays from outside
 * the layers control, and reports when the user changes it there, keeping
 * the two in step.
 */
export const OverlayVisibility: React.FC<OverlayVisibilityProps> = ({
  id,
  visible,
  onChange,
}) => {
  const map = useMap();
  const changed = useEffectEvent(onChange);
  useEffect(() => {
    setOverlayVisible(map, id, visible);
  }, [map, id, visible]);
  useEffect(() => {
    const layer = overlayLayer(map, id);
    const onAdd = (event: leaflet.LayersControlEvent) => {
      if (event.layer === layer) {
        changed(true);
      }
    };
    const onRemove = (event: leaflet.LayersControlEvent) => {
      if (event.layer === layer) {
        changed(false);
      }
    };
    map.on("overlayadd", onAdd);
    map.on("overlayremove", onRemove);
    return () => {
      map.off("overlayadd", onAdd);
      map.off("overlayremove", onRemove);
    };
  }, [map, id]);
  return null;
};
