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
];

const LAYERS_KEY = "trail-tools.map-layers";

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
  for (const definition of OVERLAYS) {
    const layer = createTileLayer(definition);
    overlays[definition.name] = layer;
    ids.set(layer, definition.id);
    if (selection.overlays.includes(definition.id)) {
      layer.addTo(map);
      activeOverlays.add(definition.id);
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
    }
  });
  map.on("overlayremove", (event: leaflet.LayersControlEvent) => {
    if (event.layer instanceof leaflet.TileLayer) {
      const id = ids.get(event.layer);
      if (id !== undefined) {
        activeOverlays.delete(id);
        save();
      }
    }
  });

  leaflet.control
    .layers(baseLayers, overlays, { collapsed: true, position: "topright" })
    .addTo(map);
};
