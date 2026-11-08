import { readStorage, writeStorage } from "helpers/storage";
import type { Control, Map as LeafletMap, TileLayer } from "leaflet";
import * as leaflet from "leaflet";

/**
 * MAX_ZOOM is the maximum zoom of every map. Layers upscale their tiles
 * beyond their provider's native maximum zoom.
 */
export const MAX_ZOOM = 19;

interface TileLayerDefinition {
  // A stable identifier, stored in localStorage.
  id: string;
  name: string;
  url: string;
  attribution: string;
  // The highest zoom the provider serves tiles for.
  maxNativeZoom: number;
  // The lowest zoom the layer shows at. It's hidden when zoomed out further.
  minZoom?: number;
  subdomains?: string;
  opacity?: number;
}

const OSM_COPYRIGHT =
  '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors';
const ESRI_SERVER = "https://server.arcgisonline.com/ArcGIS/rest/services";

const BASE_LAYERS: TileLayerDefinition[] = [
  {
    attribution: `Map data: ${OSM_COPYRIGHT}, <a href="http://viewfinderpanoramas.org">SRTM</a> | Map style: &copy; <a href="https://opentopomap.org">OpenTopoMap</a> (<a href="https://creativecommons.org/licenses/by-sa/3.0/">CC-BY-SA</a>)`,
    id: "opentopomap",
    maxNativeZoom: 17,
    name: "OpenTopoMap",
    subdomains: "abc",
    url: "https://{s}.tile.opentopomap.org/{z}/{x}/{y}.png",
  },
  {
    attribution: OSM_COPYRIGHT,
    id: "osm",
    maxNativeZoom: 19,
    name: "OpenStreetMap",
    url: "https://tile.openstreetmap.org/{z}/{x}/{y}.png",
  },
  {
    attribution: `${OSM_COPYRIGHT} · Style: <a href="https://www.cyclosm.org">CyclOSM</a> hosted by OpenStreetMap France`,
    id: "cyclosm",
    maxNativeZoom: 20,
    name: "CyclOSM",
    subdomains: "abc",
    url: "https://{s}.tile-cyclosm.openstreetmap.fr/cyclosm/{z}/{x}/{y}.png",
  },
  {
    attribution:
      "Tiles &copy; Esri &mdash; Esri, HERE, Garmin, FAO, NOAA, USGS, &copy; OpenStreetMap contributors, and the GIS User Community",
    id: "esri-topo",
    maxNativeZoom: 19,
    name: "Esri World Topo",
    url: `${ESRI_SERVER}/World_Topo_Map/MapServer/tile/{z}/{y}/{x}`,
  },
  {
    attribution:
      "Tiles &copy; Esri &mdash; Source: Esri, Maxar, Earthstar Geographics, and the GIS User Community",
    id: "satellite",
    maxNativeZoom: 19,
    name: "Satellite",
    url: `${ESRI_SERVER}/World_Imagery/MapServer/tile/{z}/{y}/{x}`,
  },
  {
    attribution: "USGS The National Map",
    id: "usgs-topo",
    maxNativeZoom: 16,
    name: "USGS Topo (US)",
    url: "https://basemap.nationalmap.gov/arcgis/rest/services/USGSTopo/MapServer/tile/{z}/{y}/{x}",
  },
];

// SLOPE_OVERLAY_ID is the ID of the slope angle overlay, which has a legend.
export const SLOPE_OVERLAY_ID = "slope";

/*
 * SLOPE_BANDS are the slope angle overlay's colors, as the server renders
 * them in internal/slope.
 */
const SLOPE_BANDS = [
  { color: "#f2e205", label: "27–30°" },
  { color: "#f59e0b", label: "30–35°" },
  { color: "#dc2626", label: "35–40°" },
  { color: "#a21caf", label: "40–45°" },
  { color: "#1e1b4b", label: "45°+" },
];

/**
 * SlopeLegend explains the slope angle overlay's colors, and that the 30 m
 * elevation data smooths out small steep features like cliff bands and
 * gully walls, so it's a guide to terrain, not a measure of it.
 */
const slopeLegend = (): Control => {
  const legend = new leaflet.Control({ position: "bottomleft" });
  legend.onAdd = () => {
    const container = leaflet.DomUtil.create(
      "div",
      "rounded-md bg-white/90 px-2 py-1.5 text-[11px] leading-tight text-gray-800 shadow"
    );
    const title = leaflet.DomUtil.create(
      "div",
      "mb-1 font-semibold",
      container
    );
    title.textContent = "Slope angle";
    for (const band of SLOPE_BANDS) {
      const row = leaflet.DomUtil.create(
        "div",
        "flex items-center gap-1.5 tabular-nums",
        container
      );
      const swatch = leaflet.DomUtil.create(
        "span",
        "inline-block size-2.5 rounded-sm",
        row
      );
      swatch.style.backgroundColor = band.color;
      const label = leaflet.DomUtil.create("span", "", row);
      label.textContent = band.label;
    }
    const note = leaflet.DomUtil.create(
      "div",
      "mt-1 max-w-36 text-[10px] text-gray-500",
      container
    );
    note.textContent =
      "From zoom 12. 30 m elevation data smooths small steep features.";
    return container;
  };
  return legend;
};

const OVERLAYS: TileLayerDefinition[] = [
  {
    attribution: "Hillshade &copy; Esri",
    id: "hillshade",
    maxNativeZoom: 16,
    name: "Hillshade",
    opacity: 0.35,
    url: `${ESRI_SERVER}/Elevation/World_Hillshade/MapServer/tile/{z}/{y}/{x}`,
  },
  {
    /*
     * Ski runs and ski touring, snowshoe and nordic routes mapped in
     * OpenStreetMap, for planning and reading ski tours.
     */
    attribution:
      'Ski routes &copy; <a href="https://www.opensnowmap.org">OpenSnowMap</a> (CC BY-SA)',
    id: "opensnowmap",
    maxNativeZoom: 18,
    name: "Ski routes",
    url: "https://tiles.opensnowmap.org/pistes/{z}/{x}/{y}.png",
  },
  {
    // Rendered by the server from the elevation model, for signed in users.
    attribution: "Slope angles from SRTM elevation data",
    id: SLOPE_OVERLAY_ID,
    maxNativeZoom: 15,
    minZoom: 12,
    name: "Slope angle",
    opacity: 0.7,
    url: "/tiles/slope/{z}/{x}/{y}.png",
  },
];

const LAYERS_KEY = "trail-tools.map-layers";

/**
 * MapOverlays holds each map's overlays by ID, so they can be shown and
 * hidden from outside the layers control.
 */
const mapOverlays = new WeakMap<LeafletMap, Map<string, TileLayer>>();

// OverlayLayer returns a map's overlay by ID, undefined if there's none.
export const overlayLayer = (
  map: LeafletMap,
  id: string
): TileLayer | undefined => mapOverlays.get(map)?.get(id);

/**
 * SetOverlayVisible shows or hides one of a map's overlays. The layers
 * control follows, checking or unchecking it, showing its legend and
 * remembering the selection, as it does for the user's own clicks.
 */
export const setOverlayVisible = (
  map: LeafletMap,
  id: string,
  visible: boolean
): void => {
  const layer = overlayLayer(map, id);
  if (layer === undefined || map.hasLayer(layer) === visible) {
    return;
  }
  if (visible) {
    layer.addTo(map);
  } else {
    layer.remove();
  }
};

interface LayerSelection {
  base: string;
  overlays: string[];
}

const loadSelection = (): LayerSelection => {
  const fallback: LayerSelection = { base: "opentopomap", overlays: [] };
  const raw = readStorage(LAYERS_KEY);
  if (raw === null) {
    return fallback;
  }
  try {
    const parsed: unknown = JSON.parse(raw);
    if (
      typeof parsed !== "object" ||
      parsed === null ||
      !("base" in parsed) ||
      !("overlays" in parsed)
    ) {
      return fallback;
    }
    const { base, overlays } = parsed;
    if (typeof base !== "string" || !Array.isArray(overlays)) {
      return fallback;
    }
    const overlayIds: string[] = [];
    for (const overlay of overlays) {
      if (typeof overlay === "string") {
        overlayIds.push(overlay);
      }
    }
    return { base, overlays: overlayIds };
  } catch {
    return fallback;
  }
};

const createTileLayer = (definition: TileLayerDefinition): TileLayer =>
  leaflet.tileLayer(definition.url, {
    attribution: definition.attribution,
    maxNativeZoom: definition.maxNativeZoom,
    maxZoom: MAX_ZOOM,
    minZoom: definition.minZoom ?? 0,
    opacity: definition.opacity ?? 1,
    subdomains: definition.subdomains ?? "abc",
  });

/**
 * AddTileLayers adds the base layers and overlays to the map with a layers
 * control, restoring and remembering the user's selection.
 */
export const addTileLayers = (map: LeafletMap): void => {
  const selection = loadSelection();
  const baseLayers: Control.LayersObject = {};
  const overlays: Control.LayersObject = {};
  const ids = new Map<TileLayer, string>();
  let selectedBase: TileLayer | null = null;

  for (const definition of BASE_LAYERS) {
    const layer = createTileLayer(definition);
    baseLayers[definition.name] = layer;
    ids.set(layer, definition.id);
    if (definition.id === selection.base || selectedBase === null) {
      selectedBase = layer;
    }
  }
  if (selectedBase !== null) {
    selectedBase.addTo(map);
  }
  const activeOverlays = new Set<string>();
  const legend = slopeLegend();
  const overlaysByID = new Map<string, TileLayer>();
  mapOverlays.set(map, overlaysByID);
  for (const definition of OVERLAYS) {
    const layer = createTileLayer(definition);
    overlays[definition.name] = layer;
    overlaysByID.set(definition.id, layer);
    ids.set(layer, definition.id);
    if (selection.overlays.includes(definition.id)) {
      layer.addTo(map);
      activeOverlays.add(definition.id);
      if (definition.id === SLOPE_OVERLAY_ID) {
        legend.addTo(map);
      }
    }
  }

  let baseId = selectedBase === null ? "" : (ids.get(selectedBase) ?? "");
  const save = () => {
    writeStorage(
      LAYERS_KEY,
      JSON.stringify({ base: baseId, overlays: [...activeOverlays] })
    );
  };
  map.on("baselayerchange", (event: leaflet.LayersControlEvent) => {
    if (event.layer instanceof leaflet.TileLayer) {
      baseId = ids.get(event.layer) ?? baseId;
      save();
    }
  });
  map.on("overlayadd", (event: leaflet.LayersControlEvent) => {
    if (event.layer instanceof leaflet.TileLayer) {
      const id = ids.get(event.layer);
      if (id !== undefined) {
        activeOverlays.add(id);
        save();
      }
      if (id === SLOPE_OVERLAY_ID) {
        legend.addTo(map);
      }
    }
  });
  map.on("overlayremove", (event: leaflet.LayersControlEvent) => {
    if (event.layer instanceof leaflet.TileLayer) {
      const id = ids.get(event.layer);
      if (id !== undefined) {
        activeOverlays.delete(id);
        save();
      }
      if (id === SLOPE_OVERLAY_ID) {
        legend.remove();
      }
    }
  });

  leaflet.control
    .layers(baseLayers, overlays, { collapsed: true, position: "topright" })
    .addTo(map);
};
