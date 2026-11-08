import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import ElevationProfile from "@components/charts/elevation-profile";
import type {
  ProfileMarker,
  ProfilePoint,
} from "@components/charts/elevation-profile";
import AvalancheForecastView from "@components/forecast/avalanche-forecast";
import ForecastMarkers from "@components/forecast/forecast-markers";
import {
  defaultStart,
  forecastKindLabel,
  formatClock,
  formatTime,
  parseLocalDateTime,
} from "@components/forecast/forecast-utils";
import TouringVerdictView from "@components/forecast/touring-verdict";
import { FitBounds, HoverMarker, PolylineLayer } from "@components/map/layers";
import MapView from "@components/map/map-view";
import { toTuples } from "@components/routes/route-geometry";
import Card from "@components/ui/card";
import { CheckboxField, SelectField, TextField } from "@components/ui/fields";
import type { SelectOption } from "@components/ui/fields";
import { Badge, PageHeader, Stat } from "@components/ui/misc";
import { ErrorText } from "@components/ui/query-error";
import {
  primaryButton,
  tableClass,
  tdClass,
  thClass,
} from "@components/ui/styles";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import {
  ArrowDownIcon,
  ExclamationTriangleIcon,
  MoonIcon,
  SunIcon,
} from "@heroicons/react/24/outline";
import {
  getRouteForecast,
  listRoutes,
} from "gen/routes/v1/routes-RouteService_connectquery";
import type {
  ForecastPoint,
  GetRouteForecastResponse,
} from "gen/routes/v1/routes_pb";
import { toDateString } from "helpers/dates";
import { formatHoursMinutes, formatKm, formatMeters } from "helpers/format";
import { parseNumber } from "helpers/parse";
import {
  compassDirection,
  temperatureClass,
  weatherSymbol,
} from "helpers/weather";
import type { LatLngTuple } from "leaflet";
import React, {
  useEffect,
  useEffectEvent,
  useMemo,
  useRef,
  useState,
} from "react";
import { Link, useSearchParams } from "react-router";

type SourceMode = "route" | "gpx";

const INTERVAL_OPTIONS: SelectOption[] = [
  { label: "Every 30 minutes", value: "30" },
  { label: "Every hour", value: "60" },
  { label: "Every 2 hours", value: "120" },
];

interface WindProps {
  point: ForecastPoint;
}

/**
 * Wind shows the wind speed and gusts with an arrow pointing where the wind
 * blows to.
 */
const Wind: React.FC<WindProps> = ({ point }) => (
  <span className="inline-flex items-center gap-1 whitespace-nowrap">
    <ArrowDownIcon
      className="size-3.5 text-gray-500"
      style={{ transform: `rotate(${point.windDirectionDegrees}deg)` }}
      aria-label={`from ${compassDirection(point.windDirectionDegrees)}`}
    />
    <span className="tabular-nums">
      {Math.round(point.windSpeedKmh)}
      <span className="text-gray-500"> / {Math.round(point.windGustsKmh)}</span>
    </span>
    <span className="text-xs text-gray-500">
      {compassDirection(point.windDirectionDegrees)}
    </span>
  </span>
);

interface ForecastTableProps {
  points: ForecastPoint[];
  onHover: (index: number | null) => void;
}

/**
 * HEAVY_NEW_SNOW_CM is the snow in 3 days from which it's highlighted:
 * about when avalanche forecasters expect the danger to rise.
 */
const HEAVY_NEW_SNOW_CM = 30;

/**
 * ForecastTable lists the forecast points. The snow columns are only shown
 * when there's snow on the route, so summer forecasts stay narrow.
 */
const ForecastTable: React.FC<ForecastTableProps> = ({ points, onHover }) => {
  const snowy = points.some(
    (point) => point.snowDepthCm >= 1 || point.snowfallPast72HoursCm >= 1
  );
  return (
    <div className="-mx-4 overflow-x-auto px-4">
      <table className={tableClass}>
        <thead>
          <tr>
            <th className={thClass}>Time</th>
            <th className={`${thClass} text-right`}>Km</th>
            <th className={`${thClass} text-right`}>Elev</th>
            <th className={thClass}>Weather</th>
            <th className={`${thClass} text-right`}>Temp</th>
            <th className={`${thClass} text-right`}>Precip</th>
            <th className={thClass}>Wind / gusts km/h</th>
            <th className={`${thClass} text-right`}>0°C level</th>
            {snowy && (
              <>
                <th className={`${thClass} text-right`}>Snow depth</th>
                <th className={`${thClass} text-right`}>New snow 3 days</th>
              </>
            )}
            <th className={`${thClass} text-right`}>UV</th>
            <th className={thClass} />
          </tr>
        </thead>
        <tbody className="divide-y divide-gray-100">
          {points.map((point, index) => {
            const kind = forecastKindLabel(point.kind);
            const belowFreezing =
              point.freezingLevelMeters > 0 &&
              point.freezingLevelMeters < point.elevationMeters;
            return (
              <tr
                key={`${point.distanceMeters}-${point.kind}`}
                className="hover:bg-gray-50"
                onMouseEnter={() => {
                  onHover(index);
                }}
                onMouseLeave={() => {
                  onHover(null);
                }}
              >
                <td className={`${tdClass} whitespace-nowrap`}>
                  <span className="font-medium">{formatClock(point.time)}</span>
                  {kind !== "" && (
                    <Badge className="ml-1 bg-gray-100 text-gray-700 ring-gray-300">
                      {kind}
                    </Badge>
                  )}
                </td>
                <td className={`${tdClass} text-right tabular-nums`}>
                  {(point.distanceMeters / 1000).toFixed(1)}
                </td>
                <td className={`${tdClass} text-right tabular-nums`}>
                  {Math.round(point.elevationMeters)}
                </td>
                <td className={tdClass}>
                  <span className="flex items-center gap-1.5">
                    <span className="text-lg leading-none" aria-hidden="true">
                      {weatherSymbol(point.weatherCode, point.isDay)}
                    </span>
                    <span className="text-xs">{point.weatherDescription}</span>
                  </span>
                </td>
                <td
                  className={`${tdClass} text-right whitespace-nowrap tabular-nums`}
                >
                  <span
                    className={`font-semibold ${temperatureClass(point.temperatureCelsius)}`}
                  >
                    {point.temperatureCelsius.toFixed(0)}°
                  </span>
                  <span className="text-xs text-gray-500">
                    {" "}
                    feels {point.apparentTemperatureCelsius.toFixed(0)}°
                  </span>
                </td>
                <td
                  className={`${tdClass} text-right whitespace-nowrap tabular-nums`}
                >
                  <span
                    className={
                      point.precipitationProbabilityPercent >= 50
                        ? "font-semibold text-blue-700"
                        : ""
                    }
                  >
                    {point.precipitationProbabilityPercent}%
                  </span>
                  <span className="text-xs text-gray-500">
                    {" "}
                    {point.precipitationMm.toFixed(1)} mm
                  </span>
                  {point.snowfallCm > 0 && (
                    <span className="block text-xs text-sky-700">
                      {point.snowfallCm.toFixed(1)} cm snow
                    </span>
                  )}
                </td>
                <td className={tdClass}>
                  <Wind point={point} />
                </td>
                <td
                  className={`${tdClass} text-right tabular-nums ${belowFreezing ? "font-semibold text-sky-700" : ""}`}
                >
                  {point.freezingLevelMeters > 0
                    ? `${Math.round(point.freezingLevelMeters)} m`
                    : "–"}
                </td>
                {snowy && (
                  <>
                    <td className={`${tdClass} text-right tabular-nums`}>
                      {Math.round(point.snowDepthCm)} cm
                    </td>
                    <td
                      className={`${tdClass} text-right tabular-nums ${point.snowfallPast72HoursCm >= HEAVY_NEW_SNOW_CM ? "font-semibold text-sky-700" : ""}`}
                    >
                      {Math.round(point.snowfallPast72HoursCm)} cm
                    </td>
                  </>
                )}
                <td className={`${tdClass} text-right tabular-nums`}>
                  {point.uvIndex.toFixed(0)}
                </td>
                <td className={tdClass}>
                  {point.isDay ? (
                    <SunIcon
                      className="size-4 text-amber-500"
                      aria-label="Day"
                    />
                  ) : (
                    <MoonIcon
                      className="size-4 text-indigo-700"
                      aria-label="Night"
                    />
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
};

interface ForecastResultProps {
  forecast: GetRouteForecastResponse;
  // Whether the forecast is for a ski tour, which gets a verdict.
  skiTour: boolean;
}

const ForecastResult: React.FC<ForecastResultProps> = ({
  forecast,
  skiTour,
}) => {
  const [hoverPathIndex, setHoverPathIndex] = useState<number | null>(null);
  const [hoverPointIndex, setHoverPointIndex] = useState<number | null>(null);
  const pathTuples = useMemo(() => toTuples(forecast.path), [forecast.path]);
  const [first] = forecast.points;
  const markers: ProfileMarker[] = forecast.points.map((point) => ({
    distanceMeters: point.distanceMeters,
    label: formatTime(point.time),
  }));
  const freezingLevels: ProfilePoint[] = forecast.points
    .filter((point) => point.freezingLevelMeters > 0)
    .map((point) => ({
      distanceMeters: point.distanceMeters,
      elevationMeters: point.freezingLevelMeters,
    }));

  let hoverPosition: LatLngTuple | null = null;
  const hoverPoint =
    hoverPathIndex === null ? undefined : forecast.path.at(hoverPathIndex);
  if (hoverPoint !== undefined) {
    hoverPosition = [hoverPoint.lat, hoverPoint.lng];
  } else if (hoverPointIndex !== null) {
    const location = forecast.points[hoverPointIndex]?.location;
    if (location !== undefined) {
      hoverPosition = [location.lat, location.lng];
    }
  }

  return (
    <div className="flex flex-col gap-4">
      {forecast.warnings.length > 0 && (
        <div className="rounded-lg border border-amber-300 bg-amber-50 p-4">
          <h2 className="mb-2 flex items-center gap-2 font-semibold text-amber-900">
            <ExclamationTriangleIcon className="size-5" />
            Warnings
          </h2>
          <ul className="list-disc space-y-1 pl-6 text-sm text-amber-900">
            {forecast.warnings.map((warning) => (
              <li key={warning}>{warning}</li>
            ))}
          </ul>
        </div>
      )}
      <Card>
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-5">
          <Stat
            label="Start"
            value={first === undefined ? "–" : formatClock(first.time)}
          />
          <Stat label="Finish" value={formatClock(forecast.finishTime)} />
          <Stat
            label="Duration"
            value={formatHoursMinutes(forecast.durationSeconds)}
          />
          <Stat label="Distance" value={formatKm(forecast.distanceMeters)} />
          <Stat
            label="Elevation gain"
            value={formatMeters(forecast.elevationGainMeters)}
          />
        </div>
      </Card>
      <div className="overflow-hidden rounded-lg border border-gray-200">
        <MapView className="h-[45vh] min-h-72">
          {pathTuples.length > 1 && (
            <PolylineLayer positions={pathTuples} color="#d2601a" weight={4} />
          )}
          <ForecastMarkers points={forecast.points} />
          <HoverMarker position={hoverPosition} />
          <FitBounds
            positions={pathTuples}
            fitKey={`${forecast.distanceMeters}-${pathTuples.length}`}
          />
        </MapView>
      </div>
      {skiTour && (
        <Card title="Go or no-go">
          <TouringVerdictView verdict={forecast.touringVerdict} />
        </Card>
      )}
      {forecast.avalancheForecast !== undefined && (
        <Card title="Avalanche forecast">
          <AvalancheForecastView
            forecast={forecast.avalancheForecast}
            date={
              first?.time === undefined
                ? undefined
                : toDateString(timestampDate(first.time))
            }
          />
        </Card>
      )}
      <Card title="Profile with forecast times">
        <ElevationProfile
          points={forecast.path}
          markers={markers}
          levelLine={freezingLevels}
          levelLabel="Freezing level"
          onHover={setHoverPathIndex}
        />
      </Card>
      <Card title="Forecast along the route">
        <ForecastTable points={forecast.points} onHover={setHoverPointIndex} />
      </Card>
    </div>
  );
};

/**
 * StartFromParam returns the start in the URL, e.g. "2026-10-14T07:00", if
 * it's a time in the future, or else the default start.
 */
const startFromParam = (value: string | null): string => {
  const parsed = value === null ? null : parseLocalDateTime(value);
  if (value === null || parsed === null || parsed.getTime() <= Date.now()) {
    return defaultStart();
  }
  return value;
};

const ForecastPage: React.FC = () => {
  const [searchParams] = useSearchParams();
  const routeParam = searchParams.get("route") ?? "";
  const routes = useQuery(listRoutes);
  const [mode, setMode] = useState<SourceMode>("route");
  const [routeId, setRouteId] = useState(routeParam);
  const [gpx, setGpx] = useState("");
  const [start, setStart] = useState(() =>
    startFromParam(searchParams.get("start"))
  );
  const [speed, setSpeed] = useState("8");
  const [climb, setClimb] = useState("600");
  const [intervalMinutes, setIntervalMinutes] = useState("60");
  const [skiTour, setSkiTour] = useState(searchParams.get("ski") === "1");
  const [validation, setValidation] = useState("");
  const forecast = useMutation(getRouteForecast);

  const submit = () => {
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
    if (climbValue === null) {
      setValidation("The climbing rate must be a number, or 0 to ignore it.");
      return;
    }
    if (mode === "route" && routeId === "") {
      setValidation("Choose a route.");
      return;
    }
    if (mode === "gpx" && gpx === "") {
      setValidation("Choose a GPX file.");
      return;
    }
    setValidation("");
    forecast.mutate({
      climbMetersPerHour: climbValue,
      intervalMinutes: Number(intervalMinutes),
      skiTour,
      source:
        mode === "route"
          ? { case: "routeId", value: routeId }
          : { case: "gpx", value: gpx },
      speedKmh: speedValue,
      startTime: timestampFromDate(startDate),
    });
  };

  // Fetch the forecast straight away when opened from a route.
  const autoSubmit = useEffectEvent(() => {
    submit();
  });
  const autoSubmitted = useRef(false);
  useEffect(() => {
    if (routeParam !== "" && !autoSubmitted.current) {
      autoSubmitted.current = true;
      autoSubmit();
    }
  }, [routeParam]);

  const routeOptions: SelectOption[] = [{ label: "Choose a route", value: "" }];
  for (const route of routes.data?.routes ?? []) {
    routeOptions.push({
      label:
        route.stats === undefined
          ? route.name
          : `${route.name} (${formatKm(route.stats.distanceMeters)}, ${Math.round(route.stats.elevationGainMeters)} m)`,
      value: route.id,
    });
  }

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Weather along a route"
        subtitle="Hourly forecast for where you will be, when you will be there. Available up to 15 days ahead."
      />
      <Card>
        <form
          className="flex flex-col gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            submit();
          }}
        >
          <div className="flex flex-wrap items-end gap-3">
            <div className="inline-flex overflow-hidden rounded-md ring-1 ring-gray-300">
              <button
                type="button"
                className={`px-3 py-1.5 text-sm ${mode === "route" ? "bg-gray-800 text-white" : "bg-white text-gray-700 hover:bg-gray-50"}`}
                onClick={() => {
                  setMode("route");
                }}
              >
                Saved route
              </button>
              <button
                type="button"
                className={`px-3 py-1.5 text-sm ${mode === "gpx" ? "bg-gray-800 text-white" : "bg-white text-gray-700 hover:bg-gray-50"}`}
                onClick={() => {
                  setMode("gpx");
                }}
              >
                GPX file
              </button>
            </div>
            {mode === "route" ? (
              <SelectField
                label="Route"
                value={routeId}
                options={routeOptions}
                onChange={setRouteId}
                className="min-w-64 grow"
              />
            ) : (
              <label className="flex min-w-64 grow flex-col gap-1">
                <span className="text-xs font-medium text-gray-600">
                  GPX file
                </span>
                <input
                  type="file"
                  accept=".gpx,application/gpx+xml"
                  className="text-sm file:mr-3 file:rounded-md file:border-0 file:bg-brand-50 file:px-3 file:py-1.5 file:text-brand-700 hover:file:bg-brand-100"
                  onChange={(event) => {
                    const file = event.target.files?.[0];
                    if (file !== undefined) {
                      void file.text().then(setGpx);
                    }
                  }}
                />
              </label>
            )}
          </div>
          {mode === "route" && routeId !== "" && (
            <Link
              to={`/routes/${routeId}`}
              className="-mt-2 text-xs text-brand-600 hover:underline"
            >
              View route
            </Link>
          )}
          <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
            <TextField
              label="Start (your local time)"
              type="datetime-local"
              value={start}
              onChange={setStart}
              required
            />
            <TextField
              label="Flat speed (km/h)"
              value={speed}
              onChange={setSpeed}
              inputMode="decimal"
            />
            <TextField
              label="Climbing (m/h, 0 to ignore)"
              value={climb}
              onChange={setClimb}
              inputMode="numeric"
            />
            <SelectField
              label="Forecast points"
              value={intervalMinutes}
              options={INTERVAL_OPTIONS}
              onChange={setIntervalMinutes}
            />
          </div>
          <CheckboxField
            label="Ski tour: check the forecast against my touring limits"
            checked={skiTour}
            onChange={setSkiTour}
          />
          {validation !== "" && (
            <p className="text-sm text-red-700">{validation}</p>
          )}
          <ErrorText error={forecast.error} />
          <div>
            <button
              type="submit"
              className={primaryButton}
              disabled={forecast.isPending}
            >
              {forecast.isPending ? "Fetching forecast…" : "Get forecast"}
            </button>
          </div>
        </form>
      </Card>
      {forecast.data !== undefined && (
        <ForecastResult
          forecast={forecast.data}
          skiTour={forecast.variables?.skiTour === true}
        />
      )}
    </div>
  );
};

export default ForecastPage;
