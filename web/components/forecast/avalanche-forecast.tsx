import { timestampDate } from "@bufbuild/protobuf/wkt";
import { linkClass } from "@components/ui/styles";
import { ArrowTopRightOnSquareIcon } from "@heroicons/react/24/outline";
import type {
  AvalancheDangerDay,
  AvalancheDangerRating,
  AvalancheForecast,
  AvalancheProblem,
} from "gen/geo/v1/geo_pb";
import { AvalancheDanger } from "gen/geo/v1/geo_pb";
import { parseDate } from "helpers/dates";
import React from "react";

/**
 * DangerClass is the color of a danger rating, in the colors of the North
 * American Public Avalanche Danger Scale that forecasts are drawn in.
 */
const dangerClass = (danger: AvalancheDanger): string => {
  switch (danger) {
    case AvalancheDanger.LOW: {
      return "bg-[#52ba4a] text-white";
    }
    case AvalancheDanger.MODERATE: {
      return "bg-[#fff300] text-gray-900";
    }
    case AvalancheDanger.CONSIDERABLE: {
      return "bg-[#f79218] text-gray-900";
    }
    case AvalancheDanger.HIGH: {
      return "bg-[#ef1c29] text-white";
    }
    case AvalancheDanger.EXTREME: {
      return "bg-[#1a1a1a] text-white";
    }
    default: {
      return "bg-gray-100 text-gray-600";
    }
  }
};

// DangerCell shows a band's rating on a day.
const DangerCell: React.FC<{
  rating: AvalancheDangerRating | undefined;
  selected: boolean;
}> = ({ rating, selected }) => (
  <td className="p-0.5">
    <span
      className={`block rounded px-1.5 py-1 text-center text-xs font-medium ${dangerClass(rating?.danger ?? AvalancheDanger.UNSPECIFIED)} ${selected ? "ring-2 ring-gray-900 ring-offset-1" : ""}`}
    >
      {rating?.description === undefined || rating.description === ""
        ? "–"
        : rating.description}
    </span>
  </td>
);

const BANDS: {
  label: string;
  rating: (day: AvalancheDangerDay) => AvalancheDangerRating | undefined;
}[] = [
  { label: "Alpine", rating: (day) => day.alpine },
  { label: "Treeline", rating: (day) => day.treeline },
  { label: "Below treeline", rating: (day) => day.belowTreeline },
];

// FormatDay names a forecast's day, e.g. "Sat, Feb 14".
const formatDay = (date: string): string =>
  parseDate(date).toLocaleDateString(undefined, {
    day: "numeric",
    month: "short",
    weekday: "short",
  });

// FormatSize formats the expected sizes of a problem, e.g. "1–2.5".
const formatSize = (problem: AvalancheProblem): string => {
  if (problem.maxSize === 0) {
    return "";
  }
  if (problem.minSize === 0 || problem.minSize === problem.maxSize) {
    return String(problem.maxSize);
  }
  return `${problem.minSize}–${problem.maxSize}`;
};

// Problem describes an avalanche problem in a line.
const Problem: React.FC<{ problem: AvalancheProblem }> = ({ problem }) => {
  const details = [
    problem.likelihood,
    formatSize(problem) === "" ? "" : `size ${formatSize(problem)}`,
    problem.elevations.join(", "),
    problem.aspects.join(" "),
  ].filter((detail) => detail !== "");
  return (
    <li>
      <span className="font-medium text-gray-900">{problem.name}</span>
      {details.length > 0 && (
        <span className="text-gray-600"> · {details.join(" · ")}</span>
      )}
    </li>
  );
};

interface AvalancheForecastViewProps {
  forecast: AvalancheForecast;
  // The day to highlight (YYYY-MM-DD), like the day of a ski tour.
  date?: string | undefined;
}

/**
 * AvalancheForecastView shows an avalanche forecast: the danger of each
 * elevation band over the coming days, the problems to expect and the
 * forecast's summary, credited to its forecaster with a link to the full
 * forecast on avalanche.ca.
 */
const AvalancheForecastView: React.FC<AvalancheForecastViewProps> = ({
  forecast,
  date,
}) => {
  const covered =
    date === undefined || forecast.days.some((day) => day.date === date);
  return (
    <div className="flex flex-col gap-3 text-sm">
      {!covered && (
        <p className="text-gray-600">
          The forecast doesn't reach {formatDay(date ?? "")} yet: these are the
          next days. Check again closer to the day.
        </p>
      )}
      <div className="overflow-x-auto">
        <table className="w-full min-w-80 table-fixed border-separate border-spacing-0 tabular-nums">
          <thead>
            <tr>
              <th className="w-28" />
              {forecast.days.map((day) => (
                <th
                  key={day.date}
                  scope="col"
                  className={`pb-1 text-xs font-medium ${day.date === date ? "text-gray-900" : "text-gray-500"}`}
                >
                  {formatDay(day.date)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {BANDS.map((band) => (
              <tr key={band.label}>
                <th
                  scope="row"
                  className="pr-2 text-left text-xs font-medium text-gray-600"
                >
                  {band.label}
                </th>
                {forecast.days.map((day) => (
                  <DangerCell
                    key={day.date}
                    rating={band.rating(day)}
                    selected={day.date === date}
                  />
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {forecast.problems.length > 0 && (
        <div>
          <h3 className="text-xs font-medium text-gray-500">Problems</h3>
          <ul className="mt-1 flex flex-col gap-1">
            {forecast.problems.map((problem) => (
              <Problem key={problem.type} problem={problem} />
            ))}
          </ul>
        </div>
      )}
      {forecast.highlights !== "" && (
        <p className="whitespace-pre-line text-gray-700">
          {forecast.highlights}
        </p>
      )}
      <p className="text-xs text-gray-500">
        {forecast.title} forecast by {forecast.forecaster}
        {forecast.issueTime !== undefined &&
          `, issued ${timestampDate(forecast.issueTime).toLocaleString(undefined, { day: "numeric", hour: "2-digit", minute: "2-digit", month: "short" })}`}
        , from Avalanche Canada.{" "}
        <a
          href={forecast.url}
          target="_blank"
          rel="noreferrer"
          className={`${linkClass} inline-flex items-center gap-0.5`}
        >
          Read the full forecast
          <ArrowTopRightOnSquareIcon className="size-3" aria-hidden="true" />
        </a>
      </p>
    </div>
  );
};

export default AvalancheForecastView;
