import { create as createMessage } from "@bufbuild/protobuf";
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
import {
  CrossTrainingSport,
  RunFuelingSchema,
  TrainingPlanService,
  WorkoutType,
} from "gen/plans/v1/plans_pb";
import { listRoutes } from "gen/routes/v1/routes-RouteService_connectquery";
import { isDateString, isWithin } from "helpers/dates";
import { formatKm } from "helpers/format";
import {
  CROSS_TRAINING_SPORT_OPTIONS,
  TERRAIN_STYLES,
  WORKOUT_STYLES,
  sportLabel,
} from "helpers/labels";
import {
  hoursMinutesInput,
  kmInput,
  numberInput,
  parseDuration,
  parseEdited,
  parseEnum,
  parseKm,
  parseMeters,
} from "helpers/parse";
import { invalidateServices } from "helpers/query";
import {
  detailsPath,
  hasRoute,
  workoutDetails,
  workoutTargets,
} from "helpers/workouts";
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

// WINDOW_OPTIONS are the lengths of start windows to choose from.
const WINDOW_OPTIONS: SelectOption[] = [15, 30, 45, 60, 90, 120, 180, 240].map(
  (minutes) => ({
    label:
      minutes < 60
        ? `${minutes} minutes`
        : `${minutes / 60} ${minutes === 60 ? "hour" : "hours"}`,
    value: String(minutes),
  })
);

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
  const stored = workout === undefined ? undefined : workoutTargets(workout);
  const [distance, setDistance] = useState(
    kmInput(stored?.distanceMeters ?? 0)
  );
  const [vert, setVert] = useState(
    numberInput(stored?.elevationGainMeters ?? 0)
  );
  const [duration, setDuration] = useState(
    hoursMinutesInput(stored?.durationSeconds ?? 0)
  );
  const [terrain, setTerrain] = useState(String(stored?.terrain ?? 0));
  const [sport, setSport] = useState(
    String(stored?.crossTrainingSport ?? CrossTrainingSport.UNSPECIFIED)
  );
  const [routeId, setRouteId] = useState(stored?.routeId ?? "");
  const [lactateTest, setLactateTest] = useState(stored?.lactateTest ?? false);
  const storedFueling =
    workout?.details.case === "run" ? workout.details.value.fueling : undefined;
  const [carbs, setCarbs] = useState(
    numberInput(storedFueling?.carbsGramsPerHour ?? 0)
  );
  const [fluid, setFluid] = useState(
    numberInput(storedFueling?.fluidMlPerHour ?? 0)
  );
  const [sodium, setSodium] = useState(
    numberInput(storedFueling?.sodiumMgPerHour ?? 0)
  );
  const [completedId, setCompletedId] = useState(
    workout?.completedActivityId ?? ""
  );
  const [startTime, setStartTime] = useState(workout?.startTime ?? "");
  const [windowMinutes, setWindowMinutes] = useState(
    String(
      workout === undefined || workout.startWindowMinutes === 0
        ? 60
        : workout.startWindowMinutes
    )
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
      stored?.distanceMeters ?? 0,
      kmInput,
      parseKm
    );
    const vertMeters = parseEdited(
      vert,
      stored?.elevationGainMeters ?? 0,
      numberInput,
      parseMeters
    );
    const durationSeconds = parseEdited(
      duration,
      stored?.durationSeconds ?? 0,
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
    const rates = [carbs, fluid, sodium].map((value) =>
      value.trim() === "" ? 0 : Number(value)
    );
    if (rates.some((rate) => !Number.isFinite(rate) || rate < 0)) {
      setValidation("Fueling rates must be positive numbers.");
      return;
    }
    setValidation("");
    const workoutType = parseEnum(WorkoutType, type, WorkoutType.BASE);
    const targetTerrain = parseEnum(Terrain, terrain, Terrain.UNSPECIFIED);
    // Fields the kind of workout doesn't have are left out.
    const targets = {
      crossTrainingSport: parseEnum(
        CrossTrainingSport,
        sport,
        CrossTrainingSport.UNSPECIFIED
      ),
      distanceMeters: distanceMeters,
      durationSeconds: durationSeconds,
      elevationGainMeters: vertMeters,
      lactateTest,
      routeId: hasRoute(workoutType) ? routeId : "",
      terrain: targetTerrain,
    };
    const details = workoutDetails(workoutType, targets, workout);
    if (details.case === "run") {
      // A run without rates has no fueling plan; its notes are kept.
      const [carbsRate = 0, fluidRate = 0, sodiumRate = 0] = rates;
      details.value.fueling =
        carbsRate === 0 && fluidRate === 0 && sodiumRate === 0
          ? undefined
          : createMessage(RunFuelingSchema, {
              carbsGramsPerHour: carbsRate,
              fluidMlPerHour: fluidRate,
              notes: storedFueling?.notes ?? "",
              sodiumMgPerHour: sodiumRate,
            });
    }
    const fields = {
      completedActivityId: completedId,
      date,
      description,
      details,
      startTime,
      startWindowMinutes: startTime === "" ? 0 : Number(windowMinutes),
      title,
    };
    if (workout === undefined) {
      create.mutate({ planId, workouts: [fields] });
      return;
    }
    /*
     * Only send the fields that changed. The details of the kind of
     * workout are replaced as a whole.
     */
    const paths: string[] = [];
    if (fields.date !== workout.date) {
      paths.push("date");
    }
    if (fields.title !== workout.title) {
      paths.push("title");
    }
    if (fields.description !== workout.description) {
      paths.push("description");
    }
    const before = workoutDetails(
      workout.type,
      workoutTargets(workout),
      workout
    );
    if (
      workoutType !== workout.type ||
      JSON.stringify(details.value) !== JSON.stringify(before.value)
    ) {
      paths.push(detailsPath(workoutType));
    }
    if (fields.completedActivityId !== workout.completedActivityId) {
      paths.push("completed_activity_id");
    }
    if (
      fields.startTime !== workout.startTime ||
      fields.startWindowMinutes !== workout.startWindowMinutes
    ) {
      paths.push("start_time", "start_window_minutes");
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

  const selectedType = parseEnum(WorkoutType, type, WorkoutType.BASE);
  const isRun =
    selectedType === WorkoutType.BASE || selectedType === WorkoutType.INTENSITY;
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
          {isRun && (
            <SelectField
              label="Terrain"
              value={terrain}
              onChange={setTerrain}
              options={TERRAIN_OPTIONS}
            />
          )}
          {selectedType === WorkoutType.CROSS_TRAINING && (
            <SelectField
              label="Sport"
              value={sport}
              onChange={setSport}
              options={CROSS_TRAINING_SPORT_OPTIONS}
            />
          )}
        </div>
        <div className="grid grid-cols-2 gap-3">
          <TextField
            label="Start from"
            type="time"
            value={startTime}
            onChange={setStartTime}
            hint="Empty for any time of the day."
          />
          {startTime !== "" && (
            <SelectField
              label="Start within"
              value={windowMinutes}
              onChange={setWindowMinutes}
              options={WINDOW_OPTIONS}
            />
          )}
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
        {selectedType !== WorkoutType.REST && (
          <div className="grid grid-cols-3 gap-3">
            {selectedType !== WorkoutType.STRENGTH && (
              <>
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
              </>
            )}
            <TextField
              label="Duration (h:mm)"
              value={duration}
              onChange={setDuration}
              placeholder="1:30"
            />
          </div>
        )}
        {isRun && (
          <fieldset className="flex flex-col gap-1">
            <legend className="text-sm font-medium text-gray-700">
              Fueling per hour
            </legend>
            <div className="grid grid-cols-3 gap-3">
              <TextField
                label="Carbs (g)"
                value={carbs}
                onChange={setCarbs}
                inputMode="numeric"
              />
              <TextField
                label="Fluid (ml)"
                value={fluid}
                onChange={setFluid}
                inputMode="numeric"
              />
              <TextField
                label="Sodium (mg)"
                value={sodium}
                onChange={setSodium}
                inputMode="numeric"
              />
            </div>
            <p className="text-xs text-gray-500">
              The amounts to carry are worked out from the run&apos;s time, or
              its distance and climb, and fluid and sodium are raised for the
              heat in the forecast. Leave empty for no fueling plan. With your
              sweat sodium in Settings, leave sodium empty to replace what you
              sweat out.
            </p>
          </fieldset>
        )}
        {isRun && (
          <label className="flex items-start gap-2 text-sm text-gray-700">
            <input
              type="checkbox"
              className="mt-1"
              checked={lactateTest}
              onChange={(event) => {
                setLactateTest(event.target.checked);
              }}
            />
            <span>
              Lactate test
              <span className="block text-xs text-gray-500">
                The workout page starts the test, or sends a link for someone
                else to record it, and keeps its result.
              </span>
            </span>
          </label>
        )}
        {selectedType === WorkoutType.STRENGTH && (
          <p className="text-sm text-gray-600">
            Plan the exercises of strength workouts through the MCP server, from
            the exercise library.
          </p>
        )}
        {hasRoute(selectedType) && (
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
        )}
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
