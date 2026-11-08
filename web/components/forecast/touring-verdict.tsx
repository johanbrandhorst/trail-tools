import { linkClass } from "@components/ui/styles";
import {
  CheckCircleIcon,
  ExclamationTriangleIcon,
  XCircleIcon,
} from "@heroicons/react/24/solid";
import type { TouringVerdict } from "gen/geo/v1/geo_pb";
import { TouringVerdictLevel } from "gen/geo/v1/geo_pb";
import React from "react";
import { Link } from "react-router";

interface LevelStyle {
  label: string;
  // The classes of the verdict's banner.
  banner: string;
  // The classes of a check's icon.
  icon: string;
  Icon: typeof CheckCircleIcon;
}

const levelStyle = (level: TouringVerdictLevel): LevelStyle => {
  switch (level) {
    case TouringVerdictLevel.NO_GO: {
      return {
        Icon: XCircleIcon,
        banner: "border-red-300 bg-red-50 text-red-900",
        icon: "text-red-600",
        label: "No-go",
      };
    }
    case TouringVerdictLevel.CAUTION: {
      return {
        Icon: ExclamationTriangleIcon,
        banner: "border-amber-300 bg-amber-50 text-amber-900",
        icon: "text-amber-600",
        label: "Caution",
      };
    }
    default: {
      return {
        Icon: CheckCircleIcon,
        banner: "border-green-300 bg-green-50 text-green-900",
        icon: "text-green-600",
        label: "Go",
      };
    }
  }
};

interface TouringVerdictViewProps {
  // The verdict, undefined if the user hasn't set any touring limits.
  verdict: TouringVerdict | undefined;
}

/**
 * TouringVerdictView shows the verdict on a ski tour from checking its
 * forecast against the user's touring limits, with each check, or a link
 * to set the limits if there are none.
 */
const TouringVerdictView: React.FC<TouringVerdictViewProps> = ({ verdict }) => {
  if (verdict === undefined) {
    return (
      <p className="text-sm text-gray-600">
        Set your{" "}
        <Link to="/settings#touring-limits" className={linkClass}>
          touring limits
        </Link>{" "}
        for a go or no-go verdict on ski tours.
      </p>
    );
  }
  const style = levelStyle(verdict.level);
  return (
    <div className={`rounded-md border px-3 py-2 text-sm ${style.banner}`}>
      <p className="flex items-center gap-2 font-semibold">
        <style.Icon className={`size-5 ${style.icon}`} aria-hidden="true" />
        {style.label}
        <span className="font-normal opacity-80">by your touring limits</span>
      </p>
      <ul className="mt-2 flex flex-col gap-1">
        {verdict.checks.map((check) => {
          const checkStyle = levelStyle(check.level);
          return (
            <li key={check.message} className="flex gap-2">
              <checkStyle.Icon
                className={`mt-0.5 size-4 shrink-0 ${checkStyle.icon}`}
                aria-label={checkStyle.label}
              />
              <span>{check.message}</span>
            </li>
          );
        })}
      </ul>
      <p className="mt-2 text-xs opacity-80">
        Your own rules, from the forecast. Judge the conditions on the day.{" "}
        <Link to="/settings#touring-limits" className="underline">
          Change your limits
        </Link>
      </p>
    </div>
  );
};

export default TouringVerdictView;
