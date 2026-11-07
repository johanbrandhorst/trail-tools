import type { ConnectError } from "@connectrpc/connect";
import { Code } from "@connectrpc/connect";
import { useQuery } from "@connectrpc/connect-query";
import type { UseQueryResult } from "@tanstack/react-query";
import { keepPreviousData, skipToken } from "@tanstack/react-query";
import {
  findTrails,
  listHeatmapTracks,
} from "gen/geo/v1/geo-GeoService_connectquery";
import type { FindTrailsResponse } from "gen/geo/v1/geo_pb";
import { formatKm } from "helpers/format";
import { terrainStyle } from "helpers/labels";
import { decodePolyline } from "helpers/polyline";
import * as leaflet from "leaflet";
import React, { useEffect, useState } from "react";

import type { Viewport } from "./layers";
import { textElement } from "./layers";
import { useMap } from "./map-view";

export const TRAILS_MIN_ZOOM = 13;
const MAX_RADIUS_METERS = 15_000;

const round = (value: number, decimals: number): number => {
  const factor = 10 ** decimals;
  return Math.round(value * factor) / factor;
};

// SearchRadius is half the visible width, capped at the API maximum.
const searchRadius = (viewport: Viewport): number => {
  const { bounds } = viewport;
  const width = bounds.getNorthWest().distanceTo(bounds.getNorthEast());
  return Math.round(Math.min(MAX_RADIUS_METERS, width / 2));
};

// HEAT_COLOR is the color of the heatmap's tracks, darker where they overlap.
const HEAT_COLOR = "#6f3410";

// MetersPerPixel is the size of a screen pixel on the map at a latitude.
const metersPerPixel = (lat: number, zoom: number): number =>
  (40_075_016 * Math.cos((lat * Math.PI) / 180)) / 2 ** (zoom + 8);

/*
 * FETCH_PADDING is how far beyond the view the heatmap is fetched, as a
 * share of the view's size on each side.
 */
const FETCH_PADDING = 1;

/**
 * STALE_TIME is how long fetched trails and heatmap tracks are reused, so
 * panning back or refocusing the window doesn't fetch them again.
 */
const STALE_TIME = 5 * 60 * 1000;

/**
 * UseCanvasRenderer returns a canvas renderer for an overlay's lines, removed
 * from the map when the overlay unmounts. Canvas draws many lines much faster
 * than SVG.
 */
const useCanvasRenderer = (
  map: leaflet.Map,
  options: leaflet.RendererOptions
): leaflet.Canvas => {
  const [renderer] = useState(() => leaflet.canvas(options));
  useEffect(
    () => () => {
      renderer.removeFrom(map);
    },
    [map, renderer]
  );
  return renderer;
};

interface OverlayProps {
  viewport: Viewport;
}

/**
 * NotImported reports whether finding trails failed
 * because no map data has been imported for the area.
 */
export const notImported = (error: ConnectError | null): boolean =>
  error?.code === Code.FailedPrecondition;

// Retry retries failed queries, except where no map data has been imported.
const retry = (failureCount: number, error: ConnectError): boolean =>
  !notImported(error) && failureCount < 3;

/**
 * UseTrails finds the trails around the map center, if shown and zoomed in
 * enough. Calls with the same viewport share the query.
 */
export const useTrails = (
  viewport: Viewport,
  shown: boolean
): UseQueryResult<FindTrailsResponse, ConnectError> => {
  const { center, zoom } = viewport;
  return useQuery(
    findTrails,
    shown && zoom >= TRAILS_MIN_ZOOM
      ? {
          center: { lat: round(center.lat, 3), lng: round(center.lng, 3) },
          // Simplified to about a pixel.
          geometryToleranceMeters: Math.round(metersPerPixel(center.lat, zoom)),
          includeGeometry: true,
          limit: 300,
          radiusMeters: searchRadius(viewport),
        }
      : skipToken,
    { placeholderData: keepPreviousData, retry, staleTime: STALE_TIME }
  );
};

/**
 * HeatmapOverlay draws the user's activity tracks in the visible area as thin
 * translucent lines, darker where the user goes often.
 */
export const HeatmapOverlay: React.FC<OverlayProps> = ({ viewport }) => {
  const map = useMap();
  const { bounds, zoom } = viewport;
  /*
   * The tracks are fetched for a view's width around it, and fetched and
   * drawn again only once the map moves out of that area or zooms, as
   * rebuilding a thousand tracks takes long enough to stall panning.
   */
  const [fetched, setFetched] = useState(() => ({
    area: bounds.pad(FETCH_PADDING),
    zoom,
  }));
  if (fetched.zoom !== zoom || !fetched.area.contains(bounds)) {
    setFetched({ area: bounds.pad(FETCH_PADDING), zoom });
  }
  const { area } = fetched;
  const { data } = useQuery(
    listHeatmapTracks,
    {
      bounds: {
        east: round(area.getEast(), 3),
        north: round(area.getNorth(), 3),
        south: round(area.getSouth(), 3),
        west: round(area.getWest(), 3),
      },
      // Simplified to about a pixel.
      toleranceMeters: Math.max(
        1,
        Math.round(metersPerPixel(area.getCenter().lat, fetched.zoom))
      ),
    },
    { placeholderData: keepPreviousData, staleTime: STALE_TIME }
  );
  const [layer] = useState(() => new HeatmapLayer());
  useEffect(() => {
    layer.addTo(map);
    return () => {
      layer.remove();
    };
  }, [map, layer]);
  useEffect(() => {
    if (data !== undefined) {
      layer.setTracks(
        data.polylines.map((polyline) => decodePolyline(polyline))
      );
    }
  }, [layer, data]);

  return null;
};

/*
 * HEAT_PADDING is how far beyond the view the heatmap's canvas reaches on
 * each side, as a share of the view's size, so panning that far only moves
 * the canvas rather than drawing it again.
 */
const HEAT_PADDING = 0.5;

// MAX_LAT is the latitude spherical Mercator is limited to, as Leaflet's.
const MAX_LAT = 85.051_128_779_8;

// DEGREE is a degree in radians.
const DEGREE = Math.PI / 180;

// ProjectedTrack is a track in pixels at a zoom, with its bounds.
interface ProjectedTrack {
  xs: Float64Array;
  ys: Float64Array;
  minX: number;
  minY: number;
  maxX: number;
  maxY: number;
}

/**
 * HeatmapLayer draws tracks as thin translucent lines on its own canvas,
 * each stroked on its own so they darken where they overlap. It's much
 * faster than a Leaflet line per track for the thousand tracks of a
 * heatmap: the tracks are projected once per zoom, only those in the
 * canvas are drawn, and the canvas, half a view larger than the view on
 * each side, is only drawn again once the map moves past that or zooms.
 * It's drawn at one pixel per CSS pixel, which thin translucent lines
 * don't need more than, keeping the canvas small enough for phones.
 */
class HeatmapLayer extends leaflet.Layer {
  private tracks: leaflet.LatLngTuple[][] = [];
  private projected: ProjectedTrack[] = [];
  private projectedZoom = Number.NaN;
  private canvas: HTMLCanvasElement | undefined;
  // The drawn area in projected pixels at the drawn zoom, if drawn.
  private drawn: leaflet.Bounds | undefined;
  private drawnZoom = Number.NaN;
  private map: leaflet.Map | undefined;

  override onAdd(map: leaflet.Map): this {
    const canvas = leaflet.DomUtil.create(
      "canvas",
      "leaflet-zoom-hide pointer-events-none"
    );
    map.getPanes().overlayPane.append(canvas);
    this.canvas = canvas;
    this.map = map;
    this.drawn = undefined;
    map.on("moveend", this.update);
    map.on("resize", this.redraw);
    this.update();
    return this;
  }

  override onRemove(map: leaflet.Map): this {
    map.off("moveend", this.update);
    map.off("resize", this.redraw);
    this.canvas?.remove();
    this.canvas = undefined;
    this.map = undefined;
    return this;
  }

  // SetTracks replaces the tracks, and draws them.
  setTracks(tracks: leaflet.LatLngTuple[][]): void {
    this.tracks = tracks;
    this.projectedZoom = Number.NaN;
    this.redraw();
  }

  // Redraw draws the tracks again.
  private readonly redraw = (): void => {
    this.drawn = undefined;
    this.update();
  };

  // Update draws the tracks if the view isn't within the drawn area.
  private readonly update = (): void => {
    const { map } = this;
    if (map === undefined || this.canvas === undefined) {
      return;
    }
    const zoom = map.getZoom();
    const view = map.getPixelBounds();
    if (
      this.drawn !== undefined &&
      zoom === this.drawnZoom &&
      this.drawn.contains(view)
    ) {
      return;
    }
    this.draw(map, zoom, view);
  };

  private project(map: leaflet.Map, zoom: number): void {
    if (zoom === this.projectedZoom) {
      return;
    }
    /*
     * The tracks are projected as Leaflet's spherical Mercator does, inline,
     * as map.project's objects for each of hundreds of thousands of points
     * stall the page.
     */
    const scale = map.options.crs?.scale(zoom) ?? 256 * 2 ** zoom;
    this.projected = this.tracks.map((track) => {
      const xs = new Float64Array(track.length);
      const ys = new Float64Array(track.length);
      let [minX, minY, maxX, maxY] = [Infinity, Infinity, -Infinity, -Infinity];
      for (const [index, [lat, lng]] of track.entries()) {
        const px = (lng / 360 + 0.5) * scale;
        const sin = Math.sin(
          Math.max(-MAX_LAT, Math.min(MAX_LAT, lat)) * DEGREE
        );
        const py =
          (0.5 - Math.log((1 + sin) / (1 - sin)) / (4 * Math.PI)) * scale;
        xs[index] = px;
        ys[index] = py;
        minX = Math.min(minX, px);
        minY = Math.min(minY, py);
        maxX = Math.max(maxX, px);
        maxY = Math.max(maxY, py);
      }
      return { maxX, maxY, minX, minY, xs, ys };
    });
    this.projectedZoom = zoom;
  }

  private draw(map: leaflet.Map, zoom: number, view: leaflet.Bounds): void {
    const { canvas } = this;
    if (canvas === undefined) {
      return;
    }
    this.project(map, zoom);
    const size = view.getSize();
    const pad = size.multiplyBy(HEAT_PADDING).round();
    const min = view.min?.subtract(pad) ?? leaflet.point(0, 0);
    const width = size.x + 2 * pad.x;
    const height = size.y + 2 * pad.y;
    canvas.width = width;
    canvas.height = height;
    canvas.style.width = `${width}px`;
    canvas.style.height = `${height}px`;
    /*
     * The canvas is placed in layer pixels, which are projected pixels less
     * the map's pixel origin.
     */
    leaflet.DomUtil.setPosition(canvas, min.subtract(map.getPixelOrigin()));
    const context = canvas.getContext("2d");
    if (context === null) {
      return;
    }
    context.clearRect(0, 0, width, height);
    context.strokeStyle = HEAT_COLOR;
    context.globalAlpha = 0.35;
    context.lineWidth = 2;
    context.lineCap = "round";
    context.lineJoin = "round";
    const maxX = min.x + width;
    const maxY = min.y + height;
    for (const track of this.projected) {
      if (
        track.xs.length < 2 ||
        track.maxX < min.x ||
        track.minX > maxX ||
        track.maxY < min.y ||
        track.minY > maxY
      ) {
        continue;
      }
      context.beginPath();
      context.moveTo((track.xs[0] ?? 0) - min.x, (track.ys[0] ?? 0) - min.y);
      for (let index = 1; index < track.xs.length; index++) {
        context.lineTo(
          (track.xs[index] ?? 0) - min.x,
          (track.ys[index] ?? 0) - min.y
        );
      }
      context.stroke();
    }
    this.drawn = leaflet.bounds(min, leaflet.point(maxX, maxY));
    this.drawnZoom = zoom;
  }
}

// TrailsOverlay draws OpenStreetMap trails around the map center.
export const TrailsOverlay: React.FC<OverlayProps> = ({ viewport }) => {
  const map = useMap();
  const enabled = viewport.zoom >= TRAILS_MIN_ZOOM;
  const { data } = useTrails(viewport, true);
  // A few pixels around the thin lines show their tooltips.
  const renderer = useCanvasRenderer(map, { padding: 0.2, tolerance: 4 });

  useEffect(() => {
    if (data === undefined || !enabled) {
      return undefined;
    }
    const group = leaflet.layerGroup();
    for (const trail of data.trails) {
      if (trail.geometry.length < 2) {
        continue;
      }
      const style = terrainStyle(trail.terrain);
      const line = leaflet.polyline(
        trail.geometry.map((point): leaflet.LatLngTuple => [
          point.lat,
          point.lng,
        ]),
        {
          color: style.color,
          dashArray: "6 4",
          opacity: 0.9,
          renderer,
          weight: 2.5,
        }
      );
      const details = [trail.highway, trail.surface, trail.sacScale]
        .filter((detail) => detail !== "")
        .join(", ");
      const name = trail.name === "" ? "Unnamed trail" : trail.name;
      line.bindTooltip(
        textElement(
          `${name} · ${style.label} · ${formatKm(trail.lengthMeters)}${details === "" ? "" : ` (${details})`}`
        ),
        { sticky: true }
      );
      group.addLayer(line);
    }
    group.addTo(map);
    return () => {
      group.remove();
    };
  }, [map, data, enabled, renderer]);

  return null;
};
