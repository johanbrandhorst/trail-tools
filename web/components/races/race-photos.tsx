import PhotoUploader from "@components/trips/photo-uploader";
import { locationSourceLabel } from "@components/trips/trip-bits";
import ConfirmButton from "@components/ui/confirm-button";
import Lightbox from "@components/ui/lightbox";
import { ErrorText } from "@components/ui/query-error";
import { ghostButton, iconButton, inputClass } from "@components/ui/styles";
import { useMutation } from "@connectrpc/connect-query";
import { MapPinIcon, TrashIcon, XMarkIcon } from "@heroicons/react/24/outline";
import {
  deleteRaceReportPhoto,
  updateRaceReportPhoto,
  uploadRaceReportPhoto,
} from "gen/races/v1/race_reports-RaceReportService_connectquery";
import type { RaceReport, RaceReportPhoto } from "gen/races/v1/race_reports_pb";
import React, { useState } from "react";

interface NumberBadgeProps {
  number: number;
  className?: string;
}

// NumberBadge shows a photo's number, as on the map.
const NumberBadge: React.FC<NumberBadgeProps> = ({
  number,
  className = "",
}) => (
  <span
    className={`inline-flex size-5 shrink-0 items-center justify-center rounded-full bg-ink text-[11px] font-bold text-white ${className}`}
  >
    {number}
  </span>
);

interface PhotoRowProps {
  photo: RaceReportPhoto;
  number: number;
  // Shows the photo full screen.
  onOpen: () => void;
  // Whether the photo is being placed by clicking the map.
  placing: boolean;
  // Starts or stops placing the photo, if the report has a map.
  onPlace?: (() => void) | undefined;
  onChanged: () => Promise<void>;
}

/**
 * PhotoRow edits a photo of a race report: its caption saves when it loses
 * focus, and the photo can be placed on the map, taken off it or deleted.
 */
const PhotoRow: React.FC<PhotoRowProps> = ({
  photo,
  number,
  onOpen,
  placing,
  onPlace,
  onChanged,
}) => {
  const [caption, setCaption] = useState(photo.caption);
  const update = useMutation(updateRaceReportPhoto, { onSuccess: onChanged });
  const remove = useMutation(deleteRaceReportPhoto, { onSuccess: onChanged });
  const located = photo.location !== undefined;
  let placeLabel = "Place on map";
  if (placing) {
    placeLabel = "Click the map…";
  } else if (located) {
    placeLabel = "Move";
  }

  return (
    <li className="flex gap-3 py-3">
      <button
        type="button"
        className="relative shrink-0 cursor-zoom-in self-start"
        onClick={onOpen}
        aria-label={`Enlarge photo ${String(number)}`}
        title="Enlarge"
      >
        <img
          src={photo.thumbnailUrl}
          alt=""
          className="size-20 rounded-md object-cover sm:size-24"
        />
        <NumberBadge
          number={number}
          className="absolute -top-1.5 -left-1.5 border-2 border-white"
        />
      </button>
      <div className="flex min-w-0 grow flex-col gap-1.5">
        <textarea
          value={caption}
          rows={2}
          placeholder="Caption"
          className={`${inputClass} resize-none`}
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
          <span className="ml-auto flex items-center">
            {onPlace !== undefined && (
              <button
                type="button"
                className={`${ghostButton} text-xs`}
                onClick={onPlace}
              >
                {placeLabel}
              </button>
            )}
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

interface RacePhotosEditorProps {
  report: RaceReport;
  // The photo being placed by clicking the map, if any.
  placingId: string | null;
  // Starts or stops placing a photo, if the report has a map.
  onPlace?: ((id: string) => void) | undefined;
  onChanged: () => Promise<void>;
}

/**
 * RacePhotosEditor uploads, captions and deletes the photos of a race
 * report, which are in the order they were taken.
 */
export const RacePhotosEditor: React.FC<RacePhotosEditorProps> = ({
  report,
  placingId,
  onPlace,
  onChanged,
}) => {
  const [openIndex, setOpenIndex] = useState<number | null>(null);
  const upload = useMutation(uploadRaceReportPhoto);

  return (
    <div className="flex flex-col gap-2">
      <PhotoUploader
        upload={async (fields) =>
          upload.mutateAsync({ ...fields, reportId: report.id })
        }
        onUploaded={onChanged}
      />
      <p className="text-xs text-gray-500">
        Photos are shown in the order they were taken.
        {report.hasTrack &&
          " They're on the map of the segments, where you can drag them, or place them by clicking the map."}
      </p>
      {report.photos.length > 0 && (
        <ul className="divide-y divide-gray-100">
          {report.photos.map((photo, index) => (
            <PhotoRow
              key={photo.id}
              photo={photo}
              number={index + 1}
              onOpen={() => {
                setOpenIndex(index);
              }}
              placing={photo.id === placingId}
              onPlace={
                onPlace === undefined
                  ? undefined
                  : () => {
                      onPlace(photo.id);
                    }
              }
              onChanged={onChanged}
            />
          ))}
        </ul>
      )}
      <Lightbox
        photos={report.photos}
        index={openIndex}
        onChange={setOpenIndex}
      />
    </div>
  );
};

interface RacePhotoGalleryProps {
  photos: RaceReportPhoto[];
  // Shows the photo at the index full screen.
  onOpen: (index: number) => void;
}

/**
 * RacePhotoGallery shows the photos of a race report in the order they were
 * taken, numbered as on the map, with their captions.
 */
export const RacePhotoGallery: React.FC<RacePhotoGalleryProps> = ({
  photos,
  onOpen,
}) => {
  const single = photos.length === 1;
  return (
    <div
      className={`grid gap-x-4 gap-y-6 ${single ? "" : "grid-cols-2 sm:grid-cols-3"}`}
    >
      {photos.map((photo, index) => (
        <figure key={photo.id} id={`photo-${photo.id}`} className="scroll-mt-4">
          <button
            type="button"
            className="block w-full cursor-zoom-in rounded-xl focus:outline-none focus-visible:ring-4 focus-visible:ring-brand-500 focus-visible:ring-offset-2"
            onClick={() => {
              onOpen(index);
            }}
            aria-label={`Open photo ${String(index + 1)}`}
          >
            <img
              src={single ? photo.url : photo.thumbnailUrl}
              alt={photo.caption}
              width={photo.width}
              height={photo.height}
              loading="lazy"
              className={
                single
                  ? "h-auto max-h-[85vh] w-full rounded-2xl bg-gray-200 object-cover shadow-sm"
                  : "aspect-[4/3] w-full rounded-xl bg-gray-200 object-cover shadow-sm"
              }
            />
          </button>
          <figcaption className="mt-2 flex items-start gap-2 px-1 text-sm text-gray-600">
            <NumberBadge number={index + 1} className="mt-0.5" />
            <span className="grow">{photo.caption}</span>
          </figcaption>
        </figure>
      ))}
    </div>
  );
};
