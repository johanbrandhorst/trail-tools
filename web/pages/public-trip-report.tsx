import ElevationProfile from "@components/charts/elevation-profile";
import type { ProfileRange } from "@components/charts/elevation-profile";
import Loading from "@components/loading";
import type { MapFocus } from "@components/map/layers";
import {
  photoGroups,
  sectionRange,
  sectionSummary,
  trackSlice,
} from "@components/trips/sections";
import { TagList } from "@components/trips/tags";
import {
  Story,
  TripStatsRow,
  mapPhotos,
  profilePoints,
  trackPositions,
  tripKicker,
} from "@components/trips/trip-bits";
import TripMap from "@components/trips/trip-map";
import Lightbox from "@components/ui/lightbox";
import PublicFooter from "@components/ui/public-footer";
import QueryError from "@components/ui/query-error";
import { Code } from "@connectrpc/connect";
import { useQuery } from "@connectrpc/connect-query";
import { MapPinIcon, PencilSquareIcon } from "@heroicons/react/24/outline";
import type { RoutePoint } from "gen/routes/v1/routes_pb";
import { getPublicTripReport } from "gen/trips/v1/trips-TripReportService_connectquery";
import type { TripPhoto, TripSection } from "gen/trips/v1/trips_pb";
import type { LatLngTuple } from "leaflet";
import React, { useEffect, useMemo, useRef, useState } from "react";
import { Link, useParams } from "react-router";

const NotAvailable: React.FC = () => (
  <div className="py-16 text-center">
    <h1 className="text-xl font-semibold">Trip report not found</h1>
    <p className="mt-2 text-gray-600">
      This trip report doesn&apos;t exist or isn&apos;t published.
    </p>
  </div>
);

/**
 * How a photo is framed. The cover photo on top (hero) and photos on their
 * own (single) keep their aspect ratio, so portrait photos aren't cropped;
 * photos side by side (grid) are cropped to line up.
 */
type PhotoLayout = "hero" | "single" | "grid";

const imageClasses: Record<PhotoLayout, string> = {
  grid: "aspect-[4/3] w-full rounded-xl bg-gray-200 object-cover shadow-sm",
  hero: "mx-auto h-auto max-h-[80vh] w-auto max-w-full rounded-2xl bg-gray-200 shadow-sm",
  single:
    "mx-auto h-auto max-h-[85vh] w-auto max-w-full rounded-2xl bg-gray-200 shadow-sm",
};

interface PhotoFigureProps {
  photo: TripPhoto;
  // The photo's number, as on the map.
  number: number;
  selected: boolean;
  layout: PhotoLayout;
  onOpen: () => void;
  onHover: (id: string | null) => void;
  onShowOnMap: (photo: TripPhoto) => void;
}

// PhotoFigure shows a photo with its number and caption.
const PhotoFigure: React.FC<PhotoFigureProps> = ({
  photo,
  number,
  selected,
  layout,
  onOpen,
  onHover,
  onShowOnMap,
}) => (
  <figure
    id={`photo-${photo.id}`}
    className={`scroll-mt-4 rounded-2xl transition-shadow ${
      selected ? "ring-4 ring-amber-400 ring-offset-4 ring-offset-slate-50" : ""
    }`}
    onMouseEnter={() => {
      onHover(photo.id);
    }}
    onMouseLeave={() => {
      onHover(null);
    }}
  >
    <button
      type="button"
      className={`block cursor-zoom-in rounded-2xl focus:outline-none focus-visible:ring-4 focus-visible:ring-brand-500 focus-visible:ring-offset-2 ${layout === "grid" ? "w-full" : "mx-auto w-fit"}`}
      onClick={onOpen}
      aria-label={`Open photo ${number}`}
    >
      <img
        src={layout === "grid" ? photo.thumbnailUrl : photo.url}
        alt={photo.caption}
        width={photo.width}
        height={photo.height}
        loading={layout === "hero" ? "eager" : "lazy"}
        className={imageClasses[layout]}
      />
    </button>
    <figcaption className="mt-2 flex items-start gap-2 px-1 text-sm text-gray-600">
      <span className="mt-0.5 inline-flex size-5 shrink-0 items-center justify-center rounded-full bg-ink text-[11px] font-bold text-white">
        {number}
      </span>
      <span className="grow">{photo.caption}</span>
      {photo.location !== undefined && (
        <button
          type="button"
          className="inline-flex shrink-0 items-center gap-1 text-brand-600 hover:text-brand-800"
          onClick={() => {
            onShowOnMap(photo);
          }}
        >
          <MapPinIcon className="size-4" />
          <span className="hidden sm:inline">Map</span>
        </button>
      )}
    </figcaption>
  </figure>
);

// The columns of a group of photos shown side by side.
const gridClasses: Record<number, string> = {
  2: "grid-cols-2",
  3: "grid-cols-2 sm:grid-cols-3",
  4: "grid-cols-2",
};

interface SectionViewProps {
  section: TripSection;
  // The section's photos, with their index in the report.
  photos: { photo: TripPhoto; index: number }[];
  track: RoutePoint[];
  selectedId: string | null;
  onOpen: (index: number) => void;
  onHover: (id: string | null) => void;
  onShowOnMap: (photo: TripPhoto) => void;
}

/**
 * SectionView shows a section of the story: its heading, text, and photos,
 * with photos taken close together side by side.
 */
const SectionView: React.FC<SectionViewProps> = ({
  section,
  photos,
  track,
  selectedId,
  onOpen,
  onHover,
  onShowOnMap,
}) => {
  const range = sectionRange(section);
  const indexes = new Map(photos.map(({ photo, index }) => [photo.id, index]));
  const groups = photoGroups(photos.map(({ photo }) => photo));
  const figure = (photo: TripPhoto, layout: PhotoLayout) => {
    const index = indexes.get(photo.id) ?? 0;
    return (
      <PhotoFigure
        key={photo.id}
        photo={photo}
        number={index + 1}
        selected={photo.id === selectedId}
        layout={layout}
        onOpen={() => {
          onOpen(index);
        }}
        onHover={onHover}
        onShowOnMap={onShowOnMap}
      />
    );
  };
  return (
    <section
      id={`section-${section.id}`}
      data-section={section.id}
      className="flex scroll-mt-4 flex-col gap-6"
    >
      {section.title !== "" && (
        <header className="flex flex-col gap-1">
          <h2 className="text-2xl font-bold tracking-tight text-balance text-gray-900 sm:text-3xl">
            {section.title}
          </h2>
          {range !== undefined && (
            <p className="text-sm font-medium text-brand-600 tabular-nums">
              {sectionSummary(track, range)}
            </p>
          )}
        </header>
      )}
      <Story body={section.body} />
      {groups.map((group) => {
        const [first] = group;
        if (first === undefined) {
          return null;
        }
        if (group.length === 1) {
          return figure(first, "single");
        }
        return (
          <div
            key={first.id}
            className={`grid gap-3 ${gridClasses[group.length] ?? "grid-cols-2"}`}
          >
            {group.map((photo) => figure(photo, "grid"))}
          </div>
        );
      })}
    </section>
  );
};

/**
 * UseActiveSection returns the ID of the section being read: the one
 * crossing a line a little above the middle of the window. Above the first
 * section it's null; between sections the last one stays active.
 */
const useActiveSection = (ids: string[]): string | null => {
  const [active, setActive] = useState<string | null>(null);
  const key = ids.join(",");
  useEffect(() => {
    const elements = key
      .split(",")
      .map((id) => document.querySelector(`[data-section="${CSS.escape(id)}"]`))
      .filter((element) => element !== null);
    if (elements.length === 0) {
      return undefined;
    }
    const crossing = new Set<Element>();
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            crossing.add(entry.target);
          } else {
            crossing.delete(entry.target);
          }
        }
        const current = elements.find((element) => crossing.has(element));
        if (current !== undefined) {
          setActive(current.getAttribute("data-section"));
        } else if (
          (elements[0]?.getBoundingClientRect().top ?? 0) >
          globalThis.innerHeight * 0.4
        ) {
          setActive(null);
        }
      },
      { rootMargin: "-38% 0px -60% 0px" }
    );
    for (const element of elements) {
      observer.observe(element);
    }
    return () => {
      observer.disconnect();
    };
  }, [key]);
  return active;
};

const NO_BREAKS: number[] = [];
const NO_SECTIONS: TripSection[] = [];

/**
 * PublicTripReportPage is the public, blog style page of a published trip
 * report, at /u/<handle>/<slug>. It works without signing in. The story is
 * told section by section, and the map and elevation profile follow along.
 */
const PublicTripReportPage: React.FC = () => {
  const { handle = "", slug = "" } = useParams();
  const { isPending, isError, data, error, refetch } = useQuery(
    getPublicTripReport,
    { handle, slug },
    {
      retry: (failureCount, err) =>
        err.code !== Code.NotFound && failureCount < 3,
    }
  );
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [hoveredId, setHoveredId] = useState<string | null>(null);
  const [lightboxIndex, setLightboxIndex] = useState<number | null>(null);
  const [hoverIndex, setHoverIndex] = useState<number | null>(null);
  const [mapFocus, setMapFocus] = useState<MapFocus | null>(null);
  const mapRef = useRef<HTMLDivElement>(null);

  const report = data?.report;
  const routePoints = useMemo(() => data?.track ?? [], [data?.track]);
  const track = useMemo(() => trackPositions(routePoints), [routePoints]);
  const breaks = data?.trackBreaks ?? NO_BREAKS;
  const profile = useMemo(() => profilePoints(routePoints), [routePoints]);
  const photos = useMemo(() => report?.photos ?? [], [report?.photos]);
  const sections = report?.sections ?? NO_SECTIONS;
  const markers = useMemo(() => mapPhotos(photos), [photos]);
  const sectionIds = useMemo(
    () => sections.map((section) => section.id),
    [sections]
  );
  const activeId = useActiveSection(sectionIds);
  // The stretch of the track of the section being read, if it has one.
  const highlight = useMemo(() => {
    const section = sections.find((candidate) => candidate.id === activeId);
    const range = section === undefined ? undefined : sectionRange(section);
    return range === undefined ? [] : trackSlice(routePoints, range, breaks);
  }, [routePoints, breaks, sections, activeId]);
  const ranges = useMemo(() => {
    const result: ProfileRange[] = [];
    for (const section of sections) {
      const range = sectionRange(section);
      if (range !== undefined) {
        result.push({
          ...range,
          active: section.id === activeId,
          label: section.title,
        });
      }
    }
    return result;
  }, [sections, activeId]);

  useEffect(() => {
    if (report === undefined || data?.author === undefined) {
      return undefined;
    }
    const previous = document.title;
    document.title = `${report.title} · ${data.author.displayName}`;
    return () => {
      document.title = previous;
    };
  }, [report, data?.author]);

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
  const { author } = data;
  if (report === undefined || author === undefined) {
    return <NotAvailable />;
  }

  // Selecting a marker scrolls to its photo.
  const selectFromMap = (id: string) => {
    setSelectedId(id);
    document
      .querySelector(`#photo-${CSS.escape(id)}`)
      ?.scrollIntoView({ behavior: "smooth", block: "center" });
  };
  const showOnMap = (photo: TripPhoto) => {
    setLightboxIndex(null);
    setSelectedId(photo.id);
    if (photo.location !== undefined) {
      setMapFocus({
        key: Date.now(),
        position: [photo.location.lat, photo.location.lng],
      });
    }
    // On narrow screens the map is above the story.
    const rect = mapRef.current?.getBoundingClientRect();
    if (
      rect !== undefined &&
      (rect.bottom < 0 || rect.top > globalThis.innerHeight)
    ) {
      mapRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
    }
  };
  const openPhoto = (index: number) => {
    const photo = photos[index];
    if (photo !== undefined) {
      setSelectedId(photo.id);
    }
    setLightboxIndex(index);
  };
  let hoverPosition: LatLngTuple | null = null;
  if (hoverIndex !== null) {
    hoverPosition = track.at(hoverIndex) ?? null;
  }
  const hasMap = track.length > 1 || markers.length > 0;
  const coverIndex = Math.max(
    photos.findIndex((photo) => photo.id === report.coverPhotoId),
    0
  );
  const cover = photos.at(coverIndex);
  // The photos of each section, without the cover photo shown on top.
  const photoIndexes = new Map(photos.map((photo, index) => [photo.id, index]));
  const sectionPhotos = (section: TripSection) =>
    section.photoIds.flatMap((id) => {
      const index = photoIndexes.get(id);
      if (index === undefined || index === coverIndex) {
        return [];
      }
      const photo = photos[index];
      return photo === undefined ? [] : [{ index, photo }];
    });

  return (
    <article className="mx-auto flex w-full max-w-6xl flex-col gap-8 pb-16">
      {!report.published && (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-900">
          <span>
            This is a preview of a draft. Only you can see it until you publish
            it.
          </span>
          <Link
            to={`/trip-reports/${report.id}`}
            className="inline-flex items-center gap-1 font-medium hover:underline"
          >
            <PencilSquareIcon className="size-4" />
            Edit
          </Link>
        </div>
      )}
      <header className="mx-auto flex max-w-3xl flex-col items-center gap-3 pt-4 text-center sm:pt-8">
        <p className="text-sm font-semibold tracking-wide text-brand-600 uppercase">
          {tripKicker(report)}
        </p>
        <h1 className="text-3xl font-bold tracking-tight text-balance text-gray-900 sm:text-5xl">
          {report.title}
        </h1>
        <p className="text-gray-600">
          by{" "}
          <Link
            to={`/u/${author.handle}`}
            className="font-medium text-gray-900 hover:text-brand-700 hover:underline"
          >
            {author.displayName}
          </Link>
        </p>
        <TagList tags={report.tags} className="justify-center" />
      </header>

      {cover !== undefined && (
        <PhotoFigure
          photo={cover}
          number={coverIndex + 1}
          selected={cover.id === selectedId}
          layout="hero"
          onOpen={() => {
            openPhoto(coverIndex);
          }}
          onHover={setHoveredId}
          onShowOnMap={showOnMap}
        />
      )}

      <TripStatsRow stats={report.stats} />

      <div className="grid grid-cols-1 gap-8 lg:grid-cols-[minmax(0,1fr)_minmax(0,26rem)]">
        {hasMap && (
          <aside
            ref={mapRef}
            className="scroll-mt-4 lg:col-start-2 lg:row-start-1"
          >
            <div className="flex flex-col gap-3 lg:sticky lg:top-4">
              <div className="overflow-hidden rounded-xl shadow-sm ring-1 ring-gray-200">
                <TripMap
                  track={track}
                  photos={markers}
                  selectedId={hoveredId ?? selectedId}
                  hoverPosition={hoverPosition}
                  onSelectPhoto={selectFromMap}
                  focus={mapFocus}
                  breaks={breaks}
                  highlight={highlight}
                  highlightKey={highlight.length > 0 ? (activeId ?? "") : ""}
                  fitKey={report.id}
                  className="h-80 sm:h-96 lg:h-[50vh]"
                />
              </div>
              {profile.length > 1 && (
                <div className="rounded-xl bg-white p-3 shadow-sm ring-1 ring-gray-200">
                  <ElevationProfile
                    points={profile}
                    ranges={ranges}
                    height={160}
                    onHover={setHoverIndex}
                  />
                </div>
              )}
            </div>
          </aside>
        )}
        <div className="flex min-w-0 flex-col gap-14 lg:col-start-1 lg:row-start-1">
          {sections.map((section) => (
            <SectionView
              key={section.id}
              section={section}
              photos={sectionPhotos(section)}
              track={routePoints}
              selectedId={selectedId}
              onOpen={openPhoto}
              onHover={setHoveredId}
              onShowOnMap={showOnMap}
            />
          ))}
        </div>
      </div>
      <PublicFooter author={author} />
      <Lightbox
        photos={photos}
        index={lightboxIndex}
        onChange={setLightboxIndex}
        onShowOnMap={hasMap ? showOnMap : undefined}
      />
    </article>
  );
};

export default PublicTripReportPage;
