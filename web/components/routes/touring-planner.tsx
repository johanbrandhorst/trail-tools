import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import AvalancheForecastView from "@components/forecast/avalanche-forecast";
import {
  defaultStart,
  formatClock,
  parseLocalDateTime,
} from "@components/forecast/forecast-utils";
import TouringVerdictView from "@components/forecast/touring-verdict";
import { CheckboxField, TextField } from "@components/ui/fields";
import { ErrorText } from "@components/ui/query-error";
import { linkClass, secondaryButton } from "@components/ui/styles";
import { useMutation } from "@connectrpc/connect-query";
import { ExclamationTriangleIcon } from "@heroicons/react/24/solid";
import { getRouteForecast } from "gen/routes/v1/routes-RouteService_connectquery";
import type {
  ForecastPoint,
  GetRouteForecastResponse,
  RoutePoint,
} from "gen/routes/v1/routes_pb";
import { toDateString } from "helpers/dates";
import { formatHoursMinutes } from "helpers/format";
import { parseNumber } from "helpers/parse";
import React, { useState } from "react";
import { Link } from "react-router";

/**
 * MAX_PLANNED_POINTS bounds the points of the path sent for a forecast:
 * the server looks up the elevation of each, and the forecast only samples
 * the route every hour, so a few hundred keep its shape.
 */
const MAX_PLANNED_POINTS = 500;

// Downsample keeps at most MAX_PLANNED_POINTS of a path, and its end.
const downsample = (points: RoutePoint[]): RoutePoint[] => {
  const step = Math.ceil(points.length / MAX_PLANNED_POINTS);
  const result = points.filter((_point, index) => index % step === 0);
  const last = points.at(-1);
  if (last !== undefined && result.at(-1) !== last) {
    result.push(last);
  }
  return result;
};

// Range formats the lowest and highest of some values, e.g. "1800–2400 m".
const range = (values: number[], unit: string): string => {
  if (values.length === 0) {
    return "–";
  }
  const low = Math.round(Math.min(...values));
  const high = Math.round(Math.max(...values));
  if (low === high) {
    return `${low} ${unit}`;
  }
  // A dash between negative numbers reads as a minus.
  return low < 0 ? `${low} to ${high} ${unit}` : `${low}–${high} ${unit}`;
};

// Summary shows the forecast along the route that matters for a ski tour.
const Summary: React.FC<{ points: ForecastPoint[] }> = ({ points }) => {
  const freezing = points
    .map((point) => point.freezingLevelMeters)
    .filter((level) => level > 0);
  const newSnow = Math.max(
    0,
    ...points.map((point) => point.snowfallPast72HoursCm)
  );
  const snowfall = points.reduce((sum, point) => sum + point.snowfallCm, 0);
  const wind = Math.max(0, ...points.map((point) => point.windSpeedKmh));
  const gusts = Math.max(0, ...points.map((point) => point.windGustsKmh));
  return (
    <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-sm tabular-nums">
      <div>
        <dt className="text-xs text-gray-500">Freezing level</dt>
        <dd className="font-medium text-gray-900">{range(freezing, "m")}</dd>
      </div>
      <div>
        <dt className="text-xs text-gray-500">New snow, 3 days</dt>
        <dd className="font-medium text-gray-900">
          {Math.round(newSnow)} cm
          {snowfall >= 0.5 && (
            <span className="font-normal text-gray-600">
              {" "}
              · {snowfall.toFixed(1)} cm falling
            </span>
          )}
        </dd>
      </div>
      <div>
        <dt className="text-xs text-gray-500">Wind</dt>
        <dd className="font-medium text-gray-900">
          {Math.round(wind)} km/h, gusts {Math.round(gusts)}
        </dd>
      </div>
      <div>
        <dt className="text-xs text-gray-500">Temperature</dt>
        <dd className="font-medium text-gray-900">
          {range(
            points.map((point) => point.temperatureCelsius),
            "°C"
          )}
        </dd>
      </div>
    </dl>
  );
};

interface ResultProps {
  forecast: GetRouteForecastResponse;
  // Whether the route changed since the forecast.
  stale: boolean;
}

// Result shows the conditions of a planned tour.
const Result: React.FC<ResultProps> = ({ forecast, stale }) => {
  const [first] = forecast.points;
  const date =
    first?.time === undefined
      ? undefined
      : toDateString(timestampDate(first.time));
  return (
    <div className="flex flex-col gap-3">
      {stale && (
        <p className="text-sm text-amber-800">
          The route changed since: check again for its conditions.
        </p>
      )}
      <p className="text-sm text-gray-600 tabular-nums">
        {formatClock(first?.time)} to {formatClock(forecast.finishTime)},{" "}
        {formatHoursMinutes(forecast.durationSeconds)}
      </p>
      <TouringVerdictView verdict={forecast.touringVerdict} />
      <Summary points={forecast.points} />
      {forecast.warnings.length > 0 && (
        <ul className="flex flex-col gap-1 text-xs text-amber-900">
          {forecast.warnings.map((warning) => (
            <li key={warning} className="flex gap-1.5">
              <ExclamationTriangleIcon
                className="mt-0.5 size-3.5 shrink-0 text-amber-600"
                aria-hidden="true"
              />
              <span>{warning}</span>
            </li>
          ))}
        </ul>
      )}
      {forecast.avalancheForecast === undefined ? (
        <p className="text-xs text-gray-500">
          There&apos;s no avalanche forecast here: Avalanche Canada&apos;s
          forecasts cover Canada&apos;s mountains in winter. Check the local
          avalanche service.
        </p>
      ) : (
        <div className="flex flex-col gap-1">
          <h3 className="text-xs font-medium text-gray-500">
            Avalanche forecast
          </h3>
          <AvalancheForecastView
            forecast={forecast.avalancheForecast}
            date={date}
          />
        </div>
      )}
    </div>
  );
};

interface TouringPlannerProps {
  // The route's path. It needs at least 2 points to plan.
  points: RoutePoint[];
  // The ID of the saved route, for its full forecast, or empty if unsaved.
  routeId: string;
  // Whether the slope angle overlay is on the map.
  slopeShown: boolean;
  onSlopeShownChange: (shown: boolean) => void;
}

/**
 * TouringPlanner plans a ski tour on the route for a day and time: the
 * forecast along it that matters for touring (freezing level, new snow and
 * wind), the avalanche danger, and the verdict by the user's touring
 * limits, with the slope angle overlay on the map.
 */
const TouringPlanner: React.FC<TouringPlannerProps> = ({
  points,
  routeId,
  slopeShown,
  onSlopeShownChange,
}) => {
  const [start, setStart] = useState(defaultStart);
  // Skinning up is slower than running, so the defaults are a ski tour's.
  const [speed, setSpeed] = useState("4");
  const [climb, setClimb] = useState("400");
  const [validation, setValidation] = useState("");
  // The path the forecast is for, to tell when the route changed since.
  const [checked, setChecked] = useState<RoutePoint[] | null>(null);
  const forecast = useMutation(getRouteForecast);

  const check = () => {
    const startDate = parseLocalDateTime(start);
    const speedValue = parseNumber(speed);
    const climbValue = parseNumber(climb);
    if (startDate === null) {
      setValidation("Choose a start date and time.");
      return;
    }
    if (speedValue === null || speedValue <= 0) {
      setValidation("The flat speed must be a positive number.");
      return;
    }
    if (climbValue === null || climbValue < 0) {
      setValidation("The climbing rate must be a number, or 0 to ignore it.");
      return;
    }
    setValidation("");
    setChecked(points);
    forecast.mutate({
      climbMetersPerHour: climbValue,
      skiTour: true,
      source: {
        case: "path",
        value: {
          points: downsample(points).map((point) => ({
            lat: point.lat,
            lng: point.lng,
          })),
        },
      },
      speedKmh: speedValue,
      startTime: timestampFromDate(startDate),
    });
  };

  return (
    <div className="flex flex-col gap-3">
      <form
        className="flex flex-col gap-3"
        onSubmit={(event) => {
          event.preventDefault();
          check();
        }}
      >
        <TextField
          label="Start (your local time)"
          type="datetime-local"
          value={start}
          onChange={setStart}
          required
        />
        <div className="grid grid-cols-2 gap-3">
          <TextField
            label="Flat speed (km/h)"
            value={speed}
            onChange={setSpeed}
            inputMode="decimal"
          />
          <TextField
            label="Climbing (m/h)"
            value={climb}
            onChange={setClimb}
            inputMode="numeric"
          />
        </div>
        <CheckboxField
          label="Slope angles on the map, from zoom 12"
          checked={slopeShown}
          onChange={onSlopeShownChange}
        />
        {validation !== "" && (
          <p className="text-sm text-red-700">{validation}</p>
        )}
        <ErrorText error={forecast.error} />
        <div>
          <button
            type="submit"
            className={secondaryButton}
            disabled={forecast.isPending || points.length < 2}
          >
            {forecast.isPending ? "Checking…" : "Check conditions"}
          </button>
        </div>
      </form>
      {forecast.data !== undefined && (
        <Result forecast={forecast.data} stale={checked !== points} />
      )}
      {forecast.data !== undefined && routeId !== "" && (
        <Link
          to={`/forecast?route=${routeId}&start=${start}&ski=1`}
          className={`${linkClass} text-sm`}
        >
          Hour by hour along the saved route
        </Link>
      )}
    </div>
  );
};

export default TouringPlanner;
