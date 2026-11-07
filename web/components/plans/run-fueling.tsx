import Card from "@components/ui/card";
import { useQuery } from "@connectrpc/connect-query";
import { getWorkoutWeather } from "gen/plans/v1/plans-TrainingPlanService_connectquery";
import type { FuelAmounts, RunFueling, Workout } from "gen/plans/v1/plans_pb";
import { formatHoursMinutes } from "helpers/format";
import React from "react";

// Forecasts change slowly, so they're kept for half an hour, as on the page.
const WEATHER_STALE_MS = 30 * 60 * 1000;

// FormatFluid formats milliliters, in liters from a liter up.
const formatFluid = (ml: number): string =>
  ml >= 1000 ? `${(ml / 1000).toFixed(1)} L` : `${Math.round(ml)} ml`;

// Amounts lists the amounts of each kind of fuel there is any of.
const amountParts = (amounts: FuelAmounts): string[] => {
  const parts: string[] = [];
  if (amounts.carbsGrams > 0) {
    parts.push(`${Math.round(amounts.carbsGrams)} g carbs`);
  }
  if (amounts.fluidMl > 0) {
    parts.push(`${formatFluid(amounts.fluidMl)} fluid`);
  }
  if (amounts.sodiumMg > 0) {
    parts.push(`${Math.round(amounts.sodiumMg)} mg sodium`);
  }
  return parts;
};

// RateParts lists the rates per hour of each kind of fuel there is any of.
const rateParts = (fueling: RunFueling): string[] => {
  const parts: string[] = [];
  if (fueling.carbsGramsPerHour > 0) {
    parts.push(`${fueling.carbsGramsPerHour} g carbs`);
  }
  if (fueling.fluidMlPerHour > 0) {
    parts.push(`${fueling.fluidMlPerHour} ml fluid`);
  }
  if (fueling.sodiumMgPerHour > 0) {
    parts.push(`${fueling.sodiumMgPerHour} mg sodium`);
  }
  return parts;
};

interface RunFuelingCardProps {
  workout: Workout;
  fueling: RunFueling;
}

/**
 * RunFuelingCard shows what to eat and drink during a run: the amounts for
 * its expected time, raised for the heat in its forecast if there is one,
 * and the rates and notes they come from.
 */
const RunFuelingCard: React.FC<RunFuelingCardProps> = ({
  workout,
  fueling,
}) => {
  // The same query as the weather card's, so it's fetched once.
  const weather = useQuery(
    getWorkoutWeather,
    { workoutId: workout.id },
    { retry: false, staleTime: WEATHER_STALE_MS }
  );
  const forecast = weather.data?.weather?.fueling;
  const amounts = forecast ?? fueling.amounts;
  const feelsLike = forecast?.apparentTemperatureCelsius;

  return (
    <Card title="Fueling">
      <div className="flex flex-col gap-2 text-sm text-gray-700">
        {amounts === undefined ? (
          <p>
            Set the run&apos;s target time, or its distance and climb, to work
            out how much to carry.
          </p>
        ) : (
          <>
            <p className="text-lg font-semibold text-gray-900 tabular-nums">
              {amountParts(amounts).join(" · ")}
            </p>
            {amounts.sweatMl > 0 && (
              <p className="text-gray-600 tabular-nums">
                You&apos;ll sweat about {formatFluid(amounts.sweatMl)}
                {amounts.sodiumLossMg > 0 &&
                  `, losing ${Math.round(amounts.sodiumLossMg)} mg of sodium`}
                {amounts.sodiumFromSweat ? ", which the sodium replaces." : "."}
              </p>
            )}
            <p className="text-gray-600">
              For about {formatHoursMinutes(amounts.expectedDurationSeconds)}
              {workout.details.case === "run" &&
                workout.details.value.targetDurationSeconds === 0 &&
                ", from the distance and climb"}
              .
              {feelsLike !== undefined &&
                (amounts.heatIncreasePercent > 0
                  ? ` Fluid and sodium are raised ${amounts.heatIncreasePercent}% for the forecast, which feels like up to ${Math.round(feelsLike)}°.`
                  : ` The forecast feels like up to ${Math.round(feelsLike)}°, so there's no extra for the heat.`)}
            </p>
          </>
        )}
        <p className="text-xs text-gray-500">
          {rateParts(fueling).join(", ")} an hour.
        </p>
        {fueling.notes !== "" && (
          <p className="whitespace-pre-line">{fueling.notes}</p>
        )}
      </div>
    </Card>
  );
};

export default RunFuelingCard;
