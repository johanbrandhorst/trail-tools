import type { Map as LeafletMap, Point } from "leaflet";
import * as leaflet from "leaflet";

// TAP_MS is the longest touch that counts as a tap.
const TAP_MS = 250;

// DOUBLE_TAP_MS is the longest gap between the taps of a double tap.
const DOUBLE_TAP_MS = 300;

/*
 * TAP_SLOP is how far in pixels a tap may move, and the second tap may be
 * from the first.
 */
const TAP_SLOP = 30;

// PIXELS_PER_ZOOM is how far to drag to zoom by one level.
const PIXELS_PER_ZOOM = 100;

// MOVE_SLOP is how far to drag before it zooms rather than taps.
const MOVE_SLOP = 8;

interface Gesture {
  startY: number;
  startZoom: number;
  anchor: Point;
  moved: boolean;
}

/**
 * EnableOneFingerZoom lets the map be zoomed with one finger, as on phone
 * maps: tap, then touch again and drag down to zoom in or up to zoom out,
 * around where the finger is. A double tap without dragging still zooms in.
 * Leaflet only zooms by pinching or double tapping, which can't zoom out
 * one-handed. It returns a function removing the gesture.
 */
export const enableOneFingerZoom = (map: LeafletMap): (() => void) => {
  const container = map.getContainer();
  let tapStart: { time: number; clientX: number; clientY: number } | undefined;
  let lastTap: { time: number; clientX: number; clientY: number } | undefined;
  let gesture: Gesture | undefined;
  const snap = map.options.zoomSnap;

  const onTouchStart = (event: TouchEvent) => {
    const [touch] = event.touches;
    if (event.touches.length !== 1 || touch === undefined) {
      gesture = undefined;
      tapStart = undefined;
      return;
    }
    const now = performance.now();
    if (
      lastTap !== undefined &&
      now - lastTap.time < DOUBLE_TAP_MS &&
      Math.hypot(
        touch.clientX - lastTap.clientX,
        touch.clientY - lastTap.clientY
      ) < TAP_SLOP
    ) {
      // The second touch of a double tap: drag to zoom, not to pan.
      event.stopPropagation();
      gesture = {
        anchor: leaflet.point(
          touch.clientX - container.getBoundingClientRect().left,
          touch.clientY - container.getBoundingClientRect().top
        ),
        moved: false,
        startY: touch.clientY,
        startZoom: map.getZoom(),
      };
      lastTap = undefined;
      tapStart = undefined;
      return;
    }
    tapStart = { clientX: touch.clientX, clientY: touch.clientY, time: now };
  };

  const onTouchMove = (event: TouchEvent) => {
    const [touch] = event.touches;
    if (gesture === undefined || touch === undefined) {
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    const dy = touch.clientY - gesture.startY;
    if (!gesture.moved && Math.abs(dy) < MOVE_SLOP) {
      return;
    }
    gesture.moved = true;
    // Fractional zooms while dragging, so it follows the finger smoothly.
    map.options.zoomSnap = 0;
    const zoom = Math.min(
      map.getMaxZoom(),
      Math.max(map.getMinZoom(), gesture.startZoom + dy / PIXELS_PER_ZOOM)
    );
    map.setZoomAround(gesture.anchor, zoom, { animate: false });
  };

  const onTouchEnd = (event: TouchEvent) => {
    if (gesture !== undefined) {
      if (gesture.moved) {
        // A drag isn't a double tap: no clicks, so no zooming in after it.
        event.preventDefault();
        event.stopPropagation();
        map.options.zoomSnap = snap;
        map.setZoomAround(gesture.anchor, Math.round(map.getZoom()));
      }
      gesture = undefined;
      return;
    }
    const [touch] = event.changedTouches;
    if (tapStart === undefined || touch === undefined) {
      return;
    }
    const now = performance.now();
    lastTap =
      now - tapStart.time < TAP_MS &&
      Math.hypot(
        touch.clientX - tapStart.clientX,
        touch.clientY - tapStart.clientY
      ) < TAP_SLOP
        ? { clientX: touch.clientX, clientY: touch.clientY, time: now }
        : undefined;
    tapStart = undefined;
  };

  const options: AddEventListenerOptions = { capture: true, passive: false };
  container.addEventListener("touchstart", onTouchStart, options);
  container.addEventListener("touchmove", onTouchMove, options);
  container.addEventListener("touchend", onTouchEnd, options);
  return () => {
    container.removeEventListener("touchstart", onTouchStart, options);
    container.removeEventListener("touchmove", onTouchMove, options);
    container.removeEventListener("touchend", onTouchEnd, options);
    map.options.zoomSnap = snap;
  };
};
