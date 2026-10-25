import type { DraftSection } from "@components/trips/section-draft";
import { draftRange } from "@components/trips/section-draft";
import { climbWithin } from "@components/trips/sections";
import { locationSourceLabel } from "@components/trips/trip-bits";
import ConfirmButton from "@components/ui/confirm-button";
import { ErrorText } from "@components/ui/query-error";
import { ghostButton, iconButton, inputClass } from "@components/ui/styles";
import { useMutation } from "@connectrpc/connect-query";
import {
  ArrowDownIcon,
  ArrowUpIcon,
  ArrowsPointingInIcon,
  Bars2Icon,
  MapPinIcon,
  ScissorsIcon,
  StarIcon,
  TrashIcon,
  XMarkIcon,
} from "@heroicons/react/24/outline";
import { StarIcon as SolidStarIcon } from "@heroicons/react/24/solid";
import type { RoutePoint } from "gen/routes/v1/routes_pb";
import {
  deleteTripPhoto,
  updateTripPhoto,
} from "gen/trips/v1/trips-TripReportService_connectquery";
import type { TripPhoto } from "gen/trips/v1/trips_pb";
import { formatMeters } from "helpers/format";
import type { DragEvent } from "react";
import React, { useRef, useState } from "react";

/**
 * PHOTO_DRAG_TYPE is the type of the data of a dragged photo row: the
 * photo's ID. Types are lowercase, as browsers lowercase them.
 */
const PHOTO_DRAG_TYPE = "application/x-trailrunning-photo";

// IsPhotoDrag reports whether a drag is of a photo row.
const isPhotoDrag = (event: DragEvent): boolean =>
  event.dataTransfer.types.includes(PHOTO_DRAG_TYPE);

interface KmInputProps {
  label: string;
  meters: number | undefined;
  onChange: (meters: number | undefined) => void;
}

// FormatKmValue formats meters as kilometers for an input.
const formatKmValue = (meters: number | undefined): string =>
  meters === undefined ? "" : String(Math.round(meters / 100) / 10);

/**
 * KmInput edits a distance in kilometers. It's empty for no distance. The
 * distance changes when the input loses focus or on Enter, not on each
 * keystroke, so neighboring sections adjust to the finished number.
 */
const KmInput: React.FC<KmInputProps> = ({ label, meters, onChange }) => {
  const [text, setText] = useState(formatKmValue(meters));
  const [shown, setShown] = useState(meters);
  // Follow changes made elsewhere, e.g. by splitting the section.
  if (shown !== meters) {
    setShown(meters);
    setText(formatKmValue(meters));
  }
  const commit = () => {
    const value = Number.parseFloat(text);
    const next = Number.isFinite(value) ? Math.round(value * 1000) : undefined;
    if (next !== meters) {
      onChange(next);
    }
  };
  return (
    <span className="w-20">
      <input
        type="number"
        inputMode="decimal"
        min={0}
        step={0.1}
        value={text}
        aria-label={label}
        placeholder="km"
        className={`${inputClass} py-1 tabular-nums`}
        onChange={(event) => {
          setText(event.target.value);
        }}
        onBlur={commit}
        onKeyDown={(event) => {
          if (event.key === "Enter") {
            // Commit instead of submitting the form with the old distance.
            event.preventDefault();
            commit();
          }
        }}
      />
    </span>
  );
};

interface PhotoRowProps {
  photo: TripPhoto;
  number: number;
  first: boolean;
  last: boolean;
  selected: boolean;
  placing: boolean;
  // Whether the photo is the report's cover photo.
  cover: boolean;
  // The other sections the photo can move to.
  sections: { key: string; label: string }[];
  // Where a photo being dragged would drop next to this one, if here.
  dropEdge: "before" | "after" | null;
  onSelect: () => void;
  // Shows the photo full screen.
  onOpen: () => void;
  onPlace: () => void;
  /**
   * Called while a photo is dragged over this one, with whether it would
   * drop after it rather than before.
   */
  onDragOver: (after: boolean) => void;
  onMove: (delta: number) => void;
  onMoveToSection: (key: string) => void;
  onSetCover: () => void;
  onChanged: () => Promise<void>;
}

/**
 * PhotoRow edits a photo: its caption and place on the map save right away,
 * and its place in the story is part of the draft.
 */
const PhotoRow: React.FC<PhotoRowProps> = ({
  photo,
  number,
  first,
  last,
  selected,
  placing,
  cover,
  sections,
  dropEdge,
  onSelect,
  onOpen,
  onPlace,
  onDragOver,
  onMove,
  onMoveToSection,
  onSetCover,
  onChanged,
}) => {
  const row = useRef<HTMLLIElement>(null);
  const [caption, setCaption] = useState(photo.caption);
  const update = useMutation(updateTripPhoto, { onSuccess: onChanged });
  const remove = useMutation(deleteTripPhoto, { onSuccess: onChanged });
  const located = photo.location !== undefined;
  let placeLabel = "Place on map";
  if (placing) {
    placeLabel = "Click the map…";
  } else if (located) {
    placeLabel = "Move";
  }

  return (
    <li
      ref={row}
      data-photo-row
      className={`flex gap-3 rounded-lg p-2 ${selected ? "bg-amber-50 ring-2 ring-amber-300" : ""} ${dropEdge === "before" ? "shadow-[0_-3px_0_var(--color-brand-600)]" : ""} ${dropEdge === "after" ? "shadow-[0_3px_0_var(--color-brand-600)]" : ""}`}
      onDragOver={(event) => {
        if (!isPhotoDrag(event)) {
          return;
        }
        event.preventDefault();
        event.dataTransfer.dropEffect = "move";
        const rect = event.currentTarget.getBoundingClientRect();
        onDragOver(event.clientY > rect.top + rect.height / 2);
      }}
    >
      {/* Only the handle drags, so the caption's text can be selected. */}
      <span
        draggable
        role="img"
        aria-label="Drag to reorder"
        title="Drag to reorder"
        className="-mx-1 hidden cursor-grab self-center p-1 text-gray-400 hover:text-gray-700 active:cursor-grabbing sm:block"
        onDragStart={(event) => {
          event.dataTransfer.setData(PHOTO_DRAG_TYPE, photo.id);
          event.dataTransfer.effectAllowed = "move";
          if (row.current !== null) {
            event.dataTransfer.setDragImage(row.current, 24, 24);
          }
        }}
      >
        <Bars2Icon className="size-4" />
      </span>
      <button
        type="button"
        className="relative shrink-0 cursor-zoom-in self-start"
        onClick={() => {
          onSelect();
          onOpen();
        }}
        aria-label={`Enlarge photo ${number}`}
        title="Enlarge"
      >
        <img
          src={photo.thumbnailUrl}
          alt=""
          className="size-20 rounded-md object-cover sm:size-24"
        />
        <span className="absolute -top-1.5 -left-1.5 inline-flex size-5 items-center justify-center rounded-full border-2 border-white bg-ink text-[10px] font-bold text-white">
          {number}
        </span>
        {cover && (
          <span className="absolute inset-x-0 bottom-0 rounded-b-md bg-ink/80 py-0.5 text-center text-[10px] font-semibold tracking-wide text-white uppercase">
            Cover
          </span>
        )}
      </button>
      <div className="flex min-w-0 grow flex-col gap-1.5">
        <textarea
          value={caption}
          rows={2}
          placeholder="Caption"
          className={`${inputClass} resize-none`}
          onFocus={onSelect}
          onChange={(event) => {
            setCaption(event.target.value);
          }}
          onBlur={() => {
            if (caption.trim() !== photo.caption) {
              update.mutate({ caption, id: photo.id });
            }
          }}
        />
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span
            className={`inline-flex items-center gap-1 text-xs ${located ? "text-gray-500" : "text-amber-700"}`}
            title={locationSourceLabel(photo.locationSource)}
          >
            <MapPinIcon className="size-3.5" />
            {photo.trackDistanceMeters === undefined
              ? locationSourceLabel(photo.locationSource)
              : `km ${(photo.trackDistanceMeters / 1000).toFixed(1)}`}
          </span>
          <span className="ml-auto flex flex-wrap items-center">
            {sections.length > 0 && (
              <select
                value=""
                aria-label="Move to section"
                className="mr-1 rounded-md border-0 bg-transparent py-0.5 pr-6 pl-1 text-xs text-gray-600 hover:bg-gray-100"
                onChange={(event) => {
                  if (event.target.value !== "") {
                    onMoveToSection(event.target.value);
                  }
                }}
              >
                <option value="">Move to…</option>
                {sections.map((section) => (
                  <option key={section.key} value={section.key}>
                    {section.label}
                  </option>
                ))}
              </select>
            )}
            <button
              type="button"
              className={`${ghostButton} text-xs`}
              onClick={onPlace}
            >
              {placeLabel}
            </button>
            {located && (
              <button
                type="button"
                className={iconButton}
                title="Remove from the map"
                aria-label="Remove from the map"
                disabled={update.isPending}
                onClick={() => {
                  update.mutate({ clearLocation: true, id: photo.id });
                }}
              >
                <XMarkIcon className="size-4" />
              </button>
            )}
            <button
              type="button"
              className={`${iconButton} ${cover ? "text-ember" : ""}`}
              title={cover ? "The cover photo" : "Use as the cover photo"}
              aria-label={cover ? "The cover photo" : "Use as the cover photo"}
              aria-pressed={cover}
              disabled={cover}
              onClick={onSetCover}
            >
              {cover ? (
                <SolidStarIcon className="size-4" />
              ) : (
                <StarIcon className="size-4" />
              )}
            </button>
            <button
              type="button"
              className={iconButton}
              aria-label="Move photo up"
              disabled={first}
              onClick={() => {
                onMove(-1);
              }}
            >
              <ArrowUpIcon className="size-4" />
            </button>
            <button
              type="button"
              className={iconButton}
              aria-label="Move photo down"
              disabled={last}
              onClick={() => {
                onMove(1);
              }}
            >
              <ArrowDownIcon className="size-4" />
            </button>
            <ConfirmButton
              className={`${iconButton} hover:text-red-600`}
              title="Delete photo?"
              message="The photo is removed from the report."
              disabled={remove.isPending}
              onConfirm={() => {
                remove.mutate({ id: photo.id });
              }}
            >
              <TrashIcon className="size-4" aria-label="Delete photo" />
            </ConfirmButton>
          </span>
        </div>
        <ErrorText error={update.error ?? remove.error} />
      </div>
    </li>
  );
};

/**
 * DropEdgeOf returns where a dragged photo dropping at dropIndex shows
 * against the photo at position: before it, or after the last photo.
 */
const dropEdgeOf = (
  dropIndex: number | null,
  position: number,
  count: number
): "before" | "after" | null => {
  if (dropIndex === position) {
    return "before";
  }
  if (dropIndex === count && position === count - 1) {
    return "after";
  }
  return null;
};

// SectionLabel names a section in lists, e.g. "2. Climb to 1,450 m".
export const sectionLabel = (section: DraftSection, index: number): string =>
  `${index + 1}. ${section.title.trim() === "" ? "Untitled section" : section.title.trim()}`;

interface SectionCardProps {
  section: DraftSection;
  index: number;
  count: number;
  // The labels of all sections, to move photos between them.
  labels: { key: string; label: string }[];
  // The photos by ID, and their numbers in the story.
  photos: Map<string, TripPhoto>;
  numbers: Map<string, number>;
  track: RoutePoint[];
  active: boolean;
  splitting: boolean;
  selectedPhotoId: string | null;
  placingPhotoId: string | null;
  coverPhotoId: string;
  onActivate: () => void;
  // What's wrong with the section's stretch, if anything.
  stretchError: string | undefined;
  onChange: (section: DraftSection) => void;
  // Sets the section's stretch, adjusting its neighbors.
  onStretch: (start: number | undefined, end: number | undefined) => void;
  onMove: (delta: number) => void;
  onMergeWithNext: () => void;
  onSplit: () => void;
  onRemove: () => void;
  onSelectPhoto: (id: string) => void;
  // Shows a photo full screen.
  onOpenPhoto: (id: string) => void;
  onPlacePhoto: (id: string) => void;
  // Moves a photo, from this section or another, to a place in this one.
  onDropPhoto: (id: string, index: number) => void;
  onMovePhoto: (id: string, delta: number) => void;
  onMovePhotoToSection: (id: string, key: string) => void;
  onSetCover: (id: string) => void;
  onPhotoChanged: () => Promise<void>;
}

/**
 * SectionCard edits a section of the story: its heading, stretch of the
 * track, text and photos.
 */
export const SectionCard: React.FC<SectionCardProps> = ({
  section,
  index,
  count,
  labels,
  photos,
  numbers,
  track,
  active,
  splitting,
  selectedPhotoId,
  placingPhotoId,
  coverPhotoId,
  onActivate,
  stretchError,
  onChange,
  onStretch,
  onMove,
  onMergeWithNext,
  onSplit,
  onRemove,
  onSelectPhoto,
  onOpenPhoto,
  onPlacePhoto,
  onDropPhoto,
  onMovePhoto,
  onMovePhotoToSection,
  onSetCover,
  onPhotoChanged,
}) => {
  // Where a photo being dragged over the section would drop.
  const [dropIndex, setDropIndex] = useState<number | null>(null);
  const range = draftRange(section);
  let climbing = "";
  if (
    range !== undefined &&
    track.some((point) => point.elevationMeters !== 0)
  ) {
    const { gain, loss } = climbWithin(track, range);
    climbing = `↑ ${formatMeters(gain)} ↓ ${formatMeters(loss)}`;
  }
  const sectionPhotos = section.photoIds.flatMap((id) => {
    const photo = photos.get(id);
    return photo === undefined ? [] : [photo];
  });
  const drop = (event: DragEvent) => {
    const id = event.dataTransfer.getData(PHOTO_DRAG_TYPE);
    if (id !== "" && dropIndex !== null) {
      event.preventDefault();
      onDropPhoto(id, dropIndex);
    }
    setDropIndex(null);
  };
  const leave = (event: DragEvent) => {
    if (
      !(event.relatedTarget instanceof Node) ||
      !event.currentTarget.contains(event.relatedTarget)
    ) {
      setDropIndex(null);
    }
  };
  const otherSections = labels.filter((label) => label.key !== section.key);

  return (
    <section
      aria-label={sectionLabel(section, index)}
      className={`flex flex-col gap-3 rounded-lg border p-3 transition-colors ${
        active
          ? "border-brand-400 bg-brand-50/40 ring-1 ring-brand-300"
          : "border-gray-200"
      }`}
      onFocusCapture={onActivate}
      onPointerDown={onActivate}
    >
      <div className="flex flex-wrap items-center gap-2">
        <span className="inline-flex size-6 items-center justify-center rounded-full bg-gray-800 text-xs font-bold text-white">
          {index + 1}
        </span>
        <div className="flex items-center gap-1 text-xs text-gray-600">
          <KmInput
            label="Start of the stretch in km"
            meters={section.start}
            onChange={(start) => {
              onStretch(start, section.end);
            }}
          />
          <span>to</span>
          <KmInput
            label="End of the stretch in km"
            meters={section.end}
            onChange={(end) => {
              onStretch(section.start, end);
            }}
          />
          <span className="ml-1">km</span>
        </div>
        {climbing !== "" && (
          <span className="text-xs text-gray-500 tabular-nums">{climbing}</span>
        )}
        <span className="ml-auto flex items-center">
          <button
            type="button"
            className={iconButton}
            aria-label="Move section up"
            title="Move up"
            disabled={index === 0}
            onClick={() => {
              onMove(-1);
            }}
          >
            <ArrowUpIcon className="size-4" />
          </button>
          <button
            type="button"
            className={iconButton}
            aria-label="Move section down"
            title="Move down"
            disabled={index === count - 1}
            onClick={() => {
              onMove(1);
            }}
          >
            <ArrowDownIcon className="size-4" />
          </button>
          <button
            type="button"
            className={iconButton}
            aria-label="Merge with the next section"
            title="Merge with the next section"
            disabled={index === count - 1}
            onClick={onMergeWithNext}
          >
            <ArrowsPointingInIcon className="size-4" />
          </button>
          <button
            type="button"
            className={`${iconButton} ${splitting ? "bg-amber-100 text-amber-900" : ""}`}
            aria-label="Split the section"
            aria-pressed={splitting}
            title={
              range === undefined
                ? "Set the stretch of the section to split it"
                : "Split at a point on the elevation profile"
            }
            disabled={range === undefined}
            onClick={onSplit}
          >
            <ScissorsIcon className="size-4" />
          </button>
          <button
            type="button"
            className={`${iconButton} hover:text-red-600`}
            aria-label="Remove the section"
            title={
              index === 0
                ? "Remove, moving its text and photos to the next section"
                : "Remove, moving its text and photos to the section before"
            }
            disabled={count < 2}
            onClick={onRemove}
          >
            <TrashIcon className="size-4" />
          </button>
        </span>
      </div>
      {stretchError !== undefined && (
        <p className="text-xs text-red-700">{stretchError}</p>
      )}
      <input
        value={section.title}
        aria-label="Heading"
        placeholder={
          index === 0 ? "Heading (optional for an introduction)" : "Heading"
        }
        className={`${inputClass} text-base font-semibold`}
        onChange={(event) => {
          onChange({ ...section, title: event.target.value });
        }}
      />
      <textarea
        value={section.body}
        aria-label="Text"
        rows={Math.min(Math.max(section.body.split("\n").length + 1, 4), 16)}
        placeholder="What happened here? Separate paragraphs with a blank line."
        className={`${inputClass} text-base leading-7`}
        onChange={(event) => {
          onChange({ ...section, body: event.target.value });
        }}
      />
      {sectionPhotos.length > 0 ? (
        <ul className="flex flex-col gap-1" onDrop={drop} onDragLeave={leave}>
          {sectionPhotos.map((photo, position) => (
            <PhotoRow
              key={photo.id}
              photo={photo}
              number={numbers.get(photo.id) ?? 0}
              first={position === 0}
              last={position === sectionPhotos.length - 1}
              selected={photo.id === selectedPhotoId}
              placing={photo.id === placingPhotoId}
              cover={photo.id === coverPhotoId}
              sections={otherSections}
              dropEdge={dropEdgeOf(dropIndex, position, sectionPhotos.length)}
              onSelect={() => {
                onSelectPhoto(photo.id);
              }}
              onOpen={() => {
                onOpenPhoto(photo.id);
              }}
              onPlace={() => {
                onPlacePhoto(photo.id);
              }}
              onDragOver={(after) => {
                setDropIndex(position + (after ? 1 : 0));
              }}
              onMove={(delta) => {
                onMovePhoto(photo.id, delta);
              }}
              onMoveToSection={(key) => {
                onMovePhotoToSection(photo.id, key);
              }}
              onSetCover={() => {
                onSetCover(photo.id);
              }}
              onChanged={onPhotoChanged}
            />
          ))}
        </ul>
      ) : (
        <p
          className={`rounded-md text-xs text-gray-500 ${dropIndex === null ? "" : "ring-2 ring-brand-600"}`}
          onDragOver={(event) => {
            if (isPhotoDrag(event)) {
              event.preventDefault();
              setDropIndex(0);
            }
          }}
          onDragLeave={leave}
          onDrop={drop}
        >
          No photos. Upload photos, or move or drag them here from another
          section.
        </p>
      )}
    </section>
  );
};
