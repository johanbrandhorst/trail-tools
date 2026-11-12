import Modal from "@components/ui/modal";
import {
  dangerButton,
  secondaryButton,
  secondaryDangerButton,
} from "@components/ui/styles";
import React, { useState } from "react";

interface ChoiceProps {
  checked: boolean;
  onSelect: () => void;
  label: string;
  hint: string;
}

// Choice is one of the options of what happens to the activities.
const Choice: React.FC<ChoiceProps> = ({ checked, onSelect, label, hint }) => (
  <label
    className={`flex cursor-pointer gap-3 rounded-md p-3 ring-1 ${checked ? "bg-orange-50 ring-orange-300" : "ring-gray-200"}`}
  >
    <input
      type="radio"
      name="strava-activities"
      checked={checked}
      className="mt-0.5 size-4 accent-orange-600"
      onChange={onSelect}
    />
    <span className="flex flex-col gap-0.5">
      <span className="text-sm font-medium text-gray-900">{label}</span>
      <span className="text-xs text-gray-600">{hint}</span>
    </span>
  </label>
);

interface DisconnectStravaProps {
  // Whether the user agreed to keep their activities, the default choice.
  keepActivities: boolean;
  disabled: boolean;
  onDisconnect: (keepActivities: boolean) => void;
}

/**
 * DisconnectStrava disconnects Strava, asking whether to keep the
 * activities that came from it: Strava's terms only allow keeping them with
 * the user's agreement.
 */
const DisconnectStrava: React.FC<DisconnectStravaProps> = ({
  keepActivities,
  disabled,
  onDisconnect,
}) => {
  const [open, setOpen] = useState(false);
  const [keep, setKeep] = useState(keepActivities);
  const close = () => {
    setOpen(false);
  };
  return (
    <>
      <button
        type="button"
        className={secondaryDangerButton}
        disabled={disabled}
        onClick={() => {
          setKeep(keepActivities);
          setOpen(true);
        }}
      >
        Disconnect
      </button>
      <Modal open={open} onClose={close} title="Disconnect Strava?">
        <div className="flex flex-col gap-4">
          <p className="text-sm text-gray-700">
            New activities stop syncing from Strava, and trailrunn.ing loses
            access to your Strava account. Your activities from Strava can only
            stay here if you agree to it.
          </p>
          <div className="flex flex-col gap-2">
            <Choice
              checked={keep}
              onSelect={() => {
                setKeep(true);
              }}
              label="Keep my activities on trailrunn.ing"
              hint="They stay as they are now, without updates from Strava."
            />
            <Choice
              checked={!keep}
              onSelect={() => {
                setKeep(false);
              }}
              label="Delete my activities from Strava"
              hint="Activities only on Strava are deleted. Those also on Garmin Connect keep Garmin's data, and those you imported from your Strava archive stay."
            />
          </div>
          <div className="flex justify-end gap-2">
            <button type="button" className={secondaryButton} onClick={close}>
              Cancel
            </button>
            <button
              type="button"
              className={dangerButton}
              onClick={() => {
                setOpen(false);
                onDisconnect(keep);
              }}
            >
              Disconnect
            </button>
          </div>
        </div>
      </Modal>
    </>
  );
};

export default DisconnectStrava;
