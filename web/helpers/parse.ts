/**
 * Parse helpers for form inputs. Parsers return null for invalid input and
 * treat an empty string as zero, which the API uses for "not set".
 */

const DIGITS = /^\d+$/u;

/**
 * ParseDuration parses "h:mm", "h:mm:ss" or plain minutes ("45") into
 * seconds.
 */
export const parseDuration = (text: string): number | null => {
  const trimmed = text.trim();
  if (trimmed === "") {
    return 0;
  }
  const parts = trimmed.split(":");
  for (const part of parts) {
    if (!DIGITS.test(part)) {
      return null;
    }
  }
  const numbers = parts.map(Number);
  const [first = 0, second = 0, third = 0] = numbers;
  if (numbers.length === 1) {
    return first * 60;
  }
  if (numbers.length === 2) {
    if (second >= 60) {
      return null;
    }
    return first * 3600 + second * 60;
  }
  if (numbers.length === 3) {
    if (second >= 60 || third >= 60) {
      return null;
    }
    return first * 3600 + second * 60 + third;
  }
  return null;
};

const pad = (value: number): string => String(value).padStart(2, "0");

/**
 * DurationInput formats seconds for a duration text input: "h:mm" or
 * "h:mm:ss" with seconds. Zero is formatted as an empty string.
 */
export const durationInput = (
  seconds: number,
  withSeconds: boolean
): string => {
  if (seconds <= 0) {
    return "";
  }
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const secs = Math.round(seconds % 60);
  if (withSeconds) {
    return `${hours}:${pad(minutes)}:${pad(secs)}`;
  }
  return `${hours}:${pad(minutes)}`;
};

// ParseNumber parses a decimal number. Empty input is zero.
export const parseNumber = (text: string): number | null => {
  const trimmed = text.trim();
  if (trimmed === "") {
    return 0;
  }
  const value = Number(trimmed);
  if (!Number.isFinite(value) || value < 0) {
    return null;
  }
  return value;
};

// NumberInput formats a number for a text input. Zero is an empty string.
export const numberInput = (value: number): string => {
  if (value === 0) {
    return "";
  }
  return String(Math.round(value * 100) / 100);
};

// KmInput formats meters as kilometers for an input. Zero is empty.
export const kmInput = (meters: number): string => {
  if (meters === 0) {
    return "";
  }
  return String(Math.round(meters / 10) / 100);
};

// ParseKm parses kilometers into meters.
export const parseKm = (text: string): number | null => {
  const km = parseNumber(text);
  if (km === null) {
    return null;
  }
  return Math.round(km * 1000);
};

// ParseMeters parses a whole number of meters, rounding decimals.
export const parseMeters = (text: string): number | null => {
  const meters = parseNumber(text);
  if (meters === null) {
    return null;
  }
  return Math.round(meters);
};

/**
 * ParseEdited parses an input that was initialized with format(original),
 * returning original if the input still shows it. Inputs show values
 * rounded, e.g. durations without seconds and distances to 10 m, so parsing
 * an input the user didn't touch would change its value when saving.
 */
export const parseEdited = (
  text: string,
  original: number,
  format: (value: number) => string,
  parse: (text: string) => number | null
): number | null => {
  if (text === format(original)) {
    return original;
  }
  return parse(text);
};

// HoursMinutesInput formats seconds as "h:mm" for an input.
export const hoursMinutesInput = (seconds: number): string =>
  durationInput(seconds, false);
