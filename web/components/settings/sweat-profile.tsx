import Loading from "@components/loading";
import Card from "@components/ui/card";
import { TextField } from "@components/ui/fields";
import QueryError, { ErrorText } from "@components/ui/query-error";
import { primaryButton } from "@components/ui/styles";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import {
  getSweatProfile,
  setSweatProfile,
} from "gen/plans/v1/plans-TrainingPlanService_connectquery";
import type { SweatProfile } from "gen/plans/v1/plans_pb";
import { TrainingPlanService } from "gen/plans/v1/plans_pb";
import { invalidateServices } from "helpers/query";
import React, { useState } from "react";

// ParseOptional parses a positive number, undefined if it's empty.
const parseOptional = (value: string): number | undefined | null => {
  if (value.trim() === "") {
    return undefined;
  }
  const parsed = Number(value.replace(",", "."));
  return Number.isFinite(parsed) && parsed > 0 ? parsed : null;
};

interface FormProps {
  profile: SweatProfile | undefined;
}

// Form edits the sweat profile, starting from the stored one.
const Form: React.FC<FormProps> = ({ profile }) => {
  const queryClient = useQueryClient();
  const [sodium, setSodium] = useState(
    profile?.sodiumMgPerLiter === undefined
      ? ""
      : String(profile.sodiumMgPerLiter)
  );
  const [rate, setRate] = useState(
    profile?.rateMlPerHour === undefined ? "" : String(profile.rateMlPerHour)
  );
  const [invalid, setInvalid] = useState(false);
  const save = useMutation(setSweatProfile, {
    onSuccess: async () => {
      // Runs' sodium depends on it.
      await invalidateServices(queryClient, [TrainingPlanService]);
    },
  });
  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={(event) => {
        event.preventDefault();
        const sodiumMgPerLiter = parseOptional(sodium);
        const rateMlPerHour = parseOptional(rate);
        if (sodiumMgPerLiter === null || rateMlPerHour === null) {
          setInvalid(true);
          return;
        }
        setInvalid(false);
        save.mutate({ profile: { rateMlPerHour, sodiumMgPerLiter } });
      }}
    >
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <TextField
          label="Sweat sodium (mg/L)"
          value={sodium}
          onChange={setSodium}
          inputMode="numeric"
          placeholder="e.g. 950"
          hint="From a sweat test. For mmol/L, multiply by 23."
        />
        <TextField
          label="Sweat rate (ml/h)"
          value={rate}
          onChange={setRate}
          inputMode="numeric"
          placeholder="800"
          hint="Running easy in mild weather. Empty for 800 ml/h."
        />
      </div>
      {invalid && (
        <p className="text-sm text-red-700">
          Enter positive numbers, or leave a field empty.
        </p>
      )}
      <ErrorText error={save.error} />
      <div className="flex items-center gap-3">
        <button
          type="submit"
          className={primaryButton}
          disabled={save.isPending}
        >
          {save.isPending ? "Saving…" : "Save"}
        </button>
        {save.isSuccess && (
          <span className="text-sm text-gray-600">Saved.</span>
        )}
      </div>
    </form>
  );
};

/**
 * SweatProfileCard sets how the user sweats, which runs with a fueling plan
 * use to work out the sodium to take: the sodium lost in the sweat expected
 * for the run's time, the weather and how hard it is.
 */
const SweatProfileCard: React.FC = () => {
  const { isPending, isError, data, error, refetch } =
    useQuery(getSweatProfile);
  let content: React.ReactNode = <Loading />;
  if (isError) {
    content = (
      <QueryError
        error={error}
        onRetry={() => {
          void refetch();
        }}
      />
    );
  } else if (!isPending) {
    content = <Form profile={data.profile} />;
  }
  return (
    <Card title="Sweat">
      <div className="flex flex-col gap-3">
        <p className="text-sm text-gray-600">
          Runs with a fueling plan estimate how much you sweat, from your rate,
          the forecast and how hard the run is, and the sodium to take is what
          you lose in it, unless the run sets its own.
        </p>
        {content}
      </div>
    </Card>
  );
};

export default SweatProfileCard;
