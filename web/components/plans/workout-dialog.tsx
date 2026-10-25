import { SelectField, TextAreaField, TextField } from "@components/ui/fields";
import type { SelectOption } from "@components/ui/fields";
import Modal from "@components/ui/modal";
import { ErrorText } from "@components/ui/query-error";
import {
  dangerButton,
  linkClass,
  primaryButton,
  secondaryButton,
} from "@components/ui/styles";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import type { Activity } from "gen/activities/v1/activities_pb";
import { Terrain } from "gen/geo/v1/geo_pb";
import {
  createWorkouts,
  deleteWorkouts,
  updateWorkout,
} from "gen/plans/v1/plans-TrainingPlanService_connectquery";
import type { Workout } from "gen/plans/v1/plans_pb";
import { TrainingPlanService, WorkoutType } from "gen/plans/v1/plans_pb";
import { listRoutes } from "gen/routes/v1/routes-RouteService_connectquery";
import { isDateString, isWithin } from "helpers/dates";
import { formatKm } from "helpers/format";
import { TERRAIN_STYLES, WORKOUT_STYLES, sportLabel } from "helpers/labels";
import {
  hoursMinutesInput,
  kmInput,
  numberInput,
  parseDuration,
  parseEdited,
  parseKm,
  parseMeters,
} from "helpers/parse";
import { invalidateServices } from "helpers/query";
import React, { useState } from "react";
import { Link } from "react-router";

interface WorkoutDialogProps {
  planId: string;
  planStart: string;
  planEnd: string;
  // The workout to edit, or undefined to create a new workout.
  workout: Workout | undefined;
  // The date for a new workout.
  date: string;
  // Activities near the workout date, to mark the workout as completed.
  activities: Activity[];
  onClose: () => void;
  // Called once the workout is deleted, before the plan is refreshed.
  onDeleted?: () => void;
}

const TYPE_OPTIONS: SelectOption[] = WORKOUT_STYLES.map((style) => ({
  label: style.label,
  value: String(style.type),
}));

const TERRAIN_OPTIONS: SelectOption[] = TERRAIN_STYLES.map((style) => ({
  label: style.terrain === Terrain.UNSPECIFIED ? "Any terrain" : style.label,
  value: String(style.terrain),
}));

// WorkoutDialog creates, edits and deletes a workout.
const WorkoutDialog: React.FC<WorkoutDialogProps> = ({
  planId,
  planStart,
  planEnd,
  workout,
  date: initialDate,
  activities,
  onClose,
  onDeleted,
}) => {
  const queryClient = useQueryClient();
  const routes = useQuery(listRoutes, {});
  const [date, setDate] = useState(workout?.date ?? initialDate);
  const [type, setType] = useState(String(workout?.type ?? WorkoutType.BASE));
  const [title, setTitle] = useState(workout?.title ?? "");
  const [description, setDescription] = useState(workout?.description ?? "");
  const [distance, setDistance] = useState(
    kmInput(workout?.targetDistanceMeters ?? 0)
  );
  const [vert, setVert] = useState(
    numberInput(workout?.targetElevationGainMeters ?? 0)
  );
  const [duration, setDuration] = useState(
    hoursMinutesInput(workout?.targetDurationSeconds ?? 0)
  );
  const [terrain, setTerrain] = useState(String(workout?.targetTerrain ?? 0));
  const [routeId, setRouteId] = useState(workout?.routeId ?? "");
  const [completedId, setCompletedId] = useState(
    workout?.completedActivityId ?? ""
  );
  const [validation, setValidation] = useState("");

  const onSuccess = async () => {
    await invalidateServices(queryClient, [TrainingPlanService]);
    onClose();
  };
  const create = useMutation(createWorkouts, { onSuccess });
  const update = useMutation(updateWorkout, { onSuccess });
  const remove = useMutation(deleteWorkouts, {
    onSuccess: async () => {
      onDeleted?.();
      await onSuccess();
    },
  });
  const pending = create.isPending || update.isPending || remove.isPending;

  const save = () => {
    // Unedited inputs keep the stored values, which they show rounded.
    const distanceMeters = parseEdited(
      distance,
      workout?.targetDistanceMeters ?? 0,
      kmInput,
      parseKm
    );
    const vertMeters = parseEdited(
      vert,
      workout?.targetElevationGainMeters ?? 0,
      numberInput,
      parseMeters
    );
    const durationSeconds = parseEdited(
      duration,
      workout?.targetDurationSeconds ?? 0,
      hoursMinutesInput,
      parseDuration
    );
    if (!isDateString(date) || !isWithin(date, planStart, planEnd)) {
      setValidation("The date must be within the plan.");
      return;
    }
    if (distanceMeters === null || vertMeters === null) {
      setValidation("Distance and elevation must be positive numbers.");
      return;
    }
    if (durationSeconds === null) {
      setValidation("Duration must be in the format h:mm.");
      return;
    }
    setValidation("");
    const workoutType: WorkoutType = Number(type);
    const targetTerrain: Terrain = Number(terrain);
    const fields = {
      completedActivityId: completedId,
      date,
      description,
      routeId,
      targetDistanceMeters: distanceMeters,
      targetDurationSeconds: durationSeconds,
      targetElevationGainMeters: vertMeters,
      targetTerrain,
      title,
      type: workoutType,
    };
    if (workout === undefined) {
      create.mutate({ planId, workouts: [fields] });
      return;
    }
    // Only send the fields that changed.
    const paths: string[] = [];
    if (fields.date !== workout.date) {
      paths.push("date");
    }
    if (fields.type !== workout.type) {
      paths.push("type");
    }
    if (fields.title !== workout.title) {
      paths.push("title");
    }
    if (fields.description !== workout.description) {
      paths.push("description");
    }
    if (fields.targetDistanceMeters !== workout.targetDistanceMeters) {
      paths.push("target_distance_meters");
    }
    if (
      fields.targetElevationGainMeters !== workout.targetElevationGainMeters
    ) {
      paths.push("target_elevation_gain_meters");
    }
    if (fields.targetDurationSeconds !== workout.targetDurationSeconds) {
      paths.push("target_duration_seconds");
    }
    if (fields.targetTerrain !== workout.targetTerrain) {
      paths.push("target_terrain");
    }
    if (fields.routeId !== workout.routeId) {
      paths.push("route_id");
    }
    if (fields.completedActivityId !== workout.completedActivityId) {
      paths.push("completed_activity_id");
    }
    if (paths.length === 0) {
      onClose();
      return;
    }
    update.mutate({
      updateMask: { paths },
      workout: { ...fields, id: workout.id },
    });
  };

  const routeOptions: SelectOption[] = [{ label: "No route", value: "" }];
  if (routes.data !== undefined) {
    for (const route of routes.data.routes) {
      const distanceLabel =
        route.stats === undefined
          ? ""
          : ` (${formatKm(route.stats.distanceMeters)}, ${Math.round(route.stats.elevationGainMeters)} m)`;
      routeOptions.push({
        label: `${route.name}${distanceLabel}`,
        value: route.id,
      });
    }
  }

  const dayActivities = activities.filter(
    (activity) =>
      activity.startDateLocal === date || activity.id === completedId
  );
  const activityOptions: SelectOption[] = [
    { label: "Not completed", value: "" },
  ];
  for (const activity of dayActivities) {
    activityOptions.push({
      label: `${activity.name} · ${sportLabel(activity.sportType)} · ${formatKm(activity.distanceMeters)}`,
      value: activity.id,
    });
  }
  if (completedId !== "" && dayActivities.length === 0) {
    activityOptions.push({ label: "Linked activity", value: completedId });
  }

  const designLink =
    workout === undefined
      ? ""
      : `/routes/new?workout=${encodeURIComponent(workout.id)}`;

  return (
    <Modal
      open
      onClose={onClose}
      title={workout === undefined ? "Add workout" : "Edit workout"}
      wide
    >
      <form
        className="flex flex-col gap-3"
        onSubmit={(event) => {
          event.preventDefault();
          save();
        }}
      >
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <TextField
            label="Date"
            type="date"
            value={date}
            onChange={setDate}
            required
          />
          <SelectField
            label="Type"
            value={type}
            onChange={setType}
            options={TYPE_OPTIONS}
          />
          <SelectField
            label="Terrain"
            value={terrain}
            onChange={setTerrain}
            options={TERRAIN_OPTIONS}
          />
        </div>
        <TextField
          label="Title"
          value={title}
          onChange={setTitle}
          placeholder="e.g. Long run with 1000 m vert"
          autoFocus
        />
        <TextAreaField
          label="Description"
          value={description}
          onChange={setDescription}
          placeholder="Structure, intensity and purpose"
        />
        <div className="grid grid-cols-3 gap-3">
          <TextField
            label="Distance (km)"
            value={distance}
            onChange={setDistance}
            inputMode="decimal"
          />
          <TextField
            label="Elevation gain (m)"
            value={vert}
            onChange={setVert}
            inputMode="numeric"
          />
          <TextField
            label="Duration (h:mm)"
            value={duration}
            onChange={setDuration}
            placeholder="1:30"
          />
        </div>
        <div className="flex flex-col gap-1">
          <SelectField
            label="Route"
            value={routeId}
            onChange={setRouteId}
            options={routeOptions}
          />
          <div className="flex gap-3 text-xs">
            {routeId !== "" && (
              <Link to={`/routes/${routeId}`} className={linkClass}>
                View route
              </Link>
            )}
            {designLink === "" ? (
              <span className="text-gray-500">
                Save the workout to design a route for it.
              </span>
            ) : (
              <Link to={designLink} className={linkClass}>
                Design a new route
              </Link>
            )}
          </div>
        </div>
        <SelectField
          label="Completed by activity"
          value={completedId}
          onChange={setCompletedId}
          options={activityOptions}
        />
        {validation !== "" && (
          <p className="text-sm text-red-700">{validation}</p>
        )}
        <ErrorText error={create.error ?? update.error ?? remove.error} />
        <div className="flex flex-wrap justify-between gap-2 pt-2">
          <div>
            {workout !== undefined && (
              <button
                type="button"
                className={dangerButton}
                disabled={pending}
                onClick={() => {
                  remove.mutate({ ids: [workout.id], planId });
                }}
              >
                Delete
              </button>
            )}
          </div>
          <div className="flex gap-2">
            <button type="button" className={secondaryButton} onClick={onClose}>
              Cancel
            </button>
            <button type="submit" className={primaryButton} disabled={pending}>
              Save
            </button>
          </div>
        </div>
      </form>
    </Modal>
  );
};

export default WorkoutDialog;
