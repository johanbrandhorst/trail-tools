import type { ProfileRange } from "@components/charts/elevation-profile";
import ElevationProfile from "@components/charts/elevation-profile";
import type { MapPhoto } from "@components/map/photo-markers";
import {
  formatRange,
  sectionSummary,
  trackSlice,
} from "@components/trips/sections";
import type { DistanceRange } from "@components/trips/sections";
import {
  profilePoints,
  Story,
  trackPositions,
} from "@components/trips/trip-bits";
import TripMap from "@components/trips/trip-map";
import ConfirmButton from "@components/ui/confirm-button";
import { ErrorText } from "@components/ui/query-error";
import {
  ghostButton,
  ghostDangerButton,
  inputClass,
  labelClass,
  primaryButton,
  secondaryButton,
} from "@components/ui/styles";
import { useMutation } from "@connectrpc/connect-query";
import { PlusIcon, SparklesIcon } from "@heroicons/react/24/outline";
import {
  setRaceReportSegments,
  suggestRaceReportSegments,
} from "gen/races/v1/race_reports-RaceReportService_connectquery";
import type {
  RaceReport,
  RaceReportSegment,
} from "gen/races/v1/race_reports_pb";
import type { RoutePoint } from "gen/routes/v1/routes_pb";
import type { PrivacyZone } from "gen/users/v1/users_pb";
import type { LatLng } from "leaflet";
import React, { useEffect, useMemo, useRef, useState } from "react";

// NEW_SEGMENT_METERS is the length of a new segment.
const NEW_SEGMENT_METERS = 1000;

// Draft is a segment being edited, with its distances as kilometer inputs.
interface Draft {
  key: string;
  title: string;
  body: string;
  start: string;
  end: string;
}

let nextKey = 0;

// NewKey returns a key for a new draft.
const newKey = (): string => {
  nextKey += 1;
  return `segment-${String(nextKey)}`;
};

// KmText formats meters as kilometers for an input.
const kmText = (meters: number): string =>
  String(Math.round(meters / 10) / 100);

// ToDraft converts a segment to a draft.
const toDraft = (segment: RaceReportSegment): Draft => ({
  body: segment.body,
  end: kmText(segment.endDistanceMeters),
  key: newKey(),
  start: kmText(segment.startDistanceMeters),
  title: segment.title,
});

// DraftRange returns the stretch of a draft, if its inputs are valid.
const draftRange = (draft: Draft): DistanceRange | undefined => {
  const start = Number(draft.start.replace(",", ".")) * 1000;
  const end = Number(draft.end.replace(",", ".")) * 1000;
  if (
    !Number.isFinite(start) ||
    !Number.isFinite(end) ||
    start < 0 ||
    end <= start
  ) {
    return undefined;
  }
  return { endMeters: end, startMeters: start };
};

// RangeOf returns the stretch of a segment.
const rangeOf = (segment: RaceReportSegment): DistanceRange => ({
  endMeters: segment.endDistanceMeters,
  startMeters: segment.startDistanceMeters,
});

// TrackLength returns the length of a track in meters.
const trackLength = (track: RoutePoint[]): number =>
  track.at(-1)?.distanceMeters ?? 0;

interface SegmentsEditorProps {
  report: RaceReport;
  track: RoutePoint[];
  // The user's privacy zones, shown on the map.
  privacyZones: PrivacyZone[];
  // The report's photos with a location, shown on the map.
  photos: MapPhoto[];
  // Called when a photo is dragged on the map.
  onMovePhoto: (id: string, position: LatLng) => void;
  // The photo being placed by clicking the map, if any.
  placing?: PlacingPhoto | undefined;
  onSaved: () => Promise<void>;
}

// PlacingPhoto is a photo being placed by clicking the map.
export interface PlacingPhoto {
  // The photo's number, as on the map.
  number: number;
  onPlace: (position: LatLng) => void;
  onCancel: () => void;
}

/**
 * SegmentsEditor edits the segments of a race report: stretches of its track
 * with a title and text, shown on a map and elevation profile. The segment
 * being edited is highlighted on both, and clicking the profile moves its
 * nearer end there.
 */
export const SegmentsEditor: React.FC<SegmentsEditorProps> = ({
  report,
  track,
  privacyZones,
  photos,
  onMovePhoto,
  placing,
  onSaved,
}) => {
  const mapRef = useRef<HTMLDivElement>(null);
  const isPlacing = placing !== undefined;
  // The map is above the photos, so bring it into view to place one.
  useEffect(() => {
    if (isPlacing) {
      mapRef.current?.scrollIntoView({ behavior: "smooth", block: "center" });
    }
  }, [isPlacing]);
  const [drafts, setDrafts] = useState<Draft[]>(() =>
    report.segments.map(toDraft)
  );
  const [activeKey, setActiveKey] = useState("");
  const [hoverIndex, setHoverIndex] = useState<number | null>(null);
  const [validation, setValidation] = useState("");
  const save = useMutation(setRaceReportSegments);
  const suggest = useMutation(suggestRaceReportSegments, {
    onSuccess: (response) => {
      setDrafts(response.segments.map(toDraft));
      setActiveKey("");
    },
  });
  const length = trackLength(track);
  const positions = useMemo(() => trackPositions(track), [track]);
  const points = useMemo(() => profilePoints(track), [track]);
  const active = drafts.find((draft) => draft.key === activeKey);
  const activeRange = active === undefined ? undefined : draftRange(active);

  const ranges: ProfileRange[] = drafts.flatMap((draft) => {
    const range = draftRange(draft);
    return range === undefined
      ? []
      : [{ ...range, active: draft.key === activeKey, label: draft.title }];
  });

  const update = (key: string, change: Partial<Draft>): void => {
    setDrafts(
      drafts.map((draft) =>
        draft.key === key ? { ...draft, ...change } : draft
      )
    );
  };

  const add = (): void => {
    const last = drafts.at(-1);
    const lastRange = last === undefined ? undefined : draftRange(last);
    const start = Math.min(lastRange?.endMeters ?? 0, Math.max(length - 1, 0));
    const draft: Draft = {
      body: "",
      end: kmText(Math.min(start + NEW_SEGMENT_METERS, length)),
      key: newKey(),
      start: kmText(start),
      title: "",
    };
    setDrafts([...drafts, draft]);
    setActiveKey(draft.key);
  };

  const submit = (): void => {
    const segments: {
      title: string;
      body: string;
      startDistanceMeters: number;
      endDistanceMeters: number;
    }[] = [];
    for (const [index, draft] of drafts.entries()) {
      const range = draftRange(draft);
      if (range === undefined) {
        setValidation(
          `Segment ${index + 1} needs a start and an end after it, in km.`
        );
        return;
      }
      if (range.endMeters > length + 5) {
        setValidation(
          `Segment ${index + 1} ends after the track, which is ${(length / 1000).toFixed(1)} km.`
        );
        return;
      }
      segments.push({
        body: draft.body,
        endDistanceMeters: Math.min(range.endMeters, length),
        startDistanceMeters: range.startMeters,
        title: draft.title,
      });
    }
    setValidation("");
    // In order along the track, as the server requires.
    segments.sort(
      (first, second) => first.startDistanceMeters - second.startDistanceMeters
    );
    void (async () => {
      await save.mutateAsync({ reportId: report.id, segments });
      await onSaved();
    })().catch(() => {
      // The error is shown below.
    });
  };

  const hovered = hoverIndex === null ? null : track[hoverIndex];
  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-gray-600">
        Comment on stretches of the course, like the climb where your legs went
        or the descent where you made up time. They're highlighted on the map
        and profile of your race.
      </p>
      {placing !== undefined && (
        <p className="flex items-center justify-between gap-2 rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-900">
          Click the map where photo {placing.number} was taken.
          <button
            type="button"
            className={ghostButton}
            onClick={placing.onCancel}
          >
            Cancel
          </button>
        </p>
      )}
      <div ref={mapRef}>
        <TripMap
          track={positions}
          photos={photos}
          selectedId={null}
          onMovePhoto={onMovePhoto}
          onMapClick={placing?.onPlace}
          privacyZones={privacyZones}
          hoverPosition={
            hovered === undefined || hovered === null
              ? null
              : [hovered.lat, hovered.lng]
          }
          highlight={
            activeRange === undefined ? [] : trackSlice(track, activeRange)
          }
          highlightKey={activeKey}
          fitKey={report.id}
          className="h-72"
        />
      </div>
      {points.length > 0 && (
        <ElevationProfile
          points={points}
          ranges={ranges}
          onHover={setHoverIndex}
          onSelectDistance={
            active === undefined || activeRange === undefined
              ? undefined
              : (distance) => {
                  // Move the nearer end of the active segment.
                  const toStart = Math.abs(distance - activeRange.startMeters);
                  const toEnd = Math.abs(distance - activeRange.endMeters);
                  update(
                    active.key,
                    toStart < toEnd
                      ? { start: kmText(distance) }
                      : { end: kmText(distance) }
                  );
                }
          }
        />
      )}
      {active !== undefined && (
        <p className="text-xs text-gray-500">
          Click the profile to move the nearer end of the segment you're
          editing.
        </p>
      )}
      <ol className="flex flex-col gap-3">
        {drafts.map((draft, index) => {
          const range = draftRange(draft);
          return (
            <li
              key={draft.key}
              className={`flex flex-col gap-2 rounded-md border p-3 ${draft.key === activeKey ? "border-ember" : "border-gray-200"}`}
              onFocus={() => {
                setActiveKey(draft.key);
              }}
              onClick={() => {
                setActiveKey(draft.key);
              }}
            >
              <div className="flex flex-wrap items-end gap-2">
                <label className="min-w-0 flex-1">
                  <span className={labelClass}>Segment {index + 1}</span>
                  <input
                    className={inputClass}
                    value={draft.title}
                    placeholder="e.g. The climb to the ridge"
                    onChange={(event) => {
                      update(draft.key, { title: event.target.value });
                    }}
                  />
                </label>
                <label className="w-20">
                  <span className={labelClass}>From km</span>
                  <input
                    className={inputClass}
                    inputMode="decimal"
                    value={draft.start}
                    onChange={(event) => {
                      update(draft.key, { start: event.target.value });
                    }}
                  />
                </label>
                <label className="w-20">
                  <span className={labelClass}>To km</span>
                  <input
                    className={inputClass}
                    inputMode="decimal"
                    value={draft.end}
                    onChange={(event) => {
                      update(draft.key, { end: event.target.value });
                    }}
                  />
                </label>
                <button
                  type="button"
                  className={ghostDangerButton}
                  onClick={() => {
                    setDrafts(
                      drafts.filter((other) => other.key !== draft.key)
                    );
                  }}
                >
                  Remove
                </button>
              </div>
              {range !== undefined && (
                <p className="text-xs text-gray-500 tabular-nums">
                  {sectionSummary(track, range)}
                </p>
              )}
              <textarea
                className={inputClass}
                rows={3}
                value={draft.body}
                placeholder="How did this stretch go?"
                aria-label={`Segment ${index + 1} text`}
                onChange={(event) => {
                  update(draft.key, { body: event.target.value });
                }}
              />
            </li>
          );
        })}
      </ol>
      <div className="flex flex-wrap items-end gap-2">
        <button type="button" className={secondaryButton} onClick={add}>
          <PlusIcon className="size-4" />
          Add a segment
        </button>
        {drafts.length === 0 ? (
          <button
            type="button"
            className={ghostButton}
            disabled={suggest.isPending}
            onClick={() => {
              suggest.mutate({ reportId: report.id });
            }}
          >
            <SparklesIcon className="size-4" />
            Suggest from the climbs
          </button>
        ) : (
          <ConfirmButton
            className={ghostButton}
            title="Replace your segments?"
            message="Your segments are replaced with ones suggested from the climbs and descents, without text."
            confirmLabel="Replace"
            disabled={suggest.isPending}
            onConfirm={() => {
              suggest.mutate({ reportId: report.id });
            }}
          >
            <SparklesIcon className="size-4" />
            Suggest from the climbs
          </ConfirmButton>
        )}
        <button
          type="button"
          className={`${primaryButton} ml-auto`}
          disabled={save.isPending}
          onClick={submit}
        >
          {save.isPending ? "Saving…" : "Save segments"}
        </button>
      </div>
      {validation !== "" && (
        <p className="text-sm text-red-700">{validation}</p>
      )}
      <ErrorText error={save.error ?? suggest.error} />
    </div>
  );
};

interface SegmentsViewProps {
  segments: RaceReportSegment[];
  // The report's photos with a location, shown on the map.
  photos: MapPhoto[];
  // Called when a photo's marker is clicked.
  onSelectPhoto: (id: string) => void;
  // The track, which the segments are distances along.
  track: RoutePoint[];
  /**
   * The indexes of the track's points that start a new piece after a
   * stretch hidden by privacy zones.
   */
  breaks: number[];
}

/**
 * SegmentsView shows a race report's segments with the map and elevation
 * profile of its track, highlighting the segment picked.
 */
export const SegmentsView: React.FC<SegmentsViewProps> = ({
  segments,
  photos,
  onSelectPhoto,
  track,
  breaks,
}) => {
  const [activeIndex, setActiveIndex] = useState(0);
  const [hoverIndex, setHoverIndex] = useState<number | null>(null);
  const positions = useMemo(() => trackPositions(track), [track]);
  const points = useMemo(() => profilePoints(track), [track]);
  if (segments.length === 0 || track.length < 2) {
    return null;
  }
  const active = segments[activeIndex];
  const hovered = hoverIndex === null ? undefined : track[hoverIndex];
  return (
    <div className="flex flex-col gap-4">
      <TripMap
        track={positions}
        photos={photos}
        selectedId={null}
        onSelectPhoto={onSelectPhoto}
        hoverPosition={
          hovered === undefined ? null : [hovered.lat, hovered.lng]
        }
        breaks={breaks}
        highlight={
          active === undefined ? [] : trackSlice(track, rangeOf(active), breaks)
        }
        highlightKey={String(activeIndex)}
        fitKey="segments"
        className="h-80"
      />
      {points.length > 0 && (
        <ElevationProfile
          points={points}
          onHover={setHoverIndex}
          ranges={segments.map((segment, index) => ({
            ...rangeOf(segment),
            active: index === activeIndex,
            label: segment.title,
          }))}
        />
      )}
      <ol className="flex flex-col gap-6">
        {segments.map((segment, index) => (
          <li
            key={`${String(index)}-${segment.title}`}
            className={`rounded-md border-l-4 pl-4 ${index === activeIndex ? "border-ember" : "border-gray-200"}`}
          >
            <button
              type="button"
              className="text-left"
              onClick={() => {
                setActiveIndex(index);
              }}
            >
              <h3 className="text-xl font-semibold text-gray-900">
                {segment.title === ""
                  ? formatRange(rangeOf(segment))
                  : segment.title}
              </h3>
              <p className="text-sm text-gray-500 tabular-nums">
                {sectionSummary(track, rangeOf(segment))}
              </p>
            </button>
            <div className="mt-2">
              <Story body={segment.body} />
            </div>
          </li>
        ))}
      </ol>
    </div>
  );
};
