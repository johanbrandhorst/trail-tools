import { TextAreaField, TextField } from "@components/ui/fields";
import Modal from "@components/ui/modal";
import { ErrorText } from "@components/ui/query-error";
import {
  dangerButton,
  primaryButton,
  secondaryButton,
} from "@components/ui/styles";
import { useMutation } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import {
  deletePlanPeriods,
  setPlanPeriods,
} from "gen/plans/v1/plans-TrainingPlanService_connectquery";
import type { PeriodGranularity, PlanPeriod } from "gen/plans/v1/plans_pb";
import { TrainingPlanService } from "gen/plans/v1/plans_pb";
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

interface PeriodDialogProps {
  planId: string;
  granularity: PeriodGranularity;
  startDate: string;
  // The existing period, if any.
  period: PlanPeriod | undefined;
  title: string;
  onClose: () => void;
}

// PeriodDialog edits the focus and targets of a month or week.
const PeriodDialog: React.FC<PeriodDialogProps> = ({
  planId,
  granularity,
  startDate,
  period,
  title: dialogTitle,
  onClose,
}) => {
  const queryClient = useQueryClient();
  const [title, setTitle] = useState(period?.title ?? "");
  const [notes, setNotes] = useState(period?.notes ?? "");
  const [distance, setDistance] = useState(
    kmInput(period?.targetDistanceMeters ?? 0)
  );
  const [vert, setVert] = useState(
    numberInput(period?.targetElevationGainMeters ?? 0)
  );
  const [duration, setDuration] = useState(
    hoursMinutesInput(period?.targetDurationSeconds ?? 0)
  );
  const [validation, setValidation] = useState("");

  const onSuccess = async () => {
    await invalidateServices(queryClient, [TrainingPlanService]);
    onClose();
  };
  const save = useMutation(setPlanPeriods, { onSuccess });
  const remove = useMutation(deletePlanPeriods, { onSuccess });

  return (
    <Modal open onClose={onClose} title={dialogTitle}>
      <form
        className="flex flex-col gap-3"
        onSubmit={(event) => {
          event.preventDefault();
          // Unedited inputs keep the stored values they show rounded.
          const distanceMeters = parseEdited(
            distance,
            period?.targetDistanceMeters ?? 0,
            kmInput,
            parseKm
          );
          const vertMeters = parseEdited(
            vert,
            period?.targetElevationGainMeters ?? 0,
            numberInput,
            parseMeters
          );
          const durationSeconds = parseEdited(
            duration,
            period?.targetDurationSeconds ?? 0,
            hoursMinutesInput,
            parseDuration
          );
          if (
            distanceMeters === null ||
            vertMeters === null ||
            durationSeconds === null
          ) {
            setValidation(
              "Targets must be positive numbers and the duration h:mm."
            );
            return;
          }
          setValidation("");
          save.mutate({
            periods: [
              {
                granularity,
                notes,
                startDate,
                targetDistanceMeters: distanceMeters,
                targetDurationSeconds: durationSeconds,
                targetElevationGainMeters: vertMeters,
                title,
              },
            ],
            planId,
          });
        }}
      >
        <TextField
          label="Title"
          value={title}
          onChange={setTitle}
          placeholder="e.g. Base 2, Recovery week, Taper"
          autoFocus
        />
        <TextAreaField
          label="Focus and notes"
          value={notes}
          onChange={setNotes}
          rows={4}
        />
        <div className="grid grid-cols-3 gap-3">
          <TextField
            label="Target distance (km)"
            value={distance}
            onChange={setDistance}
            inputMode="decimal"
          />
          <TextField
            label="Target gain (m)"
            value={vert}
            onChange={setVert}
            inputMode="numeric"
          />
          <TextField
            label="Target time (h:mm)"
            value={duration}
            onChange={setDuration}
          />
        </div>
        {validation !== "" && (
          <p className="text-sm text-red-700">{validation}</p>
        )}
        <ErrorText error={save.error ?? remove.error} />
        <div className="flex justify-between gap-2 pt-2">
          <div>
            {period !== undefined && (
              <button
                type="button"
                className={dangerButton}
                disabled={remove.isPending}
                onClick={() => {
                  remove.mutate({ ids: [period.id], planId });
                }}
              >
                Clear
              </button>
            )}
          </div>
          <div className="flex gap-2">
            <button type="button" className={secondaryButton} onClick={onClose}>
              Cancel
            </button>
            <button
              type="submit"
              className={primaryButton}
              disabled={save.isPending}
            >
              Save
            </button>
          </div>
        </div>
      </form>
    </Modal>
  );
};

export default PeriodDialog;
