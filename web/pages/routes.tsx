import Loading from "@components/loading";
import { TerrainBar, TerrainLegend } from "@components/routes/route-stats";
import { Badge, EmptyState, PageHeader } from "@components/ui/misc";
import QueryError from "@components/ui/query-error";
import { cardClass, primaryButton } from "@components/ui/styles";
import { useQuery } from "@connectrpc/connect-query";
import { PlusIcon } from "@heroicons/react/24/outline";
import { listRoutes } from "gen/routes/v1/routes-RouteService_connectquery";
import { formatKm, formatMeters, formatTimestamp } from "helpers/format";
import { routeSourceLabel } from "helpers/labels";
import React from "react";
import { Link } from "react-router";

const RoutesPage: React.FC = () => {
  const { isPending, isError, data, error, refetch } = useQuery(listRoutes);

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
  } else if (data.routes.length === 0) {
    content = (
      <EmptyState title="No routes yet">
        Design a route on the map, import a GPX file or turn one of your
        activities into a route.
      </EmptyState>
    );
  } else {
    content = (
      <div className="flex flex-col gap-3">
        <TerrainLegend />
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
          {data.routes.map((route) => (
            <Link
              key={route.id}
              to={`/routes/${route.id}`}
              className={`${cardClass} flex flex-col gap-2 p-4 hover:border-brand-400`}
            >
              <div className="flex items-start justify-between gap-2">
                <h2 className="font-semibold text-gray-900">{route.name}</h2>
                <Badge>{routeSourceLabel(route.source)}</Badge>
              </div>
              {route.stats !== undefined && (
                <div className="flex gap-4 text-sm text-gray-700 tabular-nums">
                  <span>
                    <span className="font-semibold">
                      {formatKm(route.stats.distanceMeters)}
                    </span>
                  </span>
                  <span>
                    <span className="font-semibold">
                      {formatMeters(route.stats.elevationGainMeters)}
                    </span>{" "}
                    gain
                  </span>
                  <span className="text-gray-500">
                    max {Math.round(route.stats.maxElevationMeters)} m
                  </span>
                </div>
              )}
              {route.stats !== undefined && (
                <TerrainBar
                  terrain={route.stats.terrain}
                  showLegend={false}
                  className="h-2"
                />
              )}
              {route.description !== "" && (
                <p className="line-clamp-2 text-sm text-gray-500">
                  {route.description}
                </p>
              )}
              <p className="mt-auto text-xs text-gray-400">
                Updated {formatTimestamp(route.updateTime)}
              </p>
            </Link>
          ))}
        </div>
      </div>
    );
  }

  return (
    <div>
      <PageHeader
        title="Routes"
        subtitle="Design routes on the map and check their profile and terrain."
        actions={
          <Link to="/routes/new" className={primaryButton}>
            <PlusIcon className="size-4" />
            New route
          </Link>
        }
      />
      {content}
    </div>
  );
};

export default RoutesPage;
