import Loading from "@components/loading";
import CoverageWarning from "@components/routes/coverage-warning";
import {
  CreatedRouteWarning,
  SnapProgress,
  createdRouteLink,
} from "@components/routes/route-created";
import type { RouteDraft } from "@components/routes/route-editor";
import RouteEditor from "@components/routes/route-editor";
import { hasEditableGeometry } from "@components/routes/route-edits";
import RouteViewer from "@components/routes/route-viewer";
import Card from "@components/ui/card";
import { CheckboxField, SelectField, TextField } from "@components/ui/fields";
import type { SelectOption } from "@components/ui/fields";
import QueryError, { ErrorText } from "@components/ui/query-error";
import { primaryButton } from "@components/ui/styles";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { skipToken, useQueryClient } from "@tanstack/react-query";
import { listActivities } from "gen/activities/v1/activities-ActivityService_connectquery";
import {
  getWorkout,
  updateWorkout,
} from "gen/plans/v1/plans-TrainingPlanService_connectquery";
import type { Workout } from "gen/plans/v1/plans_pb";
import { TrainingPlanService, WorkoutType } from "gen/plans/v1/plans_pb";
import {
  createRoute,
  getRoute,
  updateRoute,
} from "gen/routes/v1/routes-RouteService_connectquery";
import type { CheckTrackCoverageRequest, Route } from "gen/routes/v1/routes_pb";
import { RouteService, TrackConversion } from "gen/routes/v1/routes_pb";
import { formatDate, formatHoursMinutes, formatKm } from "helpers/format";
import { workoutStyle } from "helpers/labels";
import { invalidateServices } from "helpers/query";
import { hasRoute, workoutDetails, workoutTargets } from "helpers/workouts";
import React, { useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";

// SafeReturnPath only allows navigating back to paths within the app.
const safeReturnPath = (value: string | null): string | null => {
  if (value === null || !value.startsWith("/") || value.startsWith("//")) {
    return null;
  }
  return value;
};

/**
 * UseRouteCreated returns a function to call after creating a route. When the
 * route was designed for a workout (/routes/new?workout=<id>), the workout is
 * linked to the route and the user is sent back to the workout's plan, or to
 * the path in the optional return parameter.
 */
const useRouteCreated = () => {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const workoutId = searchParams.get("workout") ?? "";
  const workoutQuery = useQuery(
    getWorkout,
    workoutId === "" ? skipToken : { id: workoutId }
  );
  const { workout } = workoutQuery.data ?? {};
  let returnPath = safeReturnPath(searchParams.get("return"));
  if (returnPath === null && workout !== undefined) {
    returnPath = `/plans/${workout.planId}?month=${workout.date.slice(0, 7)}`;
  }
  const linkWorkout = useMutation(updateWorkout);

  const onCreated = async (route: Route | undefined, warning = "") => {
    await invalidateServices(queryClient, [RouteService]);
    if (route === undefined) {
      return;
    }
    if (workoutId !== "") {
      if (workout !== undefined) {
        /*
         * The route is part of the run's or race's details, which are
         * replaced as a whole. A workout that can't have a route becomes an
         * easy run.
         */
        const details = workoutDetails(
          hasRoute(workout.type) ? workout.type : WorkoutType.BASE,
          { ...workoutTargets(workout), routeId: route.id },
          workout
        );
        await linkWorkout.mutateAsync({
          updateMask: { paths: [details.case === "race" ? "race" : "run"] },
          workout: { details, id: workoutId },
        });
      }
      await invalidateServices(queryClient, [TrainingPlanService]);
      if (returnPath === null) {
        void navigate(-1);
      } else {
        void navigate(returnPath);
      }
      return;
    }
    void navigate(...createdRouteLink(route.id, warning));
  };
  return {
    linkError: linkWorkout.error ?? workoutQuery.error,
    onCreated,
    workout,
    workoutId,
  };
};

interface ExactTrackFieldProps {
  exact: boolean;
  onChange: (exact: boolean) => void;
  // The track, to warn if it would be snapped outside the map data.
  source: CheckTrackCoverageRequest["source"] | undefined;
}

/**
 * ExactTrackField chooses between keeping a recorded track exactly and
 * snapping it to trails as an editable route.
 */
const ExactTrackField: React.FC<ExactTrackFieldProps> = ({
  exact,
  onChange,
  source,
}) => (
  <div className="flex flex-col gap-1">
    <CheckboxField
      label="Keep the exact track"
      checked={exact}
      onChange={onChange}
    />
    <p className="text-xs text-gray-500">
      {exact
        ? "The route follows the recording exactly, e.g. for an official race course, but can't be reshaped."
        : "The route is snapped to trails with as few waypoints as possible, so you can reshape it by moving them. This can take a little while."}
    </p>
    {!exact && <CoverageWarning source={source} />}
  </div>
);

const trackConversion = (exact: boolean): TrackConversion =>
  exact ? TrackConversion.EXACT : TrackConversion.SNAP;

const ImportGpx: React.FC = () => {
  const { onCreated, linkError } = useRouteCreated();
  const [name, setName] = useState("");
  const [gpx, setGpx] = useState("");
  const [fileName, setFileName] = useState("");
  const [exact, setExact] = useState(false);
  const create = useMutation(createRoute, {
    onSuccess: async (response) => {
      await onCreated(response.route, response.warning);
    },
  });
  const source = gpx === "" ? undefined : { case: "gpx" as const, value: gpx };
  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={(event) => {
        event.preventDefault();
        if (source === undefined) {
          return;
        }
        create.mutate({
          conversion: trackConversion(exact),
          name: name.trim() === "" ? fileName : name.trim(),
          source,
        });
      }}
    >
      <label className="flex flex-col gap-1 text-sm">
        <span className="text-xs font-medium text-gray-600">GPX file</span>
        <input
          type="file"
          accept=".gpx,application/gpx+xml"
          className="text-sm file:mr-3 file:rounded-md file:border-0 file:bg-brand-50 file:px-3 file:py-1.5 file:text-brand-700 hover:file:bg-brand-100"
          onChange={(event) => {
            const file = event.target.files?.[0];
            if (file === undefined) {
              return;
            }
            setFileName(file.name.replace(/\.gpx$/iu, ""));
            void file.text().then(setGpx);
          }}
        />
      </label>
      <TextField
        label="Name"
        value={name}
        onChange={setName}
        placeholder={fileName === "" ? "Route name" : fileName}
      />
      <ExactTrackField exact={exact} onChange={setExact} source={source} />
      <SnapProgress pending={create.isPending && !exact} />
      <ErrorText error={create.error ?? linkError} />
      <div>
        <button
          type="submit"
          className={primaryButton}
          disabled={gpx === "" || create.isPending}
        >
          {create.isPending ? "Importing…" : "Import route"}
        </button>
      </div>
    </form>
  );
};

const FromActivity: React.FC = () => {
  const { onCreated, linkError } = useRouteCreated();
  const activities = useQuery(listActivities, { pageSize: 50 });
  const [activityId, setActivityId] = useState("");
  const [name, setName] = useState("");
  const [exact, setExact] = useState(false);
  const create = useMutation(createRoute, {
    onSuccess: async (response) => {
      await onCreated(response.route, response.warning);
    },
  });
  if (activities.isPending) {
    return <Loading />;
  }
  if (activities.isError) {
    return <QueryError error={activities.error} />;
  }
  const withGps = activities.data.activities.filter(
    (activity) => activity.summaryPolyline !== ""
  );
  if (withGps.length === 0) {
    return (
      <p className="text-sm text-gray-500">
        No activities with GPS data yet. Connect Strava in{" "}
        <Link to="/settings" className="text-brand-600 hover:underline">
          Settings
        </Link>
        .
      </p>
    );
  }
  const options: SelectOption[] = [{ label: "Choose an activity", value: "" }];
  for (const activity of withGps) {
    options.push({
      label: `${formatDate(activity.startDateLocal)} · ${activity.name} · ${formatKm(activity.distanceMeters)}`,
      value: activity.id,
    });
  }
  const selected = withGps.find((activity) => activity.id === activityId);
  const source =
    selected === undefined
      ? undefined
      : { case: "activityId" as const, value: selected.id };
  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={(event) => {
        event.preventDefault();
        if (selected === undefined || source === undefined) {
          return;
        }
        create.mutate({
          conversion: trackConversion(exact),
          name: name.trim() === "" ? selected.name : name.trim(),
          source,
        });
      }}
    >
      <SelectField
        label="Activity"
        value={activityId}
        onChange={setActivityId}
        options={options}
      />
      <TextField
        label="Name"
        value={name}
        onChange={setName}
        placeholder={selected === undefined ? "Route name" : selected.name}
      />
      <ExactTrackField exact={exact} onChange={setExact} source={source} />
      <SnapProgress pending={create.isPending && !exact} />
      <ErrorText error={create.error ?? linkError} />
      <div>
        <button
          type="submit"
          className={primaryButton}
          disabled={selected === undefined || create.isPending}
        >
          {create.isPending ? "Creating…" : "Create route"}
        </button>
      </div>
    </form>
  );
};

type NewMode = "draw" | "gpx" | "activity";

interface WorkoutContextProps {
  workout: Workout;
}

// WorkoutContext describes the workout a route is being designed for.
const WorkoutContext: React.FC<WorkoutContextProps> = ({ workout }) => {
  const targets: string[] = [];
  const target = workoutTargets(workout);
  if (target.distanceMeters > 0) {
    targets.push(formatKm(target.distanceMeters));
  }
  if (target.elevationGainMeters > 0) {
    targets.push(`${Math.round(target.elevationGainMeters)} m`);
  }
  if (target.durationSeconds > 0) {
    targets.push(formatHoursMinutes(target.durationSeconds));
  }
  const { label } = workoutStyle(workout.type);
  return (
    <span>
      Designing route for:{" "}
      <span className="font-semibold">
        {workout.title === "" ? label : `${workout.title} · ${label}`}
      </span>{" "}
      on {formatDate(workout.date)}
      {targets.length > 0 && ` · target ${targets.join(" / ")}`}.
    </span>
  );
};

const NewRoute: React.FC = () => {
  const navigate = useNavigate();
  const [mode, setMode] = useState<NewMode>("draw");
  const { onCreated, linkError, workout, workoutId } = useRouteCreated();
  const create = useMutation(createRoute, {
    onSuccess: async (response) => {
      await onCreated(response.route, response.warning);
    },
  });

  const tabs: { mode: NewMode; label: string }[] = [
    { label: "Draw on map", mode: "draw" },
    { label: "Import GPX", mode: "gpx" },
    { label: "From activity", mode: "activity" },
  ];

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <Link to="/routes" className="text-sm text-gray-500 hover:underline">
            Routes
          </Link>
          <h1 className="text-2xl font-bold text-gray-900">New route</h1>
          {workoutId !== "" && (
            <div className="mt-1 rounded-md bg-brand-50 px-3 py-2 text-sm text-brand-800 ring-1 ring-brand-200">
              {workout === undefined ? (
                "Designing a route for a workout."
              ) : (
                <WorkoutContext workout={workout} />
              )}{" "}
              <span className="text-brand-600">
                The route is assigned to the workout when saved.
              </span>
            </div>
          )}
        </div>
        <div className="inline-flex overflow-hidden rounded-md ring-1 ring-gray-300">
          {tabs.map((tab) => (
            <button
              key={tab.mode}
              type="button"
              className={`px-3 py-1.5 text-sm ${mode === tab.mode ? "bg-gray-800 text-white" : "bg-white text-gray-700 hover:bg-gray-50"}`}
              onClick={() => {
                setMode(tab.mode);
              }}
            >
              {tab.label}
            </button>
          ))}
        </div>
      </div>
      {mode === "draw" && (
        <RouteEditor
          route={undefined}
          defaultName={workout?.title ?? ""}
          geometryEditable
          saving={create.isPending}
          error={create.error ?? linkError}
          onCancel={() => {
            void navigate(-1);
          }}
          onSave={(draft: RouteDraft) => {
            create.mutate({
              description: draft.description,
              name: draft.name,
              source: {
                case: "waypoints",
                value: {
                  legProfiles: draft.legProfiles,
                  profile: draft.profile,
                  waypoints: draft.waypoints,
                },
              },
            });
          }}
        />
      )}
      {mode === "gpx" && (
        <Card title="Import a GPX file" className="max-w-xl">
          <ImportGpx />
        </Card>
      )}
      {mode === "activity" && (
        <Card title="Create a route from an activity" className="max-w-xl">
          <FromActivity />
        </Card>
      )}
    </div>
  );
};

const ExistingRoute: React.FC = () => {
  const { id = "" } = useParams();
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState(false);
  const { isPending, isError, data, error, refetch } = useQuery(getRoute, {
    id,
    includePoints: true,
  });
  const update = useMutation(updateRoute, {
    onSuccess: async () => {
      await invalidateServices(queryClient, [RouteService]);
      setEditing(false);
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
  const { route } = data;
  if (route === undefined) {
    return <p>Route not found.</p>;
  }
  const geometryEditable = hasEditableGeometry(route);

  if (!editing) {
    return (
      <div className="flex flex-col gap-2">
        <Link to="/routes" className="text-sm text-gray-500 hover:underline">
          Routes
        </Link>
        <CreatedRouteWarning />
        <RouteViewer
          route={route}
          onEdit={() => {
            update.reset();
            setEditing(true);
          }}
        />
      </div>
    );
  }
  return (
    <div className="flex flex-col gap-2">
      {!geometryEditable && (
        <p className="rounded-md bg-amber-50 p-2 text-sm text-amber-900">
          This route follows a recording exactly, so only its name and
          description can be edited. Use Make editable on the route to create a
          copy snapped to trails with a few waypoints you can move.
        </p>
      )}
      <RouteEditor
        route={route}
        geometryEditable={geometryEditable}
        saving={update.isPending}
        error={update.error}
        onCancel={() => {
          setEditing(false);
        }}
        onSave={(draft: RouteDraft) => {
          update.mutate({
            description: draft.description,
            id: route.id,
            name: draft.name,
            waypoints:
              geometryEditable && draft.geometryChanged
                ? {
                    legProfiles: draft.legProfiles,
                    profile: draft.profile,
                    waypoints: draft.waypoints,
                  }
                : undefined,
          });
        }}
      />
    </div>
  );
};

interface RoutePageProps {
  isNew: boolean;
}

const RoutePage: React.FC<RoutePageProps> = ({ isNew }) =>
  isNew ? <NewRoute /> : <ExistingRoute />;

export default RoutePage;
