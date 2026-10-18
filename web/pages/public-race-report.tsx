import Loading from "@components/loading";
import { RacePhotoGallery } from "@components/races/race-photos";
import {
  Paragraphs,
  RaceComparisonView,
  RaceResult,
  SECTIONS,
} from "@components/races/race-report-bits";
import { SegmentsView } from "@components/races/race-segments";
import { mapPhotos } from "@components/trips/trip-bits";
import Lightbox from "@components/ui/lightbox";
import PublicFooter from "@components/ui/public-footer";
import QueryError from "@components/ui/query-error";
import { Code } from "@connectrpc/connect";
import { useQuery } from "@connectrpc/connect-query";
import { PencilSquareIcon } from "@heroicons/react/24/outline";
import { getPublicRaceReport } from "gen/races/v1/race_reports-RaceReportService_connectquery";
import { formatDate, formatKm } from "helpers/format";
import React, { useEffect, useMemo, useState } from "react";
import { Link, useParams } from "react-router";

const NotAvailable: React.FC = () => (
  <div className="py-16 text-center">
    <h1 className="text-xl font-semibold">Race report not found</h1>
    <p className="mt-2 text-gray-600">
      This race report doesn&apos;t exist or isn&apos;t published.
    </p>
  </div>
);

/**
 * PublicRaceReportPage shows a published race report, at
 * /u/<handle>/races/<slug>: the result against the plan, and the story.
 */
const PublicRaceReportPage: React.FC = () => {
  const { handle = "", slug = "" } = useParams();
  const { isPending, isError, data, error, refetch } = useQuery(
    getPublicRaceReport,
    { handle, slug },
    {
      retry: (failureCount, err) =>
        err.code !== Code.NotFound && failureCount < 3,
    }
  );
  const report = data?.report;
  const photos = report?.photos;
  const markers = useMemo(() => mapPhotos(photos ?? []), [photos]);
  // The index of the photo shown full screen.
  const [openIndex, setOpenIndex] = useState<number | null>(null);
  useEffect(() => {
    if (report !== undefined) {
      document.title = `${report.title} · ${data?.authorName ?? ""}`;
    }
  }, [report, data?.authorName]);

  if (isPending) {
    return <Loading />;
  }
  if (isError) {
    if (error.code === Code.NotFound) {
      return <NotAvailable />;
    }
    return (
      <QueryError
        error={error}
        onRetry={() => {
          void refetch();
        }}
      />
    );
  }
  if (report === undefined) {
    return <NotAvailable />;
  }
  const facts = [
    formatDate(report.raceDate),
    report.distanceMeters > 0 ? formatKm(report.distanceMeters) : "",
    report.elevationGainMeters > 0
      ? `${Math.round(report.elevationGainMeters).toLocaleString()} m gain`
      : "",
  ].filter((part) => part !== "");
  const sections = SECTIONS.filter(
    (section) => section.key !== "summary" && report[section.key].trim() !== ""
  );
  /*
   * The segments and photos come after the race day, or after the sections
   * without it.
   */
  const course = (
    <>
      {report.segments.length > 0 && data.track.length > 1 && (
        <section className="flex flex-col gap-3">
          <h2 className="text-xl font-semibold text-gray-900">
            Along the course
          </h2>
          <SegmentsView
            segments={report.segments}
            photos={markers}
            onSelectPhoto={(id) => {
              const index = report.photos.findIndex((photo) => photo.id === id);
              setOpenIndex(index === -1 ? null : index);
            }}
            track={data.track}
            breaks={data.trackBreaks}
          />
        </section>
      )}
      {report.photos.length > 0 && (
        <section className="flex flex-col gap-3">
          <h2 className="text-xl font-semibold text-gray-900">Photos</h2>
          <RacePhotoGallery photos={report.photos} onOpen={setOpenIndex} />
        </section>
      )}
    </>
  );

  return (
    <article className="mx-auto flex w-full max-w-3xl flex-col gap-8 pb-16">
      {!report.published && (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-900">
          <span>
            This is a preview of a draft. Only you can see it until you publish
            it.
          </span>
          <Link
            to={`/race-reports/${report.id}`}
            className="inline-flex items-center gap-1 font-medium hover:underline"
          >
            <PencilSquareIcon className="size-4" />
            Edit
          </Link>
        </div>
      )}
      <header className="flex flex-col gap-2 text-center">
        <p className="text-sm font-medium tracking-wide text-brand-600 uppercase">
          Race report · {report.raceName}
        </p>
        <h1 className="text-3xl font-bold text-gray-900 sm:text-4xl">
          {report.title}
        </h1>
        <p className="text-gray-600">
          by{" "}
          <Link to={`/u/${handle}`} className="font-medium hover:underline">
            {data.authorName}
          </Link>{" "}
          · {facts.join(" · ")}
        </p>
        <RaceResult
          report={report}
          className="mt-2 justify-center gap-2 text-lg font-semibold text-gray-900"
          iconClassName="size-5"
        />
      </header>
      {report.summary.trim() !== "" && (
        <div className="flex flex-col gap-4 text-lg leading-8 text-gray-800">
          <Paragraphs text={report.summary} />
        </div>
      )}
      {data.comparison?.hasPlan === true && (
        <section className="rounded-xl bg-white p-4 shadow-sm ring-1 ring-gray-200 sm:p-6">
          <h2 className="mb-3 text-lg font-semibold text-gray-900">
            The plan against the race
          </h2>
          <RaceComparisonView
            comparison={data.comparison}
            finishSeconds={report.finishSeconds}
            didNotFinish={report.didNotFinish}
          />
        </section>
      )}
      {sections.map((section) => (
        <React.Fragment key={section.key}>
          <section className="flex flex-col gap-3">
            <h2 className="text-xl font-semibold text-gray-900">
              {section.label}
            </h2>
            <div className="flex flex-col gap-4 text-base leading-7 text-gray-800">
              <Paragraphs text={report[section.key]} />
            </div>
          </section>
          {section.key === "raceDay" && course}
        </React.Fragment>
      ))}
      {!sections.some((section) => section.key === "raceDay") && course}
      <Lightbox
        photos={report.photos}
        index={openIndex}
        onChange={setOpenIndex}
      />
      <PublicFooter author={{ displayName: data.authorName, handle }} />
    </article>
  );
};

export default PublicRaceReportPage;
