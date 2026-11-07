import { readStorage, writeStorage } from "helpers/storage";
import type { LatLngTuple, Map as LeafletMap } from "leaflet";
import * as leaflet from "leaflet";
import type { ReactNode } from "react";
import React, {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
} from "react";

import { enableOneFingerZoom } from "./one-finger-zoom";
import { MAX_ZOOM, addTileLayers } from "./tile-layers";

const MapContext = createContext<LeafletMap | null>(null);

// UseMap returns the Leaflet map of the enclosing MapView.
export const useMap = (): LeafletMap => {
  const map = useContext(MapContext);
  if (map === null) {
    throw new Error("useMap must be used inside a MapView");
  }
  return map;
};

const DEFAULT_CENTER: LatLngTuple = [46.5, 8];
const DEFAULT_ZOOM = 5;

interface SavedView {
  center: LatLngTuple;
  zoom: number;
}

const loadView = (key: string): SavedView | null => {
  const raw = readStorage(key);
  if (raw === null) {
    return null;
  }
  try {
    const parsed: unknown = JSON.parse(raw);
    if (
      typeof parsed !== "object" ||
      parsed === null ||
      !("lat" in parsed) ||
      !("lng" in parsed) ||
      !("zoom" in parsed)
    ) {
      return null;
    }
    const { lat, lng, zoom } = parsed;
    if (
      typeof lat !== "number" ||
      typeof lng !== "number" ||
      typeof zoom !== "number"
    ) {
      return null;
    }
    return { center: [lat, lng], zoom };
  } catch {
    return null;
  }
};

const MAP_VIEW_KEY = "trail-tools.map-view";

interface MapViewProps {
  className?: string;
  // Remember the map view across visits in localStorage.
  rememberView?: boolean;
  children?: ReactNode;
}

/**
 * MapView renders a Leaflet map with a choice of base layers and overlays.
 * Children can use useMap to add layers.
 */
const MapView: React.FC<MapViewProps> = ({
  className = "h-96",
  rememberView = false,
  children,
}) => {
  const containerRef = useRef<HTMLDivElement>(null);
  const [map, setMap] = useState<LeafletMap | null>(null);

  useEffect(() => {
    const container = containerRef.current;
    if (container === null) {
      return undefined;
    }
    const saved = rememberView ? loadView(MAP_VIEW_KEY) : null;
    const instance = leaflet.map(container, {
      center: saved === null ? DEFAULT_CENTER : saved.center,
      maxZoom: MAX_ZOOM,
      scrollWheelZoom: false,
      zoom: saved === null ? DEFAULT_ZOOM : saved.zoom,
    });
    /**
     * Only zoom with the scroll wheel once the map has been clicked, so that
     * scrolling the page over a map scrolls the page.
     */
    /**
     * Any press in the map counts, including on markers, which don't pass
     * their clicks on to the map.
     */
    const enableWheelZoom = () => {
      instance.scrollWheelZoom.enable();
    };
    instance.on("focus", enableWheelZoom);
    container.addEventListener("pointerdown", enableWheelZoom);
    const disableWheelZoom = () => {
      instance.scrollWheelZoom.disable();
    };
    instance.on("blur", disableWheelZoom);
    container.addEventListener("mouseleave", disableWheelZoom);
    const disableOneFingerZoom = enableOneFingerZoom(instance);
    addTileLayers(instance);
    leaflet.control.scale({ imperial: false }).addTo(instance);
    if (rememberView) {
      instance.on("moveend", () => {
        const center = instance.getCenter();
        writeStorage(
          MAP_VIEW_KEY,
          JSON.stringify({
            lat: center.lat,
            lng: center.lng,
            zoom: instance.getZoom(),
          })
        );
      });
    }
    setMap(instance);
    return () => {
      container.removeEventListener("mouseleave", disableWheelZoom);
      container.removeEventListener("pointerdown", enableWheelZoom);
      disableOneFingerZoom();
      instance.remove();
      setMap(null);
    };
  }, [rememberView]);

  return (
    <div className="relative isolate">
      <div ref={containerRef} className={`w-full ${className}`} />
      {map !== null && <MapContext value={map}>{children}</MapContext>}
    </div>
  );
};

export default MapView;
