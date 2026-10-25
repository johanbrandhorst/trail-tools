import {
  CheckboxField,
  SelectField,
  TextAreaField,
  TextField,
} from "@components/ui/fields";
import type { SelectOption } from "@components/ui/fields";
import Modal from "@components/ui/modal";
import { ErrorText } from "@components/ui/query-error";
import { primaryButton, secondaryButton } from "@components/ui/styles";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { ArrowUpTrayIcon } from "@heroicons/react/24/outline";
import { useQueryClient } from "@tanstack/react-query";
import { RacePlanService } from "gen/races/v1/race_plans_pb";
import {
  createRace,
  createRaceEvent,
  listRaceEvents,
  updateRace,
} from "gen/races/v1/races-RaceService_connectquery";
import type { Race, RaceEvent } from "gen/races/v1/races_pb";
import { RacePriority, RaceService } from "gen/races/v1/races_pb";
import {
  createRoute,
  listRoutes,
} from "gen/routes/v1/routes-RouteService_connectquery";
import type { Route } from "gen/routes/v1/routes_pb";
import { RouteService, TrackConversion } from "gen/routes/v1/routes_pb";
import { addDays, isDateString } from "helpers/dates";
import { formatKm } from "helpers/format";
import { PRIORITY_OPTIONS } from "helpers/labels";
import {
  hoursMinutesInput,
  kmInput,
  numberInput,
  parseDuration,
  parseEdited,
  parseKm,
  parseMeters,
  parseNumber,
} from "helpers/parse";
import { invalidateServices } from "helpers/query";
import React, { useState } from "react";

interface RaceDialogProps {
  // The race to edit, or undefined to create a race.
  race: Race | undefined;
  onClose: () => void;
  // Called with the saved race before the dialog closes.
  onSaved?: (race: Race) => void;
  // Whether a new race starts on the calendar.
  defaultOnCalendar?: boolean;
  // The event a new race is part of.
  defaultEventId?: string;
}

// NEW_EVENT is the event option that creates an event with the race.
const NEW_EVENT = "new";

const PRIORITY_SELECT: SelectOption[] = PRIORITY_OPTIONS.map((option) => ({
  label: option.label,
  value: String(option.value),
}));

// RaceDialog creates or edits a race.
const RaceDialog: React.FC<RaceDialogProps> = ({
  race,
  onClose,
  onSaved,
  defaultOnCalendar = false,
  defaultEventId = "",
}) => {
  const queryClient = useQueryClient();
  const routes = useQuery(listRoutes);
  const events = useQuery(listRaceEvents);
  const [eventId, setEventId] = useState(race?.eventId ?? defaultEventId);
  const [eventName, setEventName] = useState("");
  /**
   * A race added to an event takes the event's location and website, on the
   * day after the event's last race.
   */
  const defaultEvent = events.data?.events.find(
    (event) => event.id === defaultEventId
  );
  const [name, setName] = useState(race?.name ?? "");
  const [date, setDate] = useState(
    race?.date ??
      (defaultEvent === undefined || defaultEvent.endDate === ""
        ? ""
        : addDays(defaultEvent.endDate, 1))
  );
  const [startTime, setStartTime] = useState(race?.startTime ?? "");
  const [location, setLocation] = useState(
    race?.location ?? defaultEvent?.location ?? ""
  );
  const [website, setWebsite] = useState(
    race?.websiteUrl ?? defaultEvent?.websiteUrl ?? ""
  );
  const [distance, setDistance] = useState(kmInput(race?.distanceMeters ?? 0));
  const [vert, setVert] = useState(numberInput(race?.elevationGainMeters ?? 0));
  const [cutoff, setCutoff] = useState(
    hoursMinutesInput(race?.cutoffSeconds ?? 0)
  );
  const [priority, setPriority] = useState(
    String(race?.priority ?? RacePriority.A)
  );
  const [courseRouteId, setCourseRouteId] = useState(race?.courseRouteId ?? "");
  const [notes, setNotes] = useState(race?.notes ?? "");
  const [onCalendar, setOnCalendar] = useState(
    race?.onCalendar ?? defaultOnCalendar
  );
  const [validation, setValidation] = useState("");

  const onSuccess = async (saved: Race | undefined) => {
    await invalidateServices(queryClient, [RaceService, RacePlanService]);
    if (saved !== undefined) {
      onSaved?.(saved);
    }
    onClose();
  };
  const create = useMutation(createRace, {
    onSuccess: async (response) => onSuccess(response.race),
  });
  const update = useMutation(updateRace, {
    onSuccess: async (response) => onSuccess(response.race),
  });
  const createEvent = useMutation(createRaceEvent);

  const eventOptions: SelectOption[] = [
    { label: "Not part of an event", value: "" },
  ];
  for (const event of events.data?.events ?? []) {
    eventOptions.push({ label: event.name, value: event.id });
  }
  eventOptions.push({ label: "New event…", value: NEW_EVENT });

  // A new race in an event takes the event's location and website.
  const chooseEvent = (id: string) => {
    setEventId(id);
    const event: RaceEvent | undefined = events.data?.events.find(
      (candidate) => candidate.id === id
    );
    if (race === undefined && event !== undefined) {
      if (location === "") {
        setLocation(event.location);
      }
      if (website === "") {
        setWebsite(event.websiteUrl);
      }
    }
  };

  // Fills in the distance and gain from the course, unless they're set.
  const fillCourseStats = (route: Route | undefined, overwrite: boolean) => {
    if (route?.stats === undefined) {
      return;
    }
    if (overwrite || (parseKm(distance) ?? 0) === 0) {
      setDistance(kmInput(route.stats.distanceMeters));
    }
    if (overwrite || (parseNumber(vert) ?? 0) === 0) {
      setVert(numberInput(Math.round(route.stats.elevationGainMeters)));
    }
  };

  // An uploaded GPX file is imported exactly, as race courses are fixed.
  const uploadCourse = useMutation(createRoute, {
    onSuccess: async (response) => {
      await invalidateServices(queryClient, [RouteService]);
      if (response.route !== undefined) {
        setCourseRouteId(response.route.id);
        fillCourseStats(response.route, false);
      }
    },
  });

  const routeOptions: SelectOption[] = [
    { label: "No course route", value: "" },
  ];
  for (const route of routes.data?.routes ?? []) {
    routeOptions.push({
      label:
        route.stats === undefined
          ? route.name
          : `${route.name} (${formatKm(route.stats.distanceMeters)})`,
      value: route.id,
    });
  }

  const applyCourseStats = () => {
    fillCourseStats(
      routes.data?.routes.find((candidate) => candidate.id === courseRouteId),
      true
    );
  };

  const submit = async () => {
    // Unedited inputs keep the stored values, which they show rounded.
    const distanceMeters = parseEdited(
      distance,
      race?.distanceMeters ?? 0,
      kmInput,
      parseKm
    );
    const vertMeters = parseEdited(
      vert,
      race?.elevationGainMeters ?? 0,
      numberInput,
      parseMeters
    );
    const cutoffSeconds = parseEdited(
      cutoff,
      race?.cutoffSeconds ?? 0,
      hoursMinutesInput,
      parseDuration
    );
    if (name.trim() === "" || !isDateString(date)) {
      setValidation("Name and date are required.");
      return;
    }
    if (distanceMeters === null || vertMeters === null) {
      setValidation("Distance and elevation must be positive numbers.");
      return;
    }
    if (cutoffSeconds === null) {
      setValidation("The time limit must be in the format h:mm.");
      return;
    }
    if (eventId === NEW_EVENT && eventName.trim() === "") {
      setValidation("Name the new event.");
      return;
    }
    setValidation("");
    let raceEventId = eventId;
    if (eventId === NEW_EVENT) {
      const created = await createEvent.mutateAsync({
        event: {
          location: location.trim(),
          name: eventName.trim(),
          websiteUrl: website.trim(),
        },
      });
      raceEventId = created.event?.id ?? "";
      setEventId(raceEventId);
    }
    const fields = {
      courseRouteId,
      cutoffSeconds,
      date,
      distanceMeters,
      elevationGainMeters: vertMeters,
      eventId: raceEventId,
      location,
      name: name.trim(),
      notes,
      onCalendar,
      priority: Number(priority),
      startTime,
      websiteUrl: website.trim(),
    };
    if (race === undefined) {
      create.mutate({ race: fields });
    } else {
      update.mutate({ race: { ...fields, id: race.id } });
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={race === undefined ? "Add race" : "Edit race"}
      wide
    >
      <form
        className="flex flex-col gap-3"
        onSubmit={(event) => {
          event.preventDefault();
          submit().catch(() => {
            // The error is shown from the mutation.
          });
        }}
      >
        <TextField
          label="Name"
          value={name}
          onChange={setName}
          placeholder="e.g. Lavaredo Ultra Trail 120K"
          autoFocus
          required
        />
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <SelectField
            label="Event"
            value={eventId}
            onChange={chooseEvent}
            options={eventOptions}
          />
          {eventId === NEW_EVENT && (
            <TextField
              label="Event name"
              value={eventName}
              onChange={setEventName}
              placeholder="e.g. Squamish 50/50"
            />
          )}
        </div>
        {eventId !== "" && (
          <p className="-mt-1 text-xs text-gray-500">
            For a multi-day event, add a race for each day, and name each race
            in full, e.g. &ldquo;Squamish 50 Mile&rdquo; and &ldquo;Squamish
            50K&rdquo;.
          </p>
        )}
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
          <TextField
            label="Date"
            type="date"
            value={date}
            onChange={setDate}
            required
          />
          <TextField
            label="Start time"
            type="time"
            value={startTime}
            onChange={setStartTime}
          />
          <SelectField
            label="Priority"
            value={priority}
            onChange={setPriority}
            options={PRIORITY_SELECT}
          />
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <TextField label="Location" value={location} onChange={setLocation} />
          <TextField
            label="Website"
            type="url"
            value={website}
            onChange={setWebsite}
            placeholder="https://"
          />
        </div>
        <div className="flex flex-col gap-1">
          <div className="flex items-end gap-2">
            <div className="grow">
              <SelectField
                label="Course route"
                value={courseRouteId}
                onChange={setCourseRouteId}
                options={routeOptions}
              />
            </div>
            <label
              className={`${secondaryButton} cursor-pointer ${uploadCourse.isPending ? "pointer-events-none opacity-50" : ""}`}
            >
              <ArrowUpTrayIcon className="size-4" />
              {uploadCourse.isPending ? "Uploading…" : "Upload GPX"}
              <input
                type="file"
                accept=".gpx,application/gpx+xml"
                className="sr-only"
                onChange={(event) => {
                  const file = event.target.files?.[0];
                  event.target.value = "";
                  if (file === undefined) {
                    return;
                  }
                  void file.text().then((gpx) => {
                    uploadCourse.mutate({
                      conversion: TrackConversion.EXACT,
                      name:
                        name.trim() === ""
                          ? file.name.replace(/\.gpx$/iu, "")
                          : name.trim(),
                      source: { case: "gpx", value: gpx },
                    });
                  });
                }}
              />
            </label>
          </div>
          <ErrorText error={uploadCourse.error} />
          <span className="text-xs text-gray-500">
            Upload the race's GPX file or pick a saved route. The course is used
            for segment elevation in race plans.
            {courseRouteId !== "" && (
              <>
                {" "}
                <button
                  type="button"
                  className="font-medium text-brand-600 hover:underline"
                  onClick={applyCourseStats}
                >
                  Use course distance and gain
                </button>
              </>
            )}
          </span>
        </div>
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
            label="Time limit (h:mm)"
            value={cutoff}
            onChange={setCutoff}
            placeholder="30:00"
          />
        </div>
        <TextAreaField
          label="Notes"
          value={notes}
          onChange={setNotes}
          placeholder="Terrain, weather, qualification requirements"
        />
        <CheckboxField
          label="On my calendar: I'm running this race"
          checked={onCalendar}
          onChange={setOnCalendar}
        />
        {validation !== "" && (
          <p className="text-sm text-red-700">{validation}</p>
        )}
        <ErrorText error={createEvent.error ?? create.error ?? update.error} />
        <div className="flex justify-end gap-2 pt-2">
          <button type="button" className={secondaryButton} onClick={onClose}>
            Cancel
          </button>
          <button
            type="submit"
            className={primaryButton}
            disabled={
              createEvent.isPending || create.isPending || update.isPending
            }
          >
            Save
          </button>
        </div>
      </form>
    </Modal>
  );
};

export default RaceDialog;
