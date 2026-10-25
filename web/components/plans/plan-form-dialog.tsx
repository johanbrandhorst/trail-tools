import RaceDialog from "@components/races/race-dialog";
import { CheckboxField, TextAreaField, TextField } from "@components/ui/fields";
import Modal from "@components/ui/modal";
import { ErrorText } from "@components/ui/query-error";
import {
  ghostButton,
  labelClass,
  primaryButton,
  secondaryButton,
} from "@components/ui/styles";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { PlusIcon } from "@heroicons/react/24/outline";
import { useQueryClient } from "@tanstack/react-query";
import {
  createTrainingPlan,
  updateTrainingPlan,
} from "gen/plans/v1/plans-TrainingPlanService_connectquery";
import type { TrainingPlan } from "gen/plans/v1/plans_pb";
import { TrainingPlanService } from "gen/plans/v1/plans_pb";
import { listRaces } from "gen/races/v1/races-RaceService_connectquery";
import { RaceService } from "gen/races/v1/races_pb";
import { addDays, daysBetween, isDateString, today } from "helpers/dates";
import { formatDate } from "helpers/format";
import { invalidateServices } from "helpers/query";
import React, { useState } from "react";
import { useNavigate } from "react-router";

interface PlanFormDialogProps {
  // The plan to edit, or undefined to create a plan.
  plan: TrainingPlan | undefined;
  onClose: () => void;
}

// PlanFormDialog creates or edits a training plan's details.
const PlanFormDialog: React.FC<PlanFormDialogProps> = ({ plan, onClose }) => {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const races = useQuery(listRaces, {});
  const [name, setName] = useState(plan?.name ?? "");
  const [description, setDescription] = useState(plan?.description ?? "");
  // The dates the form starts with, to tell whether they've been edited.
  const [initialDates] = useState(() => ({
    endDate: plan?.endDate ?? addDays(today(), 16 * 7 - 1),
    startDate: plan?.startDate ?? today(),
  }));
  const [startDate, setStartDate] = useState(initialDates.startDate);
  const [endDate, setEndDate] = useState(initialDates.endDate);
  const [goalRaceIds, setGoalRaceIds] = useState<string[]>(
    plan?.goalRaceIds ?? []
  );
  const [validation, setValidation] = useState("");
  const [addingRace, setAddingRace] = useState(false);

  const create = useMutation(createTrainingPlan, {
    onSuccess: async (response) => {
      // Goal races are added to the calendar.
      await invalidateServices(queryClient, [TrainingPlanService, RaceService]);
      onClose();
      if (response.plan !== undefined) {
        void navigate(`/plans/${response.plan.id}`);
      }
    },
  });
  const update = useMutation(updateTrainingPlan, {
    onSuccess: async () => {
      // Goal races are added to the calendar.
      await invalidateServices(queryClient, [TrainingPlanService, RaceService]);
      onClose();
    },
  });

  const dirty =
    name !== (plan?.name ?? "") ||
    description !== (plan?.description ?? "") ||
    startDate !== initialDates.startDate ||
    endDate !== initialDates.endDate ||
    goalRaceIds.join() !== (plan?.goalRaceIds ?? []).join();

  const toggleRace = (id: string, checked: boolean) => {
    if (checked) {
      setGoalRaceIds([...goalRaceIds, id]);
    } else {
      setGoalRaceIds(goalRaceIds.filter((raceId) => raceId !== id));
    }
  };

  const submit = () => {
    if (name.trim() === "") {
      setValidation("The plan needs a name.");
      return;
    }
    if (!isDateString(startDate) || !isDateString(endDate)) {
      setValidation("Start and end dates are required.");
      return;
    }
    if (endDate < startDate) {
      setValidation("The end date must be after the start date.");
      return;
    }
    if (daysBetween(startDate, endDate) > 366) {
      setValidation("A plan can be at most a year long.");
      return;
    }
    setValidation("");
    const fields = {
      description,
      endDate,
      goalRaceIds,
      name: name.trim(),
      startDate,
    };
    if (plan === undefined) {
      create.mutate({ plan: fields });
      return;
    }
    update.mutate({
      plan: { ...fields, id: plan.id },
      updateMask: {
        paths: [
          "name",
          "description",
          "start_date",
          "end_date",
          "goal_race_ids",
        ],
      },
    });
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={plan === undefined ? "New training plan" : "Edit training plan"}
      wide
      dirty={dirty}
    >
      <form
        className="flex flex-col gap-3"
        onSubmit={(event) => {
          event.preventDefault();
          submit();
        }}
      >
        <TextField
          label="Name"
          value={name}
          onChange={setName}
          placeholder="e.g. Road to UTMB"
          autoFocus
          required
        />
        <TextAreaField
          label="Description"
          value={description}
          onChange={setDescription}
          rows={4}
          placeholder="Background, goals, constraints and the overall approach"
        />
        <div className="grid grid-cols-2 gap-3">
          <TextField
            label="Start date"
            type="date"
            value={startDate}
            onChange={setStartDate}
            required
          />
          <TextField
            label="End date"
            type="date"
            value={endDate}
            onChange={setEndDate}
            required
          />
        </div>
        <div className="flex flex-col gap-1">
          <div className="flex items-center justify-between">
            <span className={labelClass}>Goal races</span>
            <button
              type="button"
              className={ghostButton}
              onClick={() => {
                setAddingRace(true);
              }}
            >
              <PlusIcon className="size-4" />
              Add race
            </button>
          </div>
          {races.data === undefined || races.data.races.length === 0 ? (
            <p className="text-sm text-gray-500">
              {races.isPending
                ? "Loading races…"
                : "No races yet. Add the races you're training for to set goals."}
            </p>
          ) : (
            <div className="flex max-h-48 flex-col gap-1 overflow-y-auto rounded-md border border-gray-200 p-2">
              {races.data.races.map((race) => (
                <CheckboxField
                  key={race.id}
                  checked={goalRaceIds.includes(race.id)}
                  onChange={(checked) => {
                    toggleRace(race.id, checked);
                  }}
                  label={
                    <>
                      {race.name}{" "}
                      <span className="text-gray-500">
                        {formatDate(race.date)}
                        {race.onCalendar && " · on calendar"}
                      </span>
                    </>
                  }
                />
              ))}
            </div>
          )}
          <p className="text-xs text-gray-500">
            Goal races are added to your calendar.
          </p>
        </div>
        {validation !== "" && (
          <p className="text-sm text-red-700">{validation}</p>
        )}
        <ErrorText error={create.error ?? update.error} />
        <div className="flex justify-end gap-2 pt-2">
          <button type="button" className={secondaryButton} onClick={onClose}>
            Cancel
          </button>
          <button
            type="submit"
            className={primaryButton}
            disabled={create.isPending || update.isPending}
          >
            {plan === undefined ? "Create plan" : "Save"}
          </button>
        </div>
      </form>
      {addingRace && (
        <RaceDialog
          race={undefined}
          defaultOnCalendar
          onClose={() => {
            setAddingRace(false);
          }}
          onSaved={(race) => {
            setGoalRaceIds((ids) => [...ids, race.id]);
          }}
        />
      )}
    </Modal>
  );
};

export default PlanFormDialog;
