import type { ProfilePoint } from "@components/charts/elevation-profile";
import type { MapPhoto } from "@components/map/photo-markers";
import { TagList } from "@components/trips/tags";
import { Badge } from "@components/ui/misc";
import { cardClass } from "@components/ui/styles";
import { MapIcon, PhotoIcon } from "@heroicons/react/24/outline";
import type { RoutePoint } from "gen/routes/v1/routes_pb";
import type { TripReport, TripStats } from "gen/trips/v1/trips_pb";
import { LocationSource } from "gen/trips/v1/trips_pb";
import {
  formatDate,
  formatHoursMinutes,
  formatKm,
  formatMeters,
} from "helpers/format";
import { sportLabel } from "helpers/labels";
import type { LatLngTuple } from "leaflet";
import React from "react";
import { Link } from "react-router";

export const trackPositions = (track: RoutePoint[]): LatLngTuple[] =>
  track.map((point) => [point.lat, point.lng]);

/**
 * ProfilePoints returns the elevation profile of a track, or no points if
 * the track has no elevations.
 */
export const profilePoints = (track: RoutePoint[]): ProfilePoint[] => {
  if (!track.some((point) => point.elevationMeters !== 0)) {
    return [];
  }
  return track.map((point) => ({
    distanceMeters: point.distanceMeters,
    elevationMeters: point.elevationMeters,
  }));
};

/**
 * PhotoOnMap is what's needed to show a photo of a trip or race report on a
 * map.
 */
export interface PhotoOnMap {
  id: string;
  caption: string;
  thumbnailUrl: string;
  location?: { lat: number; lng: number } | undefined;
}

// MapPhotos returns the photos with a location, numbered in report order.
export const mapPhotos = (photos: PhotoOnMap[]): MapPhoto[] => {
  const result: MapPhoto[] = [];
  for (const [index, photo] of photos.entries()) {
    if (photo.location !== undefined) {
      result.push({
        caption: photo.caption,
        id: photo.id,
        number: index + 1,
        position: [photo.location.lat, photo.location.lng],
        thumbnailUrl: photo.thumbnailUrl,
      });
    }
  }
  return result;
};

export const locationSourceLabel = (source: LocationSource): string => {
  switch (source) {
    case LocationSource.GPS: {
      return "Placed from the photo's GPS position";
    }
    case LocationSource.TRACK_TIME: {
      return "Placed on the track at the time it was taken";
    }
    case LocationSource.MANUAL: {
      return "Placed by hand";
    }
    case LocationSource.UNSPECIFIED: {
      return "Not on the map";
    }
    default: {
      return "";
    }
  }
};

interface StatItem {
  label: string;
  value: string;
}

const statItems = (stats: TripStats): StatItem[] => {
  const items: StatItem[] = [];
  if (stats.distanceMeters > 0) {
    items.push({ label: "Distance", value: formatKm(stats.distanceMeters) });
  }
  if (stats.elevationGainMeters > 0) {
    items.push({
      label: "Elevation gain",
      value: formatMeters(stats.elevationGainMeters),
    });
  }
  if (stats.movingTimeSeconds > 0) {
    items.push({
      label: "Moving time",
      value: formatHoursMinutes(stats.movingTimeSeconds),
    });
  }
  if (stats.elapsedTimeSeconds > 0) {
    items.push({
      label: "Total time",
      value: formatHoursMinutes(stats.elapsedTimeSeconds),
    });
  }
  return items;
};

interface TripStatsRowProps {
  stats: TripStats | undefined;
}

// TripStatsRow shows the headline statistics of a trip.
export const TripStatsRow: React.FC<TripStatsRowProps> = ({ stats }) => {
  if (stats === undefined) {
    return null;
  }
  const items = statItems(stats);
  if (items.length === 0) {
    return null;
  }
  return (
    <dl className="grid grid-cols-2 gap-px overflow-hidden rounded-xl bg-gray-200 ring-1 ring-gray-200 sm:grid-cols-4">
      {items.map((item) => (
        <div key={item.label} className="bg-white px-4 py-3">
          <dt className="text-xs font-medium tracking-wide text-gray-500 uppercase">
            {item.label}
          </dt>
          <dd className="mt-0.5 text-xl font-semibold text-gray-900 tabular-nums">
            {item.value}
          </dd>
        </div>
      ))}
    </dl>
  );
};

interface StoryProps {
  body: string;
}

// Story renders plain text with paragraphs separated by blank lines.
export const Story: React.FC<StoryProps> = ({ body }) => {
  const paragraphs = body
    .split(/\n\s*\n/u)
    .map((paragraph) => paragraph.trim())
    .filter((paragraph) => paragraph !== "");
  if (paragraphs.length === 0) {
    return null;
  }
  return (
    <div className="flex flex-col gap-5 text-lg leading-8 text-gray-800">
      {paragraphs.map((paragraph, index) => (
        <p
          key={`${String(index)}-${paragraph.slice(0, 16)}`}
          className="whitespace-pre-line"
        >
          {paragraph}
        </p>
      ))}
    </div>
  );
};

// TripKicker is the line above a report's title: the sport and the date.
export const tripKicker = (report: TripReport): string => {
  const parts: string[] = [];
  if (report.stats !== undefined && report.stats.sportType !== "") {
    parts.push(sportLabel(report.stats.sportType));
  }
  if (report.date !== "") {
    parts.push(formatDate(report.date));
  }
  return parts.join(" · ");
};

interface ReportCardProps {
  report: TripReport;
  to: string;
  // Show whether the report is a draft.
  showStatus?: boolean;
}

// ReportCard shows a trip report in a listing, with its cover photo.
export const ReportCard: React.FC<ReportCardProps> = ({
  report,
  to,
  showStatus = false,
}) => {
  const cover =
    report.photos.find((photo) => photo.id === report.coverPhotoId) ??
    report.photos.at(0);
  const stats = report.stats === undefined ? [] : statItems(report.stats);
  return (
    <Link
      to={to}
      className={`${cardClass} group flex flex-col overflow-hidden hover:border-brand-400`}
    >
      <div className="relative aspect-[16/9] bg-stone-100">
        {cover === undefined ? (
          <div className="flex h-full items-center justify-center text-stone-400">
            {report.hasTrack ? (
              <MapIcon className="size-12" />
            ) : (
              <PhotoIcon className="size-12" />
            )}
          </div>
        ) : (
          <img
            src={cover.thumbnailUrl}
            alt={cover.caption}
            loading="lazy"
            className="h-full w-full object-cover transition duration-300 group-hover:scale-[1.02]"
          />
        )}
        {showStatus && (
          <div className="absolute top-2 left-2">
            {report.published ? (
              <Badge className="bg-emerald-50 text-emerald-800 ring-emerald-300">
                Published
              </Badge>
            ) : (
              <Badge className="bg-white text-gray-700 ring-gray-300">
                Draft
              </Badge>
            )}
          </div>
        )}
      </div>
      <div className="flex grow flex-col gap-1.5 p-4">
        <p className="text-xs font-semibold tracking-wide text-brand-600 uppercase">
          {tripKicker(report)}
        </p>
        <h2 className="text-lg leading-snug font-semibold text-gray-900 group-hover:text-brand-700">
          {report.title}
        </h2>
        {stats.length > 0 && (
          <p className="text-sm text-gray-600 tabular-nums">
            {stats
              .slice(0, 3)
              .map((item) => item.value)
              .join(" · ")}
          </p>
        )}
        {report.excerpt !== "" && (
          <p className="line-clamp-3 text-sm text-gray-500">{report.excerpt}</p>
        )}
        <TagList tags={report.tags} className="mt-auto pt-2" />
      </div>
    </Link>
  );
};
