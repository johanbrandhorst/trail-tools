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
      layer.setTracks(data.polylines);
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

/*
 * SLICE_MS is how long the heatmap works on drawing in each frame, so that
 * drawing a thousand tracks never stalls the page: on a phone that can take
 * seconds.
 */
const SLICE_MS = 6;

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

// Drawing is a drawing of the tracks in progress.
interface Drawing {
  canvas: HTMLCanvasElement;
  context: CanvasRenderingContext2D;
  zoom: number;
  // The drawn area in projected pixels at the zoom.
  area: leaflet.Bounds;
  // The next track to draw.
  next: number;
}

/**
 * Project projects a track's points to pixels at a scale, as Leaflet's
 * spherical Mercator does, inline, without an object for each point.
 */
const project = (
  points: leaflet.LatLngTuple[],
  scale: number
): ProjectedTrack => {
  const xs = new Float64Array(points.length);
  const ys = new Float64Array(points.length);
  let [minX, minY, maxX, maxY] = [Infinity, Infinity, -Infinity, -Infinity];
  for (const [index, [lat, lng]] of points.entries()) {
    const px = (lng / 360 + 0.5) * scale;
    const sin = Math.sin(Math.max(-MAX_LAT, Math.min(MAX_LAT, lat)) * DEGREE);
    const py = (0.5 - Math.log((1 + sin) / (1 - sin)) / (4 * Math.PI)) * scale;
    xs[index] = px;
    ys[index] = py;
    minX = Math.min(minX, px);
    minY = Math.min(minY, py);
    maxX = Math.max(maxX, px);
    maxY = Math.max(maxY, py);
  }
  return { maxX, maxY, minX, minY, xs, ys };
};

/**
 * HeatmapLayer draws tracks as thin translucent lines on its own canvas,
 * each stroked on its own so they darken where they overlap. A thousand
 * tracks are too many to draw at once without stalling a phone, so:
 *
 * - The canvas reaches half a view beyond the view on each side, and is
 *   only drawn again once the map moves past that or zooms.
 * - Tracks are decoded and projected only when drawn, once per zoom.
 * - Drawing is done a few milliseconds per frame into a hidden canvas,
 *   which replaces the shown one when done, and waits while the map is
 *   being moved or zoomed, so it never interrupts a gesture.
 * - Points less than a pixel from the last one drawn are skipped.
 *
 * It's drawn at one pixel per CSS pixel, which thin translucent lines
 * don't need more than, keeping the canvas small enough for phones.
 */
class HeatmapLayer extends leaflet.Layer {
  private encoded: string[] = [];
  private decoded: (leaflet.LatLngTuple[] | undefined)[] = [];
  private projected: (ProjectedTrack | undefined)[] = [];
  private projectedZoom = Number.NaN;
  private shown: HTMLCanvasElement | undefined;
  // The area and zoom of the shown canvas, if it's drawn.
  private shownArea: leaflet.Bounds | undefined;
  private shownZoom = Number.NaN;
  private drawing: Drawing | undefined;
  private frame = 0;
  private moving = false;
  private map: leaflet.Map | undefined;

  override onAdd(map: leaflet.Map): this {
    this.map = map;
    this.shown = undefined;
    this.shownArea = undefined;
    map.on("movestart zoomstart", this.onMoveStart);
    map.on("moveend", this.onMoveEnd);
    map.on("resize", this.redraw);
    this.update();
    return this;
  }

  override onRemove(map: leaflet.Map): this {
    map.off("movestart zoomstart", this.onMoveStart);
    map.off("moveend", this.onMoveEnd);
    map.off("resize", this.redraw);
    cancelAnimationFrame(this.frame);
    this.drawing = undefined;
    this.shown?.remove();
    this.shown = undefined;
    this.map = undefined;
    return this;
  }

  // SetTracks replaces the tracks, as encoded polylines, and draws them.
  setTracks(encoded: string[]): void {
    this.encoded = encoded;
    this.decoded = [];
    this.projected = [];
    // A drawing in progress is of the old tracks.
    this.drawing = undefined;
    this.redraw();
  }

  private readonly onMoveStart = (): void => {
    this.moving = true;
  };

  private readonly onMoveEnd = (): void => {
    this.moving = false;
    this.update();
  };

  // Redraw draws the tracks again.
  private readonly redraw = (): void => {
    this.shownArea = undefined;
    this.update();
  };

  /**
   * Update starts drawing if the view isn't within the shown or drawing
   * area, and carries on drawing if it is.
   */
  private update(): void {
    const { map } = this;
    if (map === undefined || this.moving) {
      return;
    }
    const zoom = map.getZoom();
    const view = map.getPixelBounds();
    const covers = (area: leaflet.Bounds | undefined, areaZoom: number) =>
      area !== undefined && areaZoom === zoom && area.contains(view);
    if (covers(this.shownArea, this.shownZoom)) {
      this.drawing = undefined;
      return;
    }
    // Tracks drawn at another zoom would show at the wrong scale.
    if (this.shownZoom !== zoom) {
      this.shown?.remove();
      this.shown = undefined;
      this.shownArea = undefined;
    }
    if (
      this.drawing === undefined ||
      !covers(this.drawing.area, this.drawing.zoom)
    ) {
      this.drawing = this.startDrawing(map, zoom, view);
    }
    this.schedule();
  }

  private startDrawing(
    map: leaflet.Map,
    zoom: number,
    view: leaflet.Bounds
  ): Drawing | undefined {
    const size = view.getSize();
    const pad = size.multiplyBy(HEAT_PADDING).round();
    const min = view.min?.subtract(pad) ?? leaflet.point(0, 0);
    const width = size.x + 2 * pad.x;
    const height = size.y + 2 * pad.y;
    const canvas = leaflet.DomUtil.create(
      "canvas",
      "leaflet-zoom-hide pointer-events-none"
    );
    canvas.width = width;
    canvas.height = height;
    canvas.style.width = `${width}px`;
    canvas.style.height = `${height}px`;
    const context = canvas.getContext("2d");
    if (context === null) {
      return undefined;
    }
    context.strokeStyle = HEAT_COLOR;
    context.globalAlpha = 0.35;
    context.lineWidth = 2;
    // Rounded joins are much slower to stroke, and thin lines don't show them.
    context.lineCap = "butt";
    context.lineJoin = "miter";
    if (zoom !== this.projectedZoom) {
      this.projected = [];
      this.projectedZoom = zoom;
    }
    // The map's pixel origin is fixed until the next zoom or reset.
    leaflet.DomUtil.setPosition(canvas, min.subtract(map.getPixelOrigin()));
    return {
      area: leaflet.bounds(min, min.add([width, height])),
      canvas,
      context,
      next: 0,
      zoom,
    };
  }

  private schedule(): void {
    cancelAnimationFrame(this.frame);
    this.frame = requestAnimationFrame(this.step);
  }

  // Step draws tracks for a slice of a frame, and shows the canvas when done.
  private readonly step = (): void => {
    const { drawing, map } = this;
    if (drawing === undefined || map === undefined || this.moving) {
      return;
    }
    const deadline = performance.now() + SLICE_MS;
    const scale =
      map.options.crs?.scale(drawing.zoom) ?? 256 * 2 ** drawing.zoom;
    const { area, context } = drawing;
    const { min = leaflet.point(0, 0), max = leaflet.point(0, 0) } = area;
    while (drawing.next < this.encoded.length) {
      if (performance.now() > deadline) {
        this.schedule();
        return;
      }
      const index = drawing.next;
      drawing.next += 1;
      let track = this.projected[index];
      if (track === undefined) {
        let points = this.decoded[index];
        if (points === undefined) {
          points = decodePolyline(this.encoded[index] ?? "");
          this.decoded[index] = points;
        }
        track = project(points, scale);
        this.projected[index] = track;
      }
      if (
        track.xs.length < 2 ||
        track.maxX < min.x ||
        track.minX > max.x ||
        track.maxY < min.y ||
        track.minY > max.y
      ) {
        continue;
      }
      let lastX = (track.xs[0] ?? 0) - min.x;
      let lastY = (track.ys[0] ?? 0) - min.y;
      context.beginPath();
      context.moveTo(lastX, lastY);
      for (let point = 1; point < track.xs.length; point++) {
        const px = (track.xs[point] ?? 0) - min.x;
        const py = (track.ys[point] ?? 0) - min.y;
        // Points within a pixel of the last one drawn don't show.
        if (
          Math.abs(px - lastX) >= 1 ||
          Math.abs(py - lastY) >= 1 ||
          point === track.xs.length - 1
        ) {
          context.lineTo(px, py);
          lastX = px;
          lastY = py;
        }
      }
      context.stroke();
    }
    // Done: the new canvas replaces the shown one.
    map.getPanes().overlayPane.append(drawing.canvas);
    this.shown?.remove();
    this.shown = drawing.canvas;
    this.shownArea = drawing.area;
    this.shownZoom = drawing.zoom;
    this.drawing = undefined;
  };
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
