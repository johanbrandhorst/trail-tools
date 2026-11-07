import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { timestampDate } from "@bufbuild/protobuf/wkt";

import { daysBetween, parseDate, today } from "./dates";

const pad = (value: number): string => String(value).padStart(2, "0");

// FormatKm formats meters as kilometers with one decimal, e.g. "12.3 km".
export const formatKm = (meters: number): string =>
  `${(meters / 1000).toFixed(1)} km`;

// FormatMeters formats meters as a whole number, e.g. "1,234 m".
export const formatMeters = (meters: number): string =>
  `${Math.round(meters).toLocaleString()} m`;

// FormatDuration formats seconds as h:mm, e.g. "3:05".
export const formatDuration = (seconds: number): string => {
  const sign = seconds < 0 ? "-" : "";
  const total = Math.round(Math.abs(seconds) / 60);
  const hours = Math.floor(total / 60);
  const minutes = total % 60;
  return `${sign}${hours}:${pad(minutes)}`;
};

// FormatDurationLong formats seconds as h:mm:ss, e.g. "3:05:09".
export const formatDurationLong = (seconds: number): string => {
  const sign = seconds < 0 ? "-" : "";
  const total = Math.round(Math.abs(seconds));
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const secs = total % 60;
  return `${sign}${hours}:${pad(minutes)}:${pad(secs)}`;
};

/**
 * FormatHoursMinutes formats seconds as e.g. "3h 05m", or "34 min" under an
 * hour.
 */
export const formatHoursMinutes = (seconds: number): string => {
  const total = Math.round(seconds / 60);
  const hours = Math.floor(total / 60);
  const minutes = total % 60;
  // A lone "m" would read as meters next to distances and climbs.
  if (hours === 0) {
    return `${minutes} min`;
  }
  return `${hours}h ${pad(minutes)}m`;
};

// FormatPace formats seconds per kilometer as m:ss /km.
export const formatPace = (secondsPerKm: number): string => {
  if (!Number.isFinite(secondsPerKm) || secondsPerKm <= 0) {
    return "–";
  }
  const total = Math.round(secondsPerKm);
  const minutes = Math.floor(total / 60);
  const secs = total % 60;
  return `${minutes}:${pad(secs)} /km`;
};

// FormatSpeedAsPace formats a speed in meters per second as a pace.
export const formatSpeedAsPace = (metersPerSecond: number): string => {
  if (metersPerSecond <= 0) {
    return "–";
  }
  return formatPace(1000 / metersPerSecond);
};

// FormatDate formats YYYY-MM-DD as e.g. "Mon, 12 Oct 2026".
export const formatDate = (value: string): string => {
  if (value === "") {
    return "";
  }
  return parseDate(value).toLocaleDateString(undefined, {
    day: "numeric",
    month: "short",
    weekday: "short",
    year: "numeric",
  });
};

// FormatShortDate formats YYYY-MM-DD as e.g. "12 Oct".
export const formatShortDate = (value: string): string => {
  if (value === "") {
    return "";
  }
  return parseDate(value).toLocaleDateString(undefined, {
    day: "numeric",
    month: "short",
  });
};

// FormatMonth formats YYYY-MM-DD as e.g. "October 2026".
export const formatMonth = (value: string): string =>
  parseDate(value).toLocaleDateString(undefined, {
    month: "long",
    year: "numeric",
  });

// FormatShortMonth formats YYYY-MM-DD as e.g. "Oct".
export const formatShortMonth = (value: string): string =>
  parseDate(value).toLocaleDateString(undefined, { month: "short" });

// FormatTimestamp formats a protobuf timestamp as a local date and time.
export const formatTimestamp = (timestamp: Timestamp | undefined): string => {
  if (timestamp === undefined) {
    return "never";
  }
  return timestampDate(timestamp).toLocaleString(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  });
};

// FormatDaysUntil describes how far away a date is, e.g. "in 12 days".
export const formatDaysUntil = (value: string): string => {
  const days = daysBetween(today(), value);
  if (days === 0) {
    return "today";
  }
  if (days === 1) {
    return "tomorrow";
  }
  if (days === -1) {
    return "yesterday";
  }
  if (days < 0) {
    return `${-days} days ago`;
  }
  return `in ${days} days`;
};

export const formatPercent = (fraction: number): string =>
  `${Math.round(fraction * 100)}%`;

/**
 * FormatRedirectTarget describes the app an OAuth client redirects to, from
 * its redirect URI, so users can tell which app they are connecting.
 */
export const formatRedirectTarget = (uri: string): string => {
  try {
    const url = new URL(uri);
    if (["localhost", "127.0.0.1", "[::1]"].includes(url.hostname)) {
      return "an app on this computer";
    }
    if (url.protocol === "https:") {
      return url.host;
    }
    return `${url.protocol}//${url.host}`;
  } catch {
    return uri;
  }
};

// FormatBytes formats a size in bytes, e.g. "234 MB".
export const formatBytes = (bytes: number): string => {
  if (bytes < 1_000_000) {
    return `${Math.round(bytes / 1000)} KB`;
  }
  if (bytes < 1_000_000_000) {
    return `${Math.round(bytes / 1_000_000)} MB`;
  }
  return `${(bytes / 1_000_000_000).toFixed(2)} GB`;
};

// FormatList joins items into an English list, e.g. "Switzerland and
// Liechtenstein".
export const formatList = (items: string[]): string =>
  new Intl.ListFormat("en", { type: "conjunction" }).format(items);
