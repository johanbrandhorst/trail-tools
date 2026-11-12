import Card from "@components/ui/card";
import { primaryButton, secondaryButton } from "@components/ui/styles";
import UnsavedChangesGuard from "@components/ui/unsaved-changes-guard";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { ArrowPathIcon } from "@heroicons/react/24/outline";
import { useQueryClient } from "@tanstack/react-query";
import { ActivityService } from "gen/activities/v1/activities_pb";
import { GeoService } from "gen/geo/v1/geo_pb";
import type { StravaArchiveActivity } from "gen/strava/v1/strava_pb";
import { StravaService } from "gen/strava/v1/strava_pb";
import { useWakeLock } from "helpers/hooks";
import { invalidateServices } from "helpers/query";
import { readZipEntries, readZipEntry } from "helpers/zip";
import type { ZipEntry } from "helpers/zip";
import React, { useMemo, useRef, useState } from "react";
import { Link } from "react-router";

/**
 * CONCURRENT_IMPORTS is how many activities are sent at once: the server
 * handles two uploads at a time, so a third would only wait.
 */
const CONCURRENT_IMPORTS = 2;

// Progress is how an import is going.
interface Progress {
  total: number;
  done: number;
  // The activities whose file couldn't be read, imported without samples.
  withoutSamples: number;
  // The activities that failed, with why.
  failed: { name: string; message: string }[];
  alreadyImported: number;
  unreadable: number;
}

type ImportState =
  | { step: "idle" }
  | { step: "reading" }
  | { step: "importing"; progress: Progress }
  | { step: "done"; progress: Progress; stopped: boolean }
  | { step: "error"; message: string };

const errorMessage = (error: unknown): string => {
  if (error instanceof ConnectError) {
    return error.rawMessage;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return String(error);
};

/**
 * StravaArchiveImport imports the user's activities from the archive of
 * their data that Strava emails them. The archive is read in the browser:
 * only its activities.csv and the activities' files are sent, one at a
 * time, so its photos and other data never leave the device.
 */
const StravaArchiveImport: React.FC = () => {
  const transport = useTransport();
  const client = useMemo(
    () => createClient(StravaService, transport),
    [transport]
  );
  const queryClient = useQueryClient();
  const [state, setState] = useState<ImportState>({ step: "idle" });
  const stopRef = useRef(false);

  const run = async (file: File) => {
    stopRef.current = false;
    setState({ step: "reading" });
    let entries: Map<string, ZipEntry>;
    let activities: StravaArchiveActivity[];
    let progress: Progress;
    try {
      entries = await readZipEntries(file);
      const csvEntry = entries.get("activities.csv");
      if (csvEntry === undefined) {
        throw new Error(
          "The zip has no activities.csv. Choose the zip of your data that Strava emailed you."
        );
      }
      const activitiesCsv = new TextDecoder().decode(
        await readZipEntry(file, csvEntry)
      );
      const started = await client.startStravaArchiveImport({ activitiesCsv });
      ({ activities } = started);
      progress = {
        alreadyImported: started.alreadyImportedCount,
        done: 0,
        failed: [],
        total: activities.length,
        unreadable: started.unreadableCount,
        withoutSamples: 0,
      };
    } catch (error) {
      setState({ message: errorMessage(error), step: "error" });
      return;
    }
    setState({ progress: { ...progress }, step: "importing" });

    let next = 0;
    /*
     * ImportNext imports the next activity, then the one after, until all
     * are done or the user stops: each worker sends one at a time.
     */
    const importNext = async (): Promise<void> => {
      const activity = activities[next];
      if (stopRef.current || activity === undefined) {
        return;
      }
      next += 1;
      try {
        const entry =
          activity.filename === "" ? undefined : entries.get(activity.filename);
        const data =
          entry === undefined
            ? new Uint8Array()
            : await readZipEntry(file, entry);
        const resp = await client.importStravaArchiveActivity({
          file: data,
          stravaId: activity.stravaId,
        });
        if (
          resp.fileUnreadable ||
          (activity.filename !== "" && entry === undefined)
        ) {
          progress.withoutSamples += 1;
        }
      } catch (error) {
        // Imported meanwhile, e.g. by another tab, is as good as done.
        if (
          !(error instanceof ConnectError) ||
          error.code !== Code.FailedPrecondition
        ) {
          progress.failed.push({
            message: errorMessage(error),
            name: activity.name,
          });
        }
      }
      progress.done += 1;
      setState({ progress: { ...progress }, step: "importing" });
      return importNext();
    };
    await Promise.all(Array.from({ length: CONCURRENT_IMPORTS }, importNext));
    setState({
      progress: { ...progress },
      step: "done",
      stopped: stopRef.current,
    });
    await invalidateServices(queryClient, [
      ActivityService,
      GeoService,
      StravaService,
    ]);
  };

  const chooser = (
    <label className={`${primaryButton} cursor-pointer`}>
      Choose the archive
      <input
        type="file"
        accept=".zip,application/zip"
        className="sr-only"
        onChange={(event) => {
          const file = event.target.files?.[0];
          // The same file can be chosen again.
          event.target.value = "";
          if (file !== undefined) {
            void run(file);
          }
        }}
      />
    </label>
  );

  /*
   * Leaving the page stops the import, so it asks first, and keeps the
   * screen on, as a locked phone pauses the page.
   */
  const running = state.step === "reading" || state.step === "importing";
  useWakeLock(running);
  return (
    <Card title="Import from a Strava archive">
      <UnsavedChangesGuard
        dirty={running}
        title="Stop importing?"
        message="Leaving this page stops the import. The activities imported so far are kept, and importing the archive again adds the rest."
        keepLabel="Keep importing"
        leaveLabel="Leave and stop"
      />
      <div className="flex flex-col gap-3 text-sm text-gray-700">
        <p>
          Import your whole history from the archive of your data that Strava
          gives you, without connecting Strava. On strava.com, go to Settings,
          My Account, Download or Delete Your Account, and request your archive:
          Strava emails a link to a zip, which can take a few hours.
        </p>
        <p className="text-xs text-gray-600">
          The zip is read on this device: only the list of activities and the
          activities&apos; files are sent, not your photos or other data.
          Activities imported from the archive are yours to keep, even if you
          disconnect Strava. Importing again only adds what&apos;s new.
        </p>
        {state.step === "idle" && <div>{chooser}</div>}
        {state.step === "reading" && (
          <p className="flex items-center gap-2">
            <ArrowPathIcon className="size-4 animate-spin" />
            Reading the archive…
          </p>
        )}
        {state.step === "error" && (
          <div className="flex flex-col items-start gap-2">
            <p className="rounded-md bg-red-50 p-2 text-red-800">
              {state.message}
            </p>
            {chooser}
          </div>
        )}
        {state.step === "importing" && (
          <div className="flex flex-col gap-2">
            <p className="flex items-center gap-2 tabular-nums">
              <ArrowPathIcon className="size-4 animate-spin" />
              Importing {state.progress.done} of {state.progress.total}{" "}
              activities… Keep this page open.
            </p>
            <div
              className="h-2 overflow-hidden rounded-full bg-gray-200"
              role="progressbar"
              aria-valuemin={0}
              aria-valuemax={state.progress.total}
              aria-valuenow={state.progress.done}
            >
              <div
                className="h-full bg-orange-600"
                style={{
                  width: `${state.progress.total === 0 ? 100 : (100 * state.progress.done) / state.progress.total}%`,
                }}
              />
            </div>
            <div>
              <button
                type="button"
                className={secondaryButton}
                onClick={() => {
                  stopRef.current = true;
                }}
              >
                Stop
              </button>
            </div>
          </div>
        )}
        {state.step === "done" && (
          <div className="flex flex-col items-start gap-2">
            <Summary progress={state.progress} stopped={state.stopped} />
            <div className="flex flex-wrap items-center gap-3">
              <Link to="/activities" className={secondaryButton}>
                See your activities
              </Link>
              {chooser}
            </div>
          </div>
        )}
      </div>
    </Card>
  );
};

// Summary says what an import did.
const Summary: React.FC<{ progress: Progress; stopped: boolean }> = ({
  progress,
  stopped,
}) => {
  const imported = progress.done - progress.failed.length;
  return (
    <div className="flex flex-col gap-1">
      <p className="font-medium text-gray-900">
        {stopped ? "Stopped after importing" : "Imported"} {imported}{" "}
        {imported === 1 ? "activity" : "activities"}.
      </p>
      <ul className="list-disc pl-5 text-xs text-gray-600">
        {progress.alreadyImported > 0 && (
          <li>{progress.alreadyImported} were imported before.</li>
        )}
        {progress.withoutSamples > 0 && (
          <li>
            {progress.withoutSamples} have Strava&apos;s stats but no samples,
            as their files couldn&apos;t be read.
          </li>
        )}
        {progress.unreadable > 0 && (
          <li>{progress.unreadable} rows of the list couldn&apos;t be read.</li>
        )}
        {stopped && <li>Import the archive again to add the rest.</li>}
        {progress.failed.length > 0 && (
          <li>
            {progress.failed.length} failed, e.g. {progress.failed[0]?.name}:{" "}
            {progress.failed[0]?.message}. Import the archive again to retry
            them.
          </li>
        )}
      </ul>
    </div>
  );
};

export default StravaArchiveImport;
