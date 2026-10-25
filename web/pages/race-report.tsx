import Loading from "@components/loading";
import { RacePhotosEditor } from "@components/races/race-photos";
import {
  DnfBadge,
  RaceComparisonView,
  SECTIONS,
} from "@components/races/race-report-bits";
import type { SectionKey } from "@components/races/race-report-bits";
import { SegmentsEditor } from "@components/races/race-segments";
import { mapPhotos } from "@components/trips/trip-bits";
import Card from "@components/ui/card";
import ConfirmButton from "@components/ui/confirm-button";
import { CheckboxField, TextField } from "@components/ui/fields";
import { Badge } from "@components/ui/misc";
import QueryError, { ErrorText } from "@components/ui/query-error";
import {
  inputClass,
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
  EyeSlashIcon,
  GlobeAltIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";
import { useQueryClient } from "@tanstack/react-query";
import {
  deleteRaceReport,
  getRaceReport,
  publishRaceReport,
  unpublishRaceReport,
  updateRaceReport,
  updateRaceReportPhoto,
} from "gen/races/v1/race_reports-RaceReportService_connectquery";
import type { RaceReport } from "gen/races/v1/race_reports_pb";
import { RaceReportService } from "gen/races/v1/race_reports_pb";
import { getProfile } from "gen/trips/v1/trips-TripReportService_connectquery";
import { listPrivacyZones } from "gen/users/v1/users-UserService_connectquery";
import type { PrivacyZone } from "gen/users/v1/users_pb";
import { formatDate, formatKm } from "helpers/format";
import { durationInput, kmInput, parseDuration, parseKm } from "helpers/parse";
import { invalidateServices } from "helpers/query";
import { isValidSlug, slugify } from "helpers/slug";
import React, { useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";

interface ReportFormProps {
  report: RaceReport;
  handle: string | undefined;
  onSaved: () => Promise<void>;
}

// ReportForm edits a race report's title, address, result and story.
const ReportForm: React.FC<ReportFormProps> = ({ report, handle, onSaved }) => {
  const [title, setTitle] = useState(report.title);
  const [slug, setSlug] = useState(report.slug);
  const [slugEdited, setSlugEdited] = useState(
    report.slug !== slugify(report.title)
  );
  const [finish, setFinish] = useState(
    durationInput(report.finishSeconds, true)
  );
  const [placement, setPlacement] = useState(report.placement);
  const [didNotFinish, setDidNotFinish] = useState(report.didNotFinish);
  const [stopTime, setStopTime] = useState(
    durationInput(report.stopSeconds, true)
  );
  const [stopKm, setStopKm] = useState(kmInput(report.stopDistanceMeters));
  const [stopCheckpoint, setStopCheckpoint] = useState(report.stopCheckpoint);
  /*
   * The server defaults where and when the runner stopped when the report
   * becomes a DNF, so take them over once saved.
   */
  const [savedStop, setSavedStop] = useState({
    meters: report.stopDistanceMeters,
    seconds: report.stopSeconds,
  });
  if (
    savedStop.meters !== report.stopDistanceMeters ||
    savedStop.seconds !== report.stopSeconds
  ) {
    setSavedStop({
      meters: report.stopDistanceMeters,
      seconds: report.stopSeconds,
    });
    setStopKm(kmInput(report.stopDistanceMeters));
    setStopTime(durationInput(report.stopSeconds, true));
  }
  const [sections, setSections] = useState<Record<SectionKey, string>>({
    lessons: report.lessons,
    nutrition: report.nutrition,
    preparation: report.preparation,
    raceDay: report.raceDay,
    summary: report.summary,
    wentBadly: report.wentBadly,
    wentWell: report.wentWell,
  });
  const update = useMutation(updateRaceReport, { onSuccess: onSaved });

  const trimmedTitle = title.trim();
  const finishSeconds = parseDuration(finish);
  const stopSeconds = parseDuration(stopTime);
  const stopMeters = parseKm(stopKm);
  const stopBeyondRace =
    stopMeters !== null &&
    report.distanceMeters > 0 &&
    stopMeters > report.distanceMeters;
  // The result is valid: a finish time, or where and when the runner stopped.
  const resultValid = didNotFinish
    ? stopSeconds !== null && stopMeters !== null && !stopBeyondRace
    : finishSeconds !== null;
  const slugValid = isValidSlug(slug);
  const changedSections = SECTIONS.filter(
    (section) => sections[section.key] !== report[section.key]
  );
  /*
   * The changes to the result: the finish time, or whether the runner
   * finished and if not where and when they stopped. Becoming a DNF without
   * a stop distance lets the server default it to the activity's.
   */
  const resultChanges = didNotFinish
    ? {
        didNotFinish: report.didNotFinish ? undefined : true,
        stopCheckpoint:
          stopCheckpoint.trim() === report.stopCheckpoint
            ? undefined
            : stopCheckpoint.trim(),
        stopDistanceMeters:
          stopKm.trim() === kmInput(report.stopDistanceMeters)
            ? undefined
            : (stopMeters ?? undefined),
        stopSeconds:
          stopSeconds === report.stopSeconds
            ? undefined
            : (stopSeconds ?? undefined),
      }
    : {
        didNotFinish: report.didNotFinish ? false : undefined,
        finishSeconds:
          finishSeconds === report.finishSeconds
            ? undefined
            : (finishSeconds ?? undefined),
      };
  const dirty =
    trimmedTitle !== report.title ||
    slug !== report.slug ||
    Object.values(resultChanges).some((value) => value !== undefined) ||
    placement.trim() !== report.placement ||
    changedSections.length > 0;
  const save = () => {
    if (!resultValid) {
      return;
    }
    const changes: Partial<Record<SectionKey, string>> = {};
    for (const section of changedSections) {
      changes[section.key] = sections[section.key];
    }
    update.mutate({
      ...changes,
      ...resultChanges,
      id: report.id,
      placement:
        placement.trim() === report.placement ? undefined : placement.trim(),
      slug: slug === report.slug ? undefined : slug,
      title: trimmedTitle === report.title ? undefined : trimmedTitle,
    });
  };

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(event) => {
        event.preventDefault();
        save();
      }}
    >
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
      <label className="flex min-w-0 flex-col gap-1">
        <span className={labelClass}>Address</span>
        <div className="flex items-center rounded-md border border-gray-300 bg-white shadow-sm focus-within:border-brand-600 focus-within:ring-1 focus-within:ring-brand-600">
          <span className="shrink-0 truncate pl-2.5 text-sm text-gray-500">
            /u/{handle ?? "<handle>"}/races/
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
            The address of a published report can&apos;t change, so links to it
            keep working. Unpublish the report to change it.
          </span>
        ) : (
          !slugValid && (
            <span className="text-xs text-red-700">
              Use lowercase letters and digits separated by single dashes.
            </span>
          )
        )}
      </label>
      <CheckboxField
        label="I didn't finish (DNF)"
        checked={didNotFinish}
        onChange={(checked) => {
          setDidNotFinish(checked);
          // The time so far, e.g. the activity's, is when the runner stopped.
          if (checked && stopTime === "") {
            setStopTime(finish);
          }
        }}
      />
      {didNotFinish && (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-[10rem_8rem_minmax(0,1fr)]">
          <TextField
            label="Stopped after"
            value={stopTime}
            onChange={setStopTime}
            placeholder="h:mm:ss"
          />
          <TextField
            label="Stopped at (km)"
            value={stopKm}
            onChange={setStopKm}
            inputMode="decimal"
            placeholder={
              report.activityId !== "" && !report.didNotFinish
                ? "From activity"
                : ""
            }
          />
          <TextField
            label="Stopped at checkpoint"
            value={stopCheckpoint}
            onChange={setStopCheckpoint}
            placeholder="e.g. Function Junction"
          />
        </div>
      )}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-[10rem_minmax(0,1fr)]">
        {!didNotFinish && (
          <TextField
            label="Finish time (h:mm:ss)"
            value={finish}
            onChange={setFinish}
            placeholder="5:42:10"
          />
        )}
        <TextField
          label="Placement"
          value={placement}
          onChange={setPlacement}
          placeholder={
            didNotFinish ? "e.g. timed out" : "e.g. 12th of 240, 3rd M40"
          }
          className={didNotFinish ? "sm:col-span-2" : ""}
        />
      </div>
      {!didNotFinish && finishSeconds === null && (
        <p className="text-xs text-red-700">
          Enter the finish time as h:mm:ss.
        </p>
      )}
      {didNotFinish && stopSeconds === null && (
        <p className="text-xs text-red-700">
          Enter the time you stopped as h:mm:ss.
        </p>
      )}
      {didNotFinish && stopMeters === null && (
        <p className="text-xs text-red-700">
          Enter where you stopped in kilometers.
        </p>
      )}
      {stopBeyondRace && (
        <p className="text-xs text-red-700">
          Where you stopped must be within the race&apos;s{" "}
          {formatKm(report.distanceMeters)}.
        </p>
      )}
      {SECTIONS.map((section) => (
        <label key={section.key} className="flex flex-col gap-1">
          <span className={labelClass}>{section.label}</span>
          <textarea
            value={sections[section.key]}
            rows={section.key === "summary" ? 3 : 6}
            placeholder={section.placeholder}
            className={`${inputClass} text-base leading-7`}
            onChange={(event) => {
              setSections((current) => ({
                ...current,
                [section.key]: event.target.value,
              }));
            }}
          />
        </label>
      ))}
      <div className="flex flex-wrap items-center gap-3">
        <button
          type="submit"
          className={primaryButton}
          disabled={
            !dirty ||
            update.isPending ||
            trimmedTitle === "" ||
            !slugValid ||
            !resultValid
          }
        >
          {update.isPending ? "Saving…" : "Save"}
        </button>
        <span className="text-sm text-gray-500">
          {dirty ? "Unsaved changes" : "All changes saved"}
        </span>
      </div>
      <ErrorText error={update.error} />
      <UnsavedChangesGuard dirty={dirty} />
    </form>
  );
};

const NO_ZONES: PrivacyZone[] = [];

// RaceReportPage edits one of the user's race reports.
const RaceReportPage: React.FC = () => {
  const { id = "" } = useParams();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const { isPending, isError, data, error, refetch } = useQuery(getRaceReport, {
    id,
    // The track, for the segments' map and elevation profile.
    includeTrack: true,
  });
  const profileQuery = useQuery(getProfile);
  const zonesQuery = useQuery(listPrivacyZones);
  const handle = profileQuery.data?.profile?.handle;
  const invalidate = async () => {
    await invalidateServices(queryClient, [RaceReportService]);
  };
  const publish = useMutation(publishRaceReport, { onSuccess: invalidate });
  const unpublish = useMutation(unpublishRaceReport, {
    onSuccess: invalidate,
  });
  const remove = useMutation(deleteRaceReport, {
    onSuccess: async () => {
      // The report's unsaved changes went with it.
      await navigate("/race-reports", leaveUnsaved);
      await invalidate();
    },
  });
  const movePhoto = useMutation(updateRaceReportPhoto, {
    onSuccess: invalidate,
  });
  const photos = data?.report?.photos;
  const markers = useMemo(() => mapPhotos(photos ?? []), [photos]);
  // The photo being placed by clicking the map, if any.
  const [placingId, setPlacingId] = useState<string | null>(null);

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
  const { report, comparison } = data;
  if (report === undefined) {
    return <p>Race report not found.</p>;
  }
  const publicPath =
    handle === undefined ? "" : `/u/${handle}/races/${report.slug}`;
  const placingIndex = report.photos.findIndex(
    (photo) => photo.id === placingId
  );

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <Link
            to="/race-reports"
            className="text-sm text-gray-500 hover:underline"
          >
            Race reports
          </Link>
          <h1 className="flex flex-wrap items-center gap-2 text-2xl font-bold text-gray-900">
            {report.title}
            {report.didNotFinish && <DnfBadge />}
            {report.published ? (
              <Badge className="bg-emerald-50 text-emerald-800 ring-emerald-300">
                Published
              </Badge>
            ) : (
              <Badge>Draft</Badge>
            )}
          </h1>
          <p className="text-sm text-gray-500">
            {[
              report.raceName,
              formatDate(report.raceDate),
              report.distanceMeters > 0 ? formatKm(report.distanceMeters) : "",
              report.elevationGainMeters > 0
                ? `${Math.round(report.elevationGainMeters).toLocaleString()} m gain`
                : "",
            ]
              .filter((part) => part !== "")
              .join(" · ")}
            {report.activityId !== "" && (
              <>
                {" · "}
                <Link
                  to={`/activities/${report.activityId}`}
                  className="hover:underline"
                >
                  View the activity
                </Link>
              </>
            )}
          </p>
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
            title="Delete race report?"
            message="The report is deleted. This can't be undone."
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
          To publish, first choose a public handle for your reports&apos;
          address in{" "}
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
        </p>
      )}
      <ErrorText error={publish.error ?? unpublish.error ?? remove.error} />
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,28rem)]">
        <Card title="Story">
          <ReportForm
            key={report.id}
            report={report}
            handle={handle}
            onSaved={invalidate}
          />
        </Card>
        <div className="order-first flex flex-col gap-4 lg:sticky lg:top-4 lg:order-none lg:self-start">
          <Card title="Plan vs actual">
            <RaceComparisonView
              comparison={comparison}
              finishSeconds={report.finishSeconds}
              didNotFinish={report.didNotFinish}
            />
          </Card>
        </div>
      </div>
      <Card title="Segments">
        {report.hasTrack ? (
          <SegmentsEditor
            // A new activity, and with it a new track, resets the editor.
            key={`${report.id}-${report.activityId}`}
            report={report}
            track={data.track}
            privacyZones={zonesQuery.data?.zones ?? NO_ZONES}
            photos={markers}
            onMovePhoto={(photoId, position) => {
              movePhoto.mutate({
                id: photoId,
                location: { lat: position.lat, lng: position.lng },
              });
            }}
            placing={
              placingId === null || placingIndex === -1
                ? undefined
                : {
                    number: placingIndex + 1,
                    onCancel: () => {
                      setPlacingId(null);
                    },
                    onPlace: (position) => {
                      movePhoto.mutate({
                        id: placingId,
                        location: { lat: position.lat, lng: position.lng },
                      });
                      setPlacingId(null);
                    },
                  }
            }
            onSaved={invalidate}
          />
        ) : (
          <p className="text-sm text-gray-600">
            Set the race's activity in the story to comment on stretches of its
            track.
          </p>
        )}
        <ErrorText error={movePhoto.error} />
      </Card>
      <Card title={`Photos (${String(report.photos.length)})`}>
        <RacePhotosEditor
          report={report}
          placingId={placingId}
          onPlace={
            report.hasTrack
              ? (photoId) => {
                  setPlacingId(photoId === placingId ? null : photoId);
                }
              : undefined
          }
          onChanged={invalidate}
        />
      </Card>
    </div>
  );
};

export default RaceReportPage;
