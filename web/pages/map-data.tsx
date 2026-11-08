import Loading from "@components/loading";
import { useViewport } from "@components/map/layers";
import MapView, { useMap } from "@components/map/map-view";
import Card from "@components/ui/card";
import ConfirmButton from "@components/ui/confirm-button";
import { TextField } from "@components/ui/fields";
import { Badge, PageHeader, ProgressBar, Stat } from "@components/ui/misc";
import QueryError, { ErrorText } from "@components/ui/query-error";
import {
  ghostButton,
  ghostDangerButton,
  primaryButton,
  secondaryButton,
  tableClass,
  tdClass,
  thClass,
} from "@components/ui/styles";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { ArrowPathIcon, TrashIcon } from "@heroicons/react/24/outline";
import { skipToken, useQueryClient } from "@tanstack/react-query";
import {
  deleteRegion,
  findExtracts,
  getMapData,
  startImport,
} from "gen/routing/v1/routing-MapDataService_connectquery";
import type { Area, Import, Region } from "gen/routing/v1/routing_pb";
import { ImportState, MapDataService } from "gen/routing/v1/routing_pb";
import { formatBytes, formatList, formatTimestamp } from "helpers/format";
import { invalidateServices } from "helpers/query";
import type { LatLngTuple } from "leaflet";
import * as leaflet from "leaflet";
import React, { useEffect, useRef, useState } from "react";

// The storage of Neon's free plan, which the database should stay within.
const STORAGE_LIMIT_BYTES = 500_000_000;

/**
 * The share of the map's width and height left out around the selected area,
 * so that the selection is visible inside the map.
 */
const SELECTION_INSET = 0.1;

// The routing graph takes about 25 KB per square kilometer.
const BYTES_PER_SQUARE_KM = 25_000;

// The largest area that can be imported, in degrees, as the server allows.
const MAX_AREA_DEGREES = 3;

const KM_PER_DEGREE = 111.32;

interface Bounds {
  south: number;
  west: number;
  north: number;
  east: number;
}

const boundsOfArea = (area: Area | undefined): Bounds | null => {
  if (area?.southWest === undefined || area.northEast === undefined) {
    return null;
  }
  return {
    east: area.northEast.lng,
    north: area.northEast.lat,
    south: area.southWest.lat,
    west: area.southWest.lng,
  };
};

const toLatLngBounds = (bounds: Bounds): [LatLngTuple, LatLngTuple] => [
  [bounds.south, bounds.west],
  [bounds.north, bounds.east],
];

// SquareKm is the approximate area of the bounds in square kilometers.
const squareKm = (bounds: Bounds): number => {
  const middle = ((bounds.north + bounds.south) / 2) * (Math.PI / 180);
  return (
    (bounds.north - bounds.south) *
    KM_PER_DEGREE *
    (bounds.east - bounds.west) *
    KM_PER_DEGREE *
    Math.cos(middle)
  );
};

const tooLarge = (bounds: Bounds): boolean =>
  bounds.north - bounds.south > MAX_AREA_DEGREES ||
  bounds.east - bounds.west > MAX_AREA_DEGREES;

/**
 * Areas closer than this, in degrees, are shown together, as one place to
 * run. Areas further apart, like ones in different countries, are shown one
 * group at a time, as fitting them all would zoom out to the whole world.
 */
const NEARBY_DEGREES = 1;

interface RegionArea {
  region: Region;
  bounds: Bounds;
}

const nearby = (first: Bounds, second: Bounds): boolean =>
  first.south - NEARBY_DEGREES <= second.north &&
  second.south - NEARBY_DEGREES <= first.north &&
  first.west - NEARBY_DEGREES <= second.east &&
  second.west - NEARBY_DEGREES <= first.east;

// GroupNearby groups the areas that are near each other, directly or not.
const groupNearby = (areas: RegionArea[]): RegionArea[][] => {
  const groups: RegionArea[][] = [];
  for (const area of areas) {
    const near = groups.filter((group) =>
      group.some((other) => nearby(other.bounds, area.bounds))
    );
    const merged = [area, ...near.flat()];
    for (const group of near) {
      groups.splice(groups.indexOf(group), 1);
    }
    groups.push(merged);
  }
  return groups;
};

const groupSize = (group: RegionArea[]): number =>
  group.reduce((total, area) => total + Number(area.region.sizeBytes), 0);

/**
 * FocusGroup returns the areas to show: the group with a changed area, so
 * that a new import is shown, or else the group with the most areas, then
 * the most data.
 */
const focusGroup = (
  areas: RegionArea[],
  changed: Set<string>
): RegionArea[] => {
  const groups = groupNearby(areas);
  const withChange = groups.find((group) =>
    group.some((area) => changed.has(area.region.name))
  );
  if (withChange !== undefined) {
    return withChange;
  }
  let best: RegionArea[] = [];
  for (const group of groups) {
    if (
      group.length > best.length ||
      (group.length === best.length && groupSize(group) > groupSize(best))
    ) {
      best = group;
    }
  }
  return best;
};

const boundsKey = (bounds: Bounds): string =>
  [bounds.south, bounds.west, bounds.north, bounds.east].join(",");

interface FitAreasProps {
  areas: RegionArea[];
}

/**
 * FitAreas fits the map to the imported areas, a group of nearby ones at a
 * time, whenever an area is added, replaced or deleted.
 */
const FitAreas: React.FC<FitAreasProps> = ({ areas }) => {
  const map = useMap();
  // The bounds of each area last fitted, or null before the first fit.
  const fitted = useRef<Map<string, string> | null>(null);
  useEffect(() => {
    const current = new Map(
      areas.map((area) => [area.region.name, boundsKey(area.bounds)])
    );
    const previous = fitted.current;
    if (
      previous !== null &&
      previous.size === current.size &&
      [...current].every(([name, key]) => previous.get(name) === key)
    ) {
      return;
    }
    fitted.current = current;
    const changed = new Set<string>();
    if (previous !== null) {
      for (const [name, key] of current) {
        if (previous.get(name) !== key) {
          changed.add(name);
        }
      }
    }
    const group = focusGroup(areas, changed);
    if (group.length === 0) {
      return;
    }
    map.fitBounds(
      leaflet.latLngBounds(
        group.flatMap(({ bounds }) => toLatLngBounds(bounds))
      ),
      { maxZoom: 15, padding: [24, 24] }
    );
  }, [map, areas]);
  return null;
};

interface AreaLayerProps {
  bounds: Bounds;
  color: string;
  label: string;
  dashed?: boolean;
}

// AreaLayer draws a rectangle on the map.
const AreaLayer: React.FC<AreaLayerProps> = ({
  bounds,
  color,
  label,
  dashed = false,
}) => {
  const map = useMap();
  /*
   * Depend on the coordinates rather than the bounds object, which is new on
   * every render, so the rectangle isn't redrawn whenever the page renders.
   */
  const { south, west, north, east } = bounds;
  useEffect(() => {
    const rectangle = leaflet.rectangle(
      [
        [south, west],
        [north, east],
      ],
      {
        color,
        dashArray: dashed ? "6 6" : "",
        fillOpacity: 0.12,
        interactive: true,
        weight: 2,
      }
    );
    rectangle.bindTooltip(label);
    rectangle.addTo(map);
    return () => {
      rectangle.remove();
    };
  }, [map, south, west, north, east, color, label, dashed]);
  return null;
};

interface VisibleAreaProps {
  onChange: (bounds: Bounds) => void;
}

/**
 * VisibleArea reports the area shown on the map as it moves, inset from the
 * edges.
 */
const VisibleArea: React.FC<VisibleAreaProps> = ({ onChange }) => {
  const { bounds } = useViewport();
  useEffect(() => {
    const latInset = (bounds.getNorth() - bounds.getSouth()) * SELECTION_INSET;
    const lngInset = (bounds.getEast() - bounds.getWest()) * SELECTION_INSET;
    onChange({
      east: bounds.getEast() - lngInset,
      north: bounds.getNorth() - latInset,
      south: bounds.getSouth() + latInset,
      west: bounds.getWest() + lngInset,
    });
  }, [bounds, onChange]);
  return null;
};

const stateBadge = (state: ImportState): React.ReactNode => {
  switch (state) {
    case ImportState.QUEUED: {
      return (
        <Badge className="bg-sky-50 text-sky-800 ring-sky-200">Queued</Badge>
      );
    }
    case ImportState.RUNNING: {
      return (
        <Badge className="bg-sky-100 text-sky-900 ring-sky-300">
          Importing…
        </Badge>
      );
    }
    case ImportState.DONE: {
      return (
        <Badge className="bg-emerald-50 text-emerald-800 ring-emerald-200">
          Done
        </Badge>
      );
    }
    case ImportState.FAILED: {
      return (
        <Badge className="bg-red-50 text-red-800 ring-red-200">Failed</Badge>
      );
    }
    default: {
      return <Badge>Unknown</Badge>;
    }
  }
};

const isActive = (item: Import): boolean =>
  item.state === ImportState.QUEUED || item.state === ImportState.RUNNING;

interface RegionsTableProps {
  regions: Region[];
  // Whether an import is queued or running, so another can't start.
  importing: boolean;
}

const RegionsTable: React.FC<RegionsTableProps> = ({ regions, importing }) => {
  const queryClient = useQueryClient();
  const remove = useMutation(deleteRegion, {
    onSuccess: async () => {
      await invalidateServices(queryClient, [MapDataService]);
    },
  });
  /*
   * Importing a region again replaces it with the latest OpenStreetMap data
   * of the same area, picking up new trails, and data newer imports read,
   * like forests. Routing uses the old data until the import finishes.
   */
  const reimport = useMutation(startImport, {
    onSuccess: async () => {
      await invalidateServices(queryClient, [MapDataService]);
    },
  });
  if (regions.length === 0) {
    return (
      <p className="text-sm text-gray-600">
        No areas have been imported yet, so routes can only be drawn with
        straight lines.
      </p>
    );
  }
  return (
    <div className="flex flex-col gap-2">
      <div className="overflow-x-auto">
        <table className={tableClass}>
          <thead>
            <tr>
              <th className={thClass}>Area</th>
              <th className={`${thClass} hidden text-right sm:table-cell`}>
                Size
              </th>
              <th className={`${thClass} hidden text-right sm:table-cell`}>
                Paths
              </th>
              <th className={`${thClass} hidden sm:table-cell`}>Imported</th>
              <th className={thClass} />
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100">
            {regions.map((region) => (
              <tr key={region.name}>
                <td className={tdClass}>
                  <span className="font-medium">{region.name}</span>
                  {/* Phones have no room for the columns, so show a line. */}
                  <span className="block text-xs text-gray-500 tabular-nums sm:hidden">
                    {formatBytes(Number(region.sizeBytes))} ·{" "}
                    {region.edgeCount.toLocaleString()} paths · imported{" "}
                    {formatTimestamp(region.importTime)}
                  </span>
                </td>
                <td
                  className={`${tdClass} hidden text-right tabular-nums sm:table-cell`}
                >
                  {formatBytes(Number(region.sizeBytes))}
                </td>
                <td
                  className={`${tdClass} hidden text-right tabular-nums sm:table-cell`}
                >
                  {region.edgeCount.toLocaleString()}
                </td>
                <td className={`${tdClass} hidden sm:table-cell`}>
                  {formatTimestamp(region.importTime)}
                </td>
                <td className={`${tdClass} text-right whitespace-nowrap`}>
                  <button
                    type="button"
                    className={ghostButton}
                    title={
                      importing
                        ? "An import is running. Another can start once it finishes."
                        : `Import ${region.name} again with the latest OpenStreetMap data`
                    }
                    disabled={
                      importing ||
                      reimport.isPending ||
                      region.area === undefined
                    }
                    onClick={() => {
                      reimport.mutate({
                        area: region.area,
                        region: region.name,
                      });
                    }}
                  >
                    <ArrowPathIcon
                      className="size-4"
                      aria-label={`Import ${region.name} again`}
                    />
                  </button>
                  <ConfirmButton
                    className={ghostDangerButton}
                    title={`Delete ${region.name}?`}
                    message="Routes can no longer be routed along trails in this area. Saved routes are kept."
                    confirmLabel="Delete"
                    disabled={remove.isPending}
                    onConfirm={() => {
                      remove.mutate({ name: region.name });
                    }}
                  >
                    <TrashIcon
                      className="size-4"
                      aria-label={`Delete ${region.name}`}
                    />
                  </ConfirmButton>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <ErrorText error={remove.error} />
      <ErrorText error={reimport.error} />
    </div>
  );
};

interface ImportsListProps {
  imports: Import[];
}

const ImportsList: React.FC<ImportsListProps> = ({ imports }) => {
  if (imports.length === 0) {
    return <p className="text-sm text-gray-600">No imports yet.</p>;
  }
  return (
    <ul className="divide-y divide-gray-100">
      {imports.map((item) => (
        <li key={item.id} className="flex flex-col gap-1 py-2">
          <div className="flex flex-wrap items-center gap-2">
            {stateBadge(item.state)}
            <span className="text-sm font-medium text-gray-900">
              {item.region}
            </span>
            <span className="text-xs text-gray-500">
              from {formatList(item.extractNames)} · started{" "}
              {formatTimestamp(item.createTime)}
            </span>
          </div>
          {item.state === ImportState.DONE && (
            <p className="text-xs text-gray-600">
              {item.edgeCount.toLocaleString()} paths imported
            </p>
          )}
          {item.message !== "" && (
            <p className="text-xs text-red-700">{item.message}</p>
          )}
        </li>
      ))}
    </ul>
  );
};

interface ImportFormProps {
  selected: Bounds | null;
  visible: Bounds | null;
  importing: boolean;
  onSelect: (bounds: Bounds | null) => void;
}

const ImportForm: React.FC<ImportFormProps> = ({
  selected,
  visible,
  importing,
  onSelect,
}) => {
  const queryClient = useQueryClient();
  const [region, setRegion] = useState("");
  const area =
    selected === null || tooLarge(selected)
      ? undefined
      : {
          northEast: { lat: selected.north, lng: selected.east },
          southWest: { lat: selected.south, lng: selected.west },
        };
  const extracts = useQuery(
    findExtracts,
    area === undefined ? skipToken : { area }
  );
  const start = useMutation(startImport, {
    onSuccess: async () => {
      onSelect(null);
      setRegion("");
      await invalidateServices(queryClient, [MapDataService]);
    },
  });

  let details: React.ReactNode = (
    <p className="text-sm text-gray-600">
      Move the map to the area you run in, then select it. The selection leaves
      out the edges of the map.
    </p>
  );
  if (selected !== null) {
    const km = squareKm(selected);
    details = (
      <div className="flex flex-col gap-1 text-sm text-gray-700">
        <p>
          About {Math.round(km).toLocaleString()} km², taking up to about{" "}
          {formatBytes(km * BYTES_PER_SQUARE_KM)} once imported.
        </p>
        {tooLarge(selected) && (
          <p className="text-red-700">
            The area is too large. Zoom in to select at most {MAX_AREA_DEGREES}{" "}
            degrees across.
          </p>
        )}
        {extracts.data !== undefined && (
          <p className="text-gray-600">
            Read from the {formatList(extracts.data.extractNames)}{" "}
            {extracts.data.extractNames.length === 1 ? "extract" : "extracts"}{" "}
            of OpenStreetMap.
          </p>
        )}
        <ErrorText error={extracts.error} />
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-3">
      {details}
      <div className="flex flex-wrap gap-2">
        <button
          type="button"
          className={secondaryButton}
          disabled={visible === null}
          onClick={() => {
            onSelect(visible);
          }}
        >
          Select the middle of the map
        </button>
        {selected !== null && (
          <button
            type="button"
            className={ghostButton}
            onClick={() => {
              onSelect(null);
            }}
          >
            Clear
          </button>
        )}
      </div>
      {selected !== null && (
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            if (area !== undefined) {
              start.mutate({ area, region: region.trim() });
            }
          }}
        >
          <TextField
            label="Name"
            value={region}
            placeholder="e.g. central-switzerland"
            hint="Lowercase letters, digits and dashes. Importing a name again replaces it."
            className="min-w-56 grow"
            onChange={setRegion}
          />
          <button
            type="submit"
            className={primaryButton}
            disabled={
              area === undefined ||
              region.trim() === "" ||
              importing ||
              start.isPending
            }
          >
            Import
          </button>
        </form>
      )}
      {importing && (
        <p className="text-xs text-gray-500">
          An import is running. Another can start once it finishes.
        </p>
      )}
      <ErrorText error={start.error} />
    </div>
  );
};

/**
 * MapDataPage shows the areas of OpenStreetMap data imported for routing and
 * the space they take, and imports new areas. Only administrators can use
 * it.
 */
const MapDataPage: React.FC = () => {
  const [selected, setSelected] = useState<Bounds | null>(null);
  const [visible, setVisible] = useState<Bounds | null>(null);
  const { isPending, isError, data, error, refetch } = useQuery(
    getMapData,
    {},
    {
      // Follow running imports.
      refetchInterval: (query) =>
        (query.state.data?.imports ?? []).some(isActive) ? 5000 : false,
    }
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
    const regionAreas: RegionArea[] = [];
    for (const region of data.regions) {
      const bounds = boundsOfArea(region.area);
      if (bounds !== null) {
        regionAreas.push({ bounds, region });
      }
    }
    const databaseSize = Number(data.databaseSizeBytes);
    const regionsSize = data.regions.reduce(
      (total, region) => total + Number(region.sizeBytes),
      0
    );
    /*
     * On phones the cards are one column, with importing right below the map
     * it works on. On wider screens the columns' contents are laid out on
     * their own, and the order classes no longer apply.
     */
    content = (
      <div className="flex flex-col gap-4 lg:grid lg:grid-cols-[minmax(0,1fr)_380px] lg:items-start">
        <div className="contents lg:flex lg:min-w-0 lg:flex-col lg:gap-4">
          <div className="order-1 overflow-hidden rounded-lg border border-gray-200 lg:order-none">
            <MapView className="h-[55vh] min-h-80" rememberView>
              {regionAreas.map(({ region, bounds }) => (
                <AreaLayer
                  key={region.name}
                  bounds={bounds}
                  color="#d2601a"
                  label={`${region.name} · ${formatBytes(Number(region.sizeBytes))}`}
                />
              ))}
              {selected !== null && (
                <AreaLayer
                  bounds={selected}
                  color={tooLarge(selected) ? "#b91c1c" : "#c2410c"}
                  label="Selected area"
                  dashed
                />
              )}
              <VisibleArea onChange={setVisible} />
              <FitAreas areas={regionAreas} />
            </MapView>
          </div>
          <Card title="Imported areas" className="order-3 lg:order-none">
            <RegionsTable
              regions={data.regions}
              importing={data.imports.some(isActive)}
            />
          </Card>
        </div>
        <div className="contents lg:flex lg:flex-col lg:gap-4">
          <Card title="Import an area" className="order-2 lg:order-none">
            <ImportForm
              selected={selected}
              visible={visible}
              importing={data.imports.some(isActive)}
              onSelect={setSelected}
            />
          </Card>
          <Card title="Storage" className="order-4 lg:order-none">
            <div className="flex flex-col gap-3">
              <div className="grid grid-cols-2 gap-3">
                <Stat
                  label="Database"
                  value={formatBytes(databaseSize)}
                  sub={`of ${formatBytes(STORAGE_LIMIT_BYTES)}`}
                />
                <Stat label="Map data" value={formatBytes(regionsSize)} />
              </div>
              <ProgressBar value={databaseSize} target={STORAGE_LIMIT_BYTES} />
              <p className="text-xs text-gray-500">
                Neon&apos;s free plan has {formatBytes(STORAGE_LIMIT_BYTES)} of
                storage. Delete areas you no longer run in to free space.
              </p>
            </div>
          </Card>
          <Card title="Recent imports" className="order-5 lg:order-none">
            <ImportsList imports={data.imports} />
          </Card>
        </div>
      </div>
    );
  }

  return (
    <div>
      <PageHeader title="Map data" />
      <p className="mb-4 max-w-3xl text-sm text-gray-600">
        Routes are routed along trails using OpenStreetMap data imported into
        the database. Import the areas you run in, and delete areas you no
        longer need.
      </p>
      {content}
    </div>
  );
};

export default MapDataPage;
