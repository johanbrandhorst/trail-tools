import { Stat } from "@components/ui/misc";
import type { RouteStats, TerrainShare } from "gen/routes/v1/routes_pb";
import { formatKm, formatMeters, formatPercent } from "helpers/format";
import { TERRAIN_STYLES, terrainStyle } from "helpers/labels";
import React from "react";

interface TerrainBarProps {
  terrain: TerrainShare[];
  showLegend?: boolean;
  className?: string;
}

// TerrainBar shows the terrain breakdown as a stacked horizontal bar.
export const TerrainBar: React.FC<TerrainBarProps> = ({
  terrain,
  showLegend = true,
  className = "h-3",
}) => {
  const shares = terrain.filter((share) => share.fraction > 0);
  if (shares.length === 0) {
    return showLegend ? (
      <p className="text-xs text-gray-500">No terrain data</p>
    ) : null;
  }
  return (
    <div className="flex flex-col gap-1.5">
      <div className={`flex w-full overflow-hidden rounded-full ${className}`}>
        {shares.map((share) => {
          const style = terrainStyle(share.terrain);
          return (
            <div
              key={share.terrain}
              title={`${style.label}: ${formatPercent(share.fraction)}`}
              style={{
                backgroundColor: style.color,
                width: `${share.fraction * 100}%`,
              }}
            />
          );
        })}
      </div>
      {showLegend && (
        <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-gray-600">
          {shares.map((share) => {
            const style = terrainStyle(share.terrain);
            return (
              <span
                key={share.terrain}
                className="inline-flex items-center gap-1"
              >
                <span
                  className="inline-block size-2.5 rounded-sm"
                  style={{ backgroundColor: style.color }}
                />
                {style.label} {formatPercent(share.fraction)}
                <span className="text-gray-400">
                  ({formatKm(share.distanceMeters)})
                </span>
              </span>
            );
          })}
        </div>
      )}
    </div>
  );
};

/**
 * TerrainLegend names the colors of terrain bars shown without their own
 * legend, like those of a list of routes.
 */
export const TerrainLegend: React.FC = () => (
  <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-gray-600">
    <span className="font-medium">Terrain:</span>
    {TERRAIN_STYLES.map((style) => (
      <span key={style.label} className="inline-flex items-center gap-1">
        <span
          className="inline-block size-2.5 rounded-sm"
          style={{ backgroundColor: style.color }}
        />
        {style.label}
      </span>
    ))}
  </div>
);

interface RouteStatsPanelProps {
  stats: RouteStats | undefined;
}

// RouteStatsPanel shows a route's distance, elevation and terrain.
export const RouteStatsPanel: React.FC<RouteStatsPanelProps> = ({ stats }) => {
  if (stats === undefined) {
    return <p className="text-sm text-gray-500">No statistics yet.</p>;
  }
  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-2 gap-3">
        <Stat label="Distance" value={formatKm(stats.distanceMeters)} />
        <Stat
          label="Elevation gain"
          value={formatMeters(stats.elevationGainMeters)}
        />
        <Stat
          label="Elevation loss"
          value={formatMeters(stats.elevationLossMeters)}
        />
        {stats.repeatedDistanceMeters !== undefined && (
          <Stat
            label="Repeated"
            value={formatKm(stats.repeatedDistanceMeters)}
            sub={
              stats.distanceMeters > 0
                ? `${formatPercent(stats.repeatedDistanceMeters / stats.distanceMeters)} retraces the route`
                : undefined
            }
          />
        )}
      </div>
      <div className="flex flex-col gap-1">
        <span className="text-xs font-medium tracking-wide text-gray-500 uppercase">
          Terrain
        </span>
        <TerrainBar terrain={stats.terrain} />
      </div>
    </div>
  );
};
