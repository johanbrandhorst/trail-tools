import {
  ActivityThumbnail,
  hasDistance,
  useActivityMuscles,
} from "@components/activities/activity-bits";
import Loading from "@components/loading";
import { SelectField, TextField } from "@components/ui/fields";
import type { SelectOption } from "@components/ui/fields";
import { Badge, EmptyState, PageHeader } from "@components/ui/misc";
import QueryError from "@components/ui/query-error";
import {
  cardClass,
  ghostButton,
  linkClass,
  secondaryButton,
} from "@components/ui/styles";
import { useInfiniteQuery } from "@connectrpc/connect-query";
import { CameraIcon } from "@heroicons/react/24/outline";
import { listActivities } from "gen/activities/v1/activities-ActivityService_connectquery";
import { ActivitySort } from "gen/activities/v1/activities_pb";
import {
  formatDate,
  formatHoursMinutes,
  formatKm,
  formatSpeedAsPace,
} from "helpers/format";
import { SPORT_TYPE_OPTIONS, sportLabel } from "helpers/labels";
import React, { useState } from "react";
import { Link, useSearchParams } from "react-router";

const SPORT_SELECT: SelectOption[] = [
  { label: "All sports", value: "" },
  ...SPORT_TYPE_OPTIONS.map((sport) => ({
    label: sportLabel(sport),
    value: sport,
  })),
];

// The orders activities can be listed in, by their URL parameter.
const SORTS: { value: string; label: string; sort: ActivitySort }[] = [
  { label: "Most recent", sort: ActivitySort.UNSPECIFIED, value: "" },
  { label: "Longest distance", sort: ActivitySort.DISTANCE, value: "distance" },
  {
    label: "Most elevation",
    sort: ActivitySort.ELEVATION_GAIN,
    value: "elevation",
  },
  { label: "Longest time", sort: ActivitySort.MOVING_TIME, value: "time" },
];

const SORT_SELECT: SelectOption[] = SORTS.map(({ label, value }) => ({
  label,
  value,
}));

const ActivitiesPage: React.FC = () => {
  const [searchParams, setSearchParams] = useSearchParams();
  const startDate = searchParams.get("start") ?? "";
  const endDate = searchParams.get("end") ?? "";
  const sport = searchParams.get("sport") ?? "";
  const sortParam = searchParams.get("sort") ?? "";
  const sort = SORTS.find((option) => option.value === sortParam) ?? SORTS[0];
  const [draftStart, setDraftStart] = useState(startDate);
  const [draftEnd, setDraftEnd] = useState(endDate);

  const setFilter = (key: string, value: string) => {
    const next = new URLSearchParams(searchParams);
    if (value === "") {
      next.delete(key);
    } else {
      next.set(key, value);
    }
    setSearchParams(next, { replace: true });
  };

  const {
    isPending,
    isError,
    data,
    error,
    refetch,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
  } = useInfiniteQuery(
    listActivities,
    {
      endDate,
      pageSize: 50,
      pageToken: "",
      sort: sort?.sort ?? ActivitySort.UNSPECIFIED,
      sportTypes: sport === "" ? [] : [sport],
      startDate,
    },
    {
      getNextPageParam: (lastPage) =>
        lastPage.nextPageToken === "" ? undefined : lastPage.nextPageToken,
      pageParamKey: "pageToken",
      /**
       * New activities arrive from Strava in the background, so check for
       * them while the page is open. Queries also refetch when the window
       * regains focus.
       */
      refetchInterval: 60 * 1000,
    }
  );
  const muscles = useActivityMuscles(
    data?.pages.flatMap((page) => page.activities) ?? []
  );

  let content: React.ReactNode = null;
  if (isPending) {
    content = <Loading />;
  } else if (isError) {
    content = (
      <QueryError
        error={error}
        onRetry={() => {
          void refetch();
        }}
      />
    );
  } else {
    const activities = data.pages.flatMap((page) => page.activities);
    content =
      activities.length === 0 ? (
        <EmptyState title="No activities found">
          {startDate === "" && endDate === "" && sport === "" ? (
            <>
              Connect Strava in{" "}
              <Link to="/settings" className={linkClass}>
                Settings
              </Link>{" "}
              to sync your activities.
            </>
          ) : (
            "Try a different date range or sport."
          )}
        </EmptyState>
      ) : (
        <div className="flex flex-col gap-3">
          <ul className={`${cardClass} divide-y divide-gray-100`}>
            {activities.map((activity) => (
              <li key={activity.id}>
                <Link
                  to={`/activities/${activity.id}`}
                  className="flex items-center gap-3 px-3 py-2 hover:bg-gray-50"
                >
                  <ActivityThumbnail
                    activity={activity}
                    muscles={muscles.get(activity.id)}
                  />
                  <div className="min-w-0 grow">
                    <p className="truncate font-medium text-gray-900">
                      {activity.name}
                    </p>
                    <p className="flex flex-wrap items-center gap-x-2 text-xs text-gray-500">
                      <span>{formatDate(activity.startDateLocal)}</span>
                      <Badge>{sportLabel(activity.sportType)}</Badge>
                      {activity.photoCount > 0 && (
                        <span
                          className="inline-flex items-center gap-0.5"
                          title={`${activity.photoCount} photos`}
                        >
                          <CameraIcon className="size-3.5" />
                          {activity.photoCount}
                        </span>
                      )}
                    </p>
                  </div>
                  <div className="grid shrink-0 grid-cols-2 gap-x-4 text-right text-sm text-gray-800 tabular-nums sm:grid-cols-4">
                    <span className="font-semibold">
                      {hasDistance(activity)
                        ? formatKm(activity.distanceMeters)
                        : formatHoursMinutes(activity.movingTimeSeconds)}
                    </span>
                    <span>
                      {hasDistance(activity)
                        ? `${Math.round(activity.elevationGainMeters)} m`
                        : ""}
                    </span>
                    <span className="hidden sm:inline">
                      {formatHoursMinutes(activity.movingTimeSeconds)}
                    </span>
                    <span className="hidden text-gray-500 sm:inline">
                      {formatSpeedAsPace(activity.averageSpeedMps)}
                    </span>
                  </div>
                </Link>
              </li>
            ))}
          </ul>
          {hasNextPage && (
            <button
              type="button"
              className={`${secondaryButton} self-center`}
              disabled={isFetchingNextPage}
              onClick={() => {
                void fetchNextPage();
              }}
            >
              {isFetchingNextPage ? "Loading…" : "Load more"}
            </button>
          )}
        </div>
      );
  }

  return (
    <div>
      <PageHeader
        title="Activities"
        subtitle="Synced from Strava and Garmin."
      />
      <form
        className="mb-4 flex flex-wrap items-end gap-3"
        onSubmit={(event) => {
          event.preventDefault();
          const next = new URLSearchParams(searchParams);
          const filters: { key: string; value: string }[] = [
            { key: "start", value: draftStart },
            { key: "end", value: draftEnd },
          ];
          for (const filter of filters) {
            if (filter.value === "") {
              next.delete(filter.key);
            } else {
              next.set(filter.key, filter.value);
            }
          }
          setSearchParams(next, { replace: true });
        }}
      >
        <TextField
          label="From"
          type="date"
          value={draftStart}
          onChange={setDraftStart}
        />
        <TextField
          label="To"
          type="date"
          value={draftEnd}
          onChange={setDraftEnd}
        />
        <button type="submit" className={secondaryButton}>
          Apply
        </button>
        <SelectField
          label="Sport"
          value={sport}
          options={SPORT_SELECT}
          onChange={(value) => {
            setFilter("sport", value);
          }}
        />
        <SelectField
          label="Sort by"
          value={sort?.value ?? ""}
          options={SORT_SELECT}
          onChange={(value) => {
            setFilter("sort", value);
          }}
        />
        {(startDate !== "" || endDate !== "" || sport !== "") && (
          <button
            type="button"
            className={ghostButton}
            onClick={() => {
              setDraftStart("");
              setDraftEnd("");
              // Clearing the filters keeps the order.
              setSearchParams(sortParam === "" ? {} : { sort: sortParam }, {
                replace: true,
              });
            }}
          >
            Clear filters
          </button>
        )}
      </form>
      {content}
    </div>
  );
};

export default ActivitiesPage;
