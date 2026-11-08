import Loading from "@components/loading";
import Card from "@components/ui/card";
import { SelectField, TextField } from "@components/ui/fields";
import type { SelectOption } from "@components/ui/fields";
import QueryError, { ErrorText } from "@components/ui/query-error";
import { primaryButton } from "@components/ui/styles";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { AvalancheDanger } from "gen/geo/v1/geo_pb";
import {
  getTouringLimits,
  setTouringLimits,
} from "gen/plans/v1/plans-TrainingPlanService_connectquery";
import type { TouringLimits } from "gen/plans/v1/plans_pb";
import { TrainingPlanService } from "gen/plans/v1/plans_pb";
import { RouteService } from "gen/routes/v1/routes_pb";
import { invalidateServices } from "helpers/query";
import React, { useState } from "react";

const DANGER_OPTIONS: SelectOption[] = [
  { label: "No limit", value: String(AvalancheDanger.UNSPECIFIED) },
  { label: "1 - Low", value: String(AvalancheDanger.LOW) },
  { label: "2 - Moderate", value: String(AvalancheDanger.MODERATE) },
  { label: "3 - Considerable", value: String(AvalancheDanger.CONSIDERABLE) },
  { label: "4 - High", value: String(AvalancheDanger.HIGH) },
  { label: "5 - Extreme", value: String(AvalancheDanger.EXTREME) },
];

// ParseOptional parses a positive number, undefined if it's empty.
const parseOptional = (value: string): number | undefined | null => {
  if (value.trim() === "") {
    return undefined;
  }
  const parsed = Number(value.replace(",", "."));
  return Number.isFinite(parsed) && parsed > 0 ? parsed : null;
};

// Text returns the text of an optional number for a field.
const text = (value: number | undefined): string =>
  value === undefined ? "" : String(value);

const DANGERS = [
  AvalancheDanger.UNSPECIFIED,
  AvalancheDanger.LOW,
  AvalancheDanger.MODERATE,
  AvalancheDanger.CONSIDERABLE,
  AvalancheDanger.HIGH,
  AvalancheDanger.EXTREME,
];

// ParseDanger parses a danger option.
const parseDanger = (value: string): AvalancheDanger =>
  DANGERS.find((danger) => String(danger) === value) ??
  AvalancheDanger.UNSPECIFIED;

interface FormProps {
  limits: TouringLimits | undefined;
}

// Form edits the touring limits, starting from the stored ones.
const Form: React.FC<FormProps> = ({ limits }) => {
  const queryClient = useQueryClient();
  const [alpine, setAlpine] = useState(
    String(limits?.maxAvalancheDangerAlpine ?? AvalancheDanger.UNSPECIFIED)
  );
  const [treeline, setTreeline] = useState(
    String(limits?.maxAvalancheDangerTreeline ?? AvalancheDanger.UNSPECIFIED)
  );
  const [belowTreeline, setBelowTreeline] = useState(
    String(
      limits?.maxAvalancheDangerBelowTreeline ?? AvalancheDanger.UNSPECIFIED
    )
  );
  const [gusts, setGusts] = useState(text(limits?.maxWindGustsKmh));
  const [freezingLevel, setFreezingLevel] = useState(
    text(limits?.maxFreezingLevelMeters)
  );
  const [snow, setSnow] = useState(text(limits?.maxSnowfallPast72HoursCm));
  const [invalid, setInvalid] = useState(false);
  const save = useMutation(setTouringLimits, {
    onSuccess: async () => {
      // Ski tours' verdicts depend on them.
      await invalidateServices(queryClient, [
        TrainingPlanService,
        RouteService,
      ]);
    },
  });
  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={(event) => {
        event.preventDefault();
        const maxWindGustsKmh = parseOptional(gusts);
        const maxFreezingLevelMeters = parseOptional(freezingLevel);
        const maxSnowfallPast72HoursCm = parseOptional(snow);
        if (
          maxWindGustsKmh === null ||
          maxFreezingLevelMeters === null ||
          maxSnowfallPast72HoursCm === null
        ) {
          setInvalid(true);
          return;
        }
        setInvalid(false);
        save.mutate({
          limits: {
            maxAvalancheDangerAlpine: parseDanger(alpine),
            maxAvalancheDangerBelowTreeline: parseDanger(belowTreeline),
            maxAvalancheDangerTreeline: parseDanger(treeline),
            maxFreezingLevelMeters,
            maxSnowfallPast72HoursCm,
            maxWindGustsKmh,
          },
        });
      }}
    >
      <fieldset className="flex flex-col gap-1">
        <legend className="text-sm font-medium text-gray-800">
          Highest avalanche danger
        </legend>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <SelectField
            label="Alpine"
            value={alpine}
            options={DANGER_OPTIONS}
            onChange={setAlpine}
          />
          <SelectField
            label="Treeline"
            value={treeline}
            options={DANGER_OPTIONS}
            onChange={setTreeline}
          />
          <SelectField
            label="Below treeline"
            value={belowTreeline}
            options={DANGER_OPTIONS}
            onChange={setBelowTreeline}
          />
        </div>
      </fieldset>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <TextField
          label="Strongest gusts (km/h)"
          value={gusts}
          onChange={setGusts}
          inputMode="numeric"
          placeholder="e.g. 60"
          hint="Within 10 km/h is a caution."
        />
        <TextField
          label="Highest freezing level (m)"
          value={freezingLevel}
          onChange={setFreezingLevel}
          inputMode="numeric"
          placeholder="e.g. 2500"
          hint="Warming brings wet avalanches. Within 200 m is a caution."
        />
        <TextField
          label="Most new snow in 3 days (cm)"
          value={snow}
          onChange={setSnow}
          inputMode="numeric"
          placeholder="e.g. 30"
          hint="Within 5 cm is a caution."
        />
      </div>
      {invalid && (
        <p className="text-sm text-red-700">
          Enter positive numbers, or leave a field empty for no limit.
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
 * TouringLimitsCard sets the user's own limits for ski touring, which the
 * forecasts of ski tours are checked against for a go, caution or no-go
 * verdict.
 */
const TouringLimitsCard: React.FC = () => {
  const { isPending, isError, data, error, refetch } =
    useQuery(getTouringLimits);
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
    content = <Form limits={data.limits} />;
  }
  return (
    <div id="touring-limits" className="scroll-mt-20">
      <Card title="Touring limits">
        <div className="flex flex-col gap-3">
          <p className="text-sm text-gray-600">
            The conditions you won&apos;t ski tour in. Ski tours and route
            forecasts for ski tours check their forecast against them: beyond a
            limit is a no-go, and close to one, or without an avalanche forecast
            to check, a caution. Leave a limit empty to skip it.
          </p>
          {content}
        </div>
      </Card>
    </div>
  );
};

export default TouringLimitsCard;
