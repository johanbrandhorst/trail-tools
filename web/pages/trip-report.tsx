import ElevationProfile from "@components/charts/elevation-profile";
import type { ProfileRange } from "@components/charts/elevation-profile";
import Loading from "@components/loading";
import PhotoUploader from "@components/trips/photo-uploader";
import { SectionCard, sectionLabel } from "@components/trips/section-card";
import type { DraftSection } from "@components/trips/section-draft";
import {
  applySuggestion,
  draftRange,
  draftSections,
  mergeWithNext,
  moveSection,
  movePhotoTo,
  movePhotoToSection,
  movePhotoWithin,
  newSection,
  rangeError,
  reconcile,
  removeSection,
  sameSections,
  sectionsToSave,
  setStretch,
  splitSection,
} from "@components/trips/section-draft";
import { formatRange, trackSlice } from "@components/trips/sections";
import { TagPicker } from "@components/trips/tags";
import {
  TripStatsRow,
  mapPhotos,
  profilePoints,
  trackPositions,
} from "@components/trips/trip-bits";
import TripMap from "@components/trips/trip-map";
import Card from "@components/ui/card";
import ConfirmButton from "@components/ui/confirm-button";
import { TextField } from "@components/ui/fields";
import Lightbox from "@components/ui/lightbox";
import { Badge } from "@components/ui/misc";
import QueryError, { ErrorText } from "@components/ui/query-error";
import {
  ghostButton,
  labelClass,
  linkClass,
  primaryButton,
  secondaryButton,
  secondaryDangerButton,
} from "@components/ui/styles";
import UnsavedChangesGuard, {
  leaveUnsaved,
} from "@components/ui/unsaved-changes-guard";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import {
  ArrowTopRightOnSquareIcon,
  ArrowUturnLeftIcon,
  EyeSlashIcon,
  GlobeAltIcon,
  PlusIcon,
  SparklesIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";
import { useQueryClient } from "@tanstack/react-query";
import type { RoutePoint } from "gen/routes/v1/routes_pb";
import {
  deleteTripReport,
  getProfile,
  getTripReport,
  publishTripReport,
  setTripSections,
  suggestTripSections,
  unpublishTripReport,
  updateTripPhoto,
  updateTripReport,
  uploadTripPhoto,
} from "gen/trips/v1/trips-TripReportService_connectquery";
import type { TripReport, TripSection } from "gen/trips/v1/trips_pb";
import { LocationSource, TripReportService } from "gen/trips/v1/trips_pb";
import { listPrivacyZones } from "gen/users/v1/users-UserService_connectquery";
import type { PrivacyZone } from "gen/users/v1/users_pb";
import { invalidateServices } from "helpers/query";
import { isValidSlug, slugify } from "helpers/slug";
import type { LatLng, LatLngTuple } from "leaflet";
import React, { useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";

interface SuggestionPanelProps {
  sections: TripSection[];
  onApply: () => void;
  onCancel: () => void;
}

// SuggestionPanel previews suggested sections before they're used.
const SuggestionPanel: React.FC<SuggestionPanelProps> = ({
  sections,
  onApply,
  onCancel,
}) => (
  <div className="flex flex-col gap-3 rounded-lg border border-sky-200 bg-sky-50 p-3 text-sm text-sky-950">
    <p>
      Suggested from the elevation profile, also shown on it. Your text is kept:
      sections without a stretch, like an introduction, stay first, and the text
      of the others moves to the suggested section where they started. Photos go
      in the section where they were taken.
    </p>
    <ol className="flex flex-col gap-1">
      {sections.map((section, index) => (
        <li
          key={`${section.title}-${String(section.startDistanceMeters)}`}
          className="flex flex-wrap gap-x-2"
        >
          <span className="font-semibold">
            {index + 1}. {section.title}
          </span>
          <span className="text-sky-800 tabular-nums">
            {formatRange({
              endMeters: section.endDistanceMeters ?? 0,
              startMeters: section.startDistanceMeters ?? 0,
            })}
            {section.photoIds.length > 0 &&
              ` · ${String(section.photoIds.length)} ${section.photoIds.length === 1 ? "photo" : "photos"}`}
          </span>
        </li>
      ))}
    </ol>
    <div className="flex flex-wrap gap-2">
      <button type="button" className={primaryButton} onClick={onApply}>
        Use these sections
      </button>
      <button type="button" className={secondaryButton} onClick={onCancel}>
        Cancel
      </button>
    </div>
  </div>
);

interface ReportEditorProps {
  report: TripReport;
  track: RoutePoint[];
  // The user's privacy zones, shown on the map.
  privacyZones: PrivacyZone[];
  handle: string | undefined;
  onChanged: () => Promise<void>;
}

/**
 * ReportEditor edits a report: its details and story are a draft saved
 * together, while photos are uploaded, captioned, placed and deleted right
 * away. It is keyed by the report, so its state starts from the saved
 * report.
 */
const ReportEditor: React.FC<ReportEditorProps> = ({
  report,
  track: routePoints,
  privacyZones,
  handle,
  onChanged,
}) => {
  const [title, setTitle] = useState(report.title);
  const [slug, setSlug] = useState(report.slug);
  // The slug follows the title until it is edited by hand.
  const [slugEdited, setSlugEdited] = useState(
    report.slug !== slugify(report.title)
  );
  const [date, setDate] = useState(report.date);
  const [tags, setTags] = useState(report.tags);
  const [draft, setDraft] = useState(() => draftSections(report.sections));
  const [activeKey, setActiveKey] = useState<string | null>(null);
  const [splittingKey, setSplittingKey] = useState<string | null>(null);
  const [suggestion, setSuggestion] = useState<TripSection[] | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [placingId, setPlacingId] = useState<string | null>(null);
  // The index of the photo shown full screen, in story order.
  const [openIndex, setOpenIndex] = useState<number | null>(null);
  const [hoverIndex, setHoverIndex] = useState<number | null>(null);

  const update = useMutation(updateTripReport);
  const saveSections = useMutation(setTripSections);
  const suggest = useMutation(suggestTripSections, {
    onSuccess: (response) => {
      setSuggestion(response.sections);
    },
  });
  const movePhoto = useMutation(updateTripPhoto, { onSuccess: onChanged });
  const uploadPhoto = useMutation(uploadTripPhoto);
  const setCover = useMutation(updateTripReport, { onSuccess: onChanged });

  // Clicking anywhere but a photo, or pressing Escape, clears the selection.
  useEffect(() => {
    if (selectedId === null || placingId !== null) {
      return undefined;
    }
    const onPointerDown = (event: PointerEvent) => {
      if (
        event.target instanceof Element &&
        event.target.closest("[data-photo-row], .photo-marker") === null
      ) {
        setSelectedId(null);
      }
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setSelectedId(null);
      }
    };
    document.addEventListener("pointerdown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [selectedId, placingId]);

  const sections = useMemo(() => reconcile(draft, report), [draft, report]);
  const saved = useMemo(
    () => reconcile(draftSections(report.sections), report),
    [report]
  );
  const photosById = useMemo(
    () => new Map(report.photos.map((photo) => [photo.id, photo])),
    [report.photos]
  );
  // The photos in the order of the draft, numbered as they'll be published.
  const photos = useMemo(
    () =>
      sections.flatMap((section) =>
        section.photoIds.flatMap((id) => {
          const photo = photosById.get(id);
          return photo === undefined ? [] : [photo];
        })
      ),
    [sections, photosById]
  );
  const numbers = new Map(photos.map((photo, index) => [photo.id, index + 1]));
  const track = useMemo(() => trackPositions(routePoints), [routePoints]);
  const profile = useMemo(() => profilePoints(routePoints), [routePoints]);
  const markers = useMemo(() => mapPhotos(photos), [photos]);
  // The stretch of the track of the section being edited, if it has one.
  const highlight = useMemo(() => {
    const active = sections.find((section) => section.key === activeKey);
    const range = active === undefined ? undefined : draftRange(active);
    return range === undefined ? [] : trackSlice(routePoints, range);
  }, [routePoints, sections, activeKey]);
  // The profile shows the suggested sections while they're previewed.
  const ranges: ProfileRange[] = [];
  if (suggestion === null) {
    for (const [index, section] of sections.entries()) {
      const range = draftRange(section);
      if (range !== undefined) {
        ranges.push({
          ...range,
          active: section.key === activeKey,
          label:
            section.title.trim() === "" ? String(index + 1) : section.title,
        });
      }
    }
  } else {
    for (const section of suggestion) {
      if (
        section.startDistanceMeters !== undefined &&
        section.endDistanceMeters !== undefined
      ) {
        ranges.push({
          endMeters: section.endDistanceMeters,
          label: section.title,
          startMeters: section.startDistanceMeters,
        });
      }
    }
  }

  const trimmedTitle = title.trim();
  const detailsDirty =
    trimmedTitle !== report.title ||
    slug !== report.slug ||
    date !== report.date ||
    tags.join() !== report.tags.join();
  const sectionsDirty = !sameSections(sections, saved);
  const dirty = detailsDirty || sectionsDirty;
  const slugValid = isValidSlug(slug);
  const stretchErrors = sections.map((_section, index) =>
    rangeError(sections, index)
  );
  const sectionErrors = stretchErrors.some((error) => error !== undefined);
  const saving = update.isPending || saveSections.isPending;

  const save = async () => {
    if (detailsDirty) {
      await update.mutateAsync({
        date: date === report.date ? undefined : date,
        id: report.id,
        slug: slug === report.slug ? undefined : slug,
        tags: tags.join() === report.tags.join() ? undefined : { tags },
        title: trimmedTitle === report.title ? undefined : trimmedTitle,
      });
    }
    if (sectionsDirty) {
      const response = await saveSections.mutateAsync({
        reportId: report.id,
        sections: sectionsToSave(sections),
      });
      // New sections have IDs now.
      setDraft(draftSections(response.report?.sections ?? []));
      setActiveKey(null);
    }
    await onChanged();
  };
  const discard = () => {
    setTitle(report.title);
    setSlug(report.slug);
    setDate(report.date);
    setTags(report.tags);
    setDraft(draftSections(report.sections));
    setSuggestion(null);
    setSplittingKey(null);
  };
  const change = (next: DraftSection[]) => {
    setDraft(next);
  };
  /*
   * The sections as they were when the stretch being edited started
   * changing. Each change of the edit adjusts the neighbors from them, so a
   * number on its way to the final one, e.g. "1" while typing "15", can't
   * squash a neighbor for good.
   */
  const stretchEdit = useRef<{ key: string; base: DraftSection[] } | null>(
    null
  );
  const place = (photoId: string, position: LatLng) => {
    movePhoto.mutate({
      id: photoId,
      location: { lat: position.lat, lng: position.wrap().lng },
    });
  };
  const placing = photos.find((photo) => photo.id === placingId);
  const splitting = sections.findIndex(
    (section) => section.key === splittingKey
  );
  let hoverPosition: LatLngTuple | null = null;
  if (hoverIndex !== null) {
    hoverPosition = track.at(hoverIndex) ?? null;
  }
  const labels = sections.map((section, index) => ({
    key: section.key,
    label: sectionLabel(section, index),
  }));

  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,28rem)]">
      <form
        className="flex min-w-0 flex-col gap-4"
        onSubmit={(event) => {
          event.preventDefault();
          void save().catch(() => {
            // The mutations show their errors.
          });
        }}
      >
        <Card title="Details">
          <div className="flex flex-col gap-4">
            <TextField
              label="Title"
              value={title}
              required
              onChange={(value) => {
                setTitle(value);
                if (!slugEdited && !report.published) {
                  const next = slugify(value);
                  if (next !== "") {
                    setSlug(next);
                  }
                }
              }}
            />
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-[minmax(0,1fr)_10rem]">
              <label className="flex min-w-0 flex-col gap-1">
                <span className={labelClass}>Address</span>
                <div className="flex items-center rounded-md border border-gray-300 bg-white shadow-sm focus-within:border-brand-600 focus-within:ring-1 focus-within:ring-brand-600">
                  <span className="shrink-0 truncate pl-2.5 text-sm text-gray-500">
                    /u/{handle ?? "<handle>"}/
                  </span>
                  <input
                    value={slug}
                    disabled={report.published}
                    className="block w-full min-w-0 rounded-md border-0 bg-transparent py-1.5 pr-2.5 pl-0.5 text-sm text-gray-900 focus:outline-none disabled:text-gray-500"
                    onChange={(event) => {
                      setSlug(event.target.value.toLowerCase());
                      setSlugEdited(true);
                    }}
                  />
                </div>
                {report.published ? (
                  <span className="text-xs text-gray-500">
                    The address of a published report can&apos;t change, so
                    links to it keep working. Unpublish the report to change it.
                  </span>
                ) : (
                  !slugValid && (
                    <span className="text-xs text-red-700">
                      Use lowercase letters and digits separated by single
                      dashes.
                    </span>
                  )
                )}
              </label>
              <TextField
                label="Date"
                type="date"
                value={date}
                required
                onChange={setDate}
              />
            </div>
            {report.hasTrack && (
              <p className="text-sm text-gray-600">
                {privacyZones.length > 0
                  ? "The track and photos within your privacy zones, shaded on the map, are hidden on the public page."
                  : "The whole track is shown on the public page."}{" "}
                <Link to="/settings" className={linkClass}>
                  {privacyZones.length > 0
                    ? "Edit your privacy zones"
                    : "Hide places like your home with privacy zones"}
                </Link>
                .
              </p>
            )}
            <TagPicker tags={tags} onChange={setTags} />
          </div>
        </Card>

        <Card
          title="Story"
          actions={
            report.hasTrack && (
              <button
                type="button"
                className={secondaryButton}
                disabled={suggest.isPending}
                onClick={() => {
                  suggest.mutate({ reportId: report.id });
                }}
              >
                <SparklesIcon className="size-4" />
                {suggest.isPending ? "Suggesting…" : "Suggest sections"}
              </button>
            )
          }
        >
          <div className="flex flex-col gap-3">
            <p className="text-sm text-gray-600">
              Tell the story in sections, each about a stretch of the trip with
              its photos. Readers see the stretch on the map as they read.
            </p>
            <ErrorText error={suggest.error ?? setCover.error} />
            {suggestion !== null && (
              <SuggestionPanel
                sections={suggestion}
                onApply={() => {
                  change(applySuggestion(sections, suggestion));
                  setSuggestion(null);
                  setActiveKey(null);
                }}
                onCancel={() => {
                  setSuggestion(null);
                }}
              />
            )}
            {sections.map((section, index) => (
              <SectionCard
                key={section.key}
                section={section}
                index={index}
                count={sections.length}
                labels={labels}
                photos={photosById}
                numbers={numbers}
                track={routePoints}
                active={section.key === activeKey}
                splitting={section.key === splittingKey}
                selectedPhotoId={selectedId}
                placingPhotoId={placingId}
                coverPhotoId={report.coverPhotoId}
                onActivate={() => {
                  setActiveKey(section.key);
                }}
                stretchError={stretchErrors[index]}
                onChange={(next) => {
                  change(
                    sections.map((other) =>
                      other.key === next.key ? next : other
                    )
                  );
                }}
                onStretch={(start, end) => {
                  if (stretchEdit.current?.key !== section.key) {
                    stretchEdit.current = { base: sections, key: section.key };
                  }
                  change(
                    setStretch(
                      stretchEdit.current.base,
                      section.key,
                      start,
                      end
                    )
                  );
                }}
                onStretchDone={() => {
                  stretchEdit.current = null;
                }}
                onMove={(delta) => {
                  change(moveSection(sections, index, delta));
                }}
                onMergeWithNext={() => {
                  change(mergeWithNext(sections, index));
                }}
                onSplit={() => {
                  setSplittingKey(
                    section.key === splittingKey ? null : section.key
                  );
                }}
                onRemove={() => {
                  change(removeSection(sections, index));
                  setActiveKey(null);
                }}
                onSelectPhoto={setSelectedId}
                onOpenPhoto={(photoId) => {
                  setOpenIndex(
                    photos.findIndex((photo) => photo.id === photoId)
                  );
                }}
                onDropPhoto={(photoId, at) => {
                  change(movePhotoTo(sections, photoId, section.key, at));
                }}
                onPlacePhoto={(photoId) => {
                  setSelectedId(photoId);
                  setPlacingId(photoId === placingId ? null : photoId);
                }}
                onMovePhoto={(photoId, delta) => {
                  change(movePhotoWithin(sections, photoId, delta));
                }}
                onMovePhotoToSection={(photoId, key) => {
                  change(movePhotoToSection(sections, photoId, key));
                }}
                onSetCover={(photoId) => {
                  setCover.mutate({ coverPhotoId: photoId, id: report.id });
                }}
                onPhotoChanged={onChanged}
              />
            ))}
            <button
              type="button"
              className={`${ghostButton} self-start`}
              onClick={() => {
                const added = newSection();
                change([...sections, added]);
                setActiveKey(added.key);
              }}
            >
              <PlusIcon className="size-4" />
              Add a section
            </button>
          </div>
        </Card>

        <Card title={`Photos (${String(photos.length)})`}>
          <div className="flex flex-col gap-2">
            <PhotoUploader
              upload={async (fields) =>
                uploadPhoto.mutateAsync({ ...fields, reportId: report.id })
              }
              onUploaded={onChanged}
            />
            <p className="text-xs text-gray-500">
              New photos go in the section about where they were taken, or the
              last section.
            </p>
          </div>
        </Card>

        <div className="sticky bottom-0 z-10 flex flex-wrap items-center gap-3 border-t border-gray-200 bg-gray-100/95 py-3 backdrop-blur">
          <button
            type="submit"
            className={primaryButton}
            disabled={
              !dirty ||
              saving ||
              trimmedTitle === "" ||
              !slugValid ||
              date === "" ||
              sectionErrors
            }
          >
            {saving ? "Saving…" : "Save"}
          </button>
          {dirty && (
            <button
              type="button"
              className={ghostButton}
              disabled={saving}
              onClick={discard}
            >
              <ArrowUturnLeftIcon className="size-4" />
              Discard changes
            </button>
          )}
          <span className="text-sm text-gray-500">
            {dirty ? "Unsaved changes" : "All changes saved"}
          </span>
          <ErrorText error={update.error ?? saveSections.error} />
        </div>
      </form>
      <UnsavedChangesGuard dirty={dirty} />
      <Lightbox
        photos={photos}
        index={openIndex}
        onChange={setOpenIndex}
        onShowOnMap={(photo) => {
          setSelectedId(photo.id);
          setOpenIndex(null);
        }}
      />

      <div className="flex flex-col gap-3 lg:sticky lg:top-4 lg:self-start">
        <div className="overflow-hidden rounded-lg border border-gray-200">
          <TripMap
            track={track}
            photos={markers}
            selectedId={selectedId}
            hoverPosition={hoverPosition}
            onSelectPhoto={setSelectedId}
            onMovePhoto={place}
            privacyZones={privacyZones}
            highlight={highlight}
            onMapClick={
              placing === undefined
                ? undefined
                : (position) => {
                    place(placing.id, position);
                    setPlacingId(null);
                  }
            }
            fitKey={report.id}
            className="h-[45vh] min-h-72"
          />
        </div>
        {placing === undefined ? (
          <p className="text-xs text-gray-500">
            Drag a photo on the map to move it. Photos that aren&apos;t on the
            map yet can be placed from their section.
          </p>
        ) : (
          <p className="flex items-center justify-between gap-2 rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-900">
            Click the map where photo {numbers.get(placing.id)} was taken.
            <button
              type="button"
              className={ghostButton}
              onClick={() => {
                setPlacingId(null);
              }}
            >
              Cancel
            </button>
          </p>
        )}
        <ErrorText error={movePhoto.error} />
        {splitting >= 0 && (
          <p className="flex items-center justify-between gap-2 rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-900">
            Click the elevation profile where section {splitting + 1} should
            split.
            <button
              type="button"
              className={ghostButton}
              onClick={() => {
                setSplittingKey(null);
              }}
            >
              Cancel
            </button>
          </p>
        )}
        {profile.length > 1 && (
          <Card>
            <ElevationProfile
              points={profile}
              ranges={ranges}
              height={170}
              onHover={setHoverIndex}
              onSelectDistance={
                splitting < 0
                  ? undefined
                  : (distance) => {
                      change(
                        splitSection(sections, splitting, distance, photosById)
                      );
                      setSplittingKey(null);
                    }
              }
            />
          </Card>
        )}
        {photos.some(
          (photo) => photo.locationSource === LocationSource.UNSPECIFIED
        ) && (
          <p className="text-xs text-amber-800">
            Some photos aren&apos;t on the map yet.
          </p>
        )}
      </div>
    </div>
  );
};

const NO_TRACK: RoutePoint[] = [];
const NO_ZONES: PrivacyZone[] = [];

// TripReportPage edits a trip report: its story, photos and publication.
const TripReportPage: React.FC = () => {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { isPending, isError, data, error, refetch } = useQuery(getTripReport, {
    id,
    includeTrack: true,
  });
  const profileQuery = useQuery(getProfile);
  const zonesQuery = useQuery(listPrivacyZones);

  const invalidate = async () => {
    await invalidateServices(queryClient, [TripReportService]);
  };
  const publish = useMutation(publishTripReport, { onSuccess: invalidate });
  const unpublish = useMutation(unpublishTripReport, { onSuccess: invalidate });
  const remove = useMutation(deleteTripReport, {
    onSuccess: async () => {
      // The report's unsaved changes went with it.
      await navigate("/trip-reports", leaveUnsaved);
      await invalidate();
    },
  });

  if (isPending) {
    return <Loading />;
  }
  if (isError) {
    return (
      <QueryError
        error={error}
        onRetry={() => {
          void refetch();
        }}
      />
    );
  }
  const { report } = data;
  if (report === undefined) {
    return <p>Trip report not found.</p>;
  }
  const handle = profileQuery.data?.profile?.handle;
  const publicPath = handle === undefined ? "" : `/u/${handle}/${report.slug}`;

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <Link
            to="/trip-reports"
            className="text-sm text-gray-500 hover:underline"
          >
            Trip reports
          </Link>
          <h1 className="flex flex-wrap items-center gap-2 text-2xl font-bold text-gray-900">
            {report.title}
            {report.published ? (
              <Badge className="bg-emerald-50 text-emerald-800 ring-emerald-300">
                Published
              </Badge>
            ) : (
              <Badge>Draft</Badge>
            )}
          </h1>
          {report.activityId !== "" && (
            <Link
              to={`/activities/${report.activityId}`}
              className="text-sm text-gray-500 hover:underline"
            >
              View the activity
            </Link>
          )}
        </div>
        <div className="flex flex-wrap gap-2">
          {publicPath !== "" && (
            <Link to={publicPath} className={secondaryButton}>
              <ArrowTopRightOnSquareIcon className="size-4" />
              {report.published ? "View" : "Preview"}
            </Link>
          )}
          {report.published ? (
            <button
              type="button"
              className={secondaryButton}
              disabled={unpublish.isPending}
              onClick={() => {
                unpublish.mutate({ id: report.id });
              }}
            >
              <EyeSlashIcon className="size-4" />
              Unpublish
            </button>
          ) : (
            <button
              type="button"
              className={primaryButton}
              disabled={handle === undefined || publish.isPending}
              onClick={() => {
                publish.mutate({ id: report.id });
              }}
            >
              <GlobeAltIcon className="size-4" />
              Publish
            </button>
          )}
          <ConfirmButton
            className={secondaryDangerButton}
            title="Delete trip report?"
            message="The report and its photos are deleted. This can't be undone."
            disabled={remove.isPending}
            onConfirm={() => {
              remove.mutate({ id: report.id });
            }}
          >
            <TrashIcon className="size-4" />
            Delete
          </ConfirmButton>
        </div>
      </div>
      {profileQuery.data !== undefined && handle === undefined && (
        <p className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-900">
          To publish, first choose a public handle for your reports' address in{" "}
          <Link to="/settings" className={linkClass}>
            Settings
          </Link>
          .
        </p>
      )}
      {report.published && report.url !== "" && (
        <p className="rounded-md border border-emerald-200 bg-emerald-50 px-3 py-2 text-sm text-emerald-900">
          Published at{" "}
          <a href={report.url} className="font-medium break-all underline">
            {report.url}
          </a>
          . Anyone with the link can read it, and search engines can find it.
        </p>
      )}
      <ErrorText error={publish.error ?? unpublish.error ?? remove.error} />
      <TripStatsRow stats={report.stats} />
      <ReportEditor
        key={report.id}
        report={report}
        track={data.track.length > 0 ? data.track : NO_TRACK}
        privacyZones={zonesQuery.data?.zones ?? NO_ZONES}
        handle={handle}
        onChanged={invalidate}
      />
    </div>
  );
};

export default TripReportPage;
