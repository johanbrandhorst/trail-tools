import { TextAreaField, TextField } from "@components/ui/fields";
import Modal from "@components/ui/modal";
import { ErrorText } from "@components/ui/query-error";
import { primaryButton, secondaryButton } from "@components/ui/styles";
import { useMutation } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import {
  createRaceEvent,
  updateRaceEvent,
} from "gen/races/v1/races-RaceService_connectquery";
import type { RaceEvent } from "gen/races/v1/races_pb";
import { RaceService } from "gen/races/v1/races_pb";
import { invalidateServices } from "helpers/query";
import React, { useState } from "react";

interface EventDialogProps {
  // The event to edit, or undefined to create an event.
  event: RaceEvent | undefined;
  onClose: () => void;
}

// EventDialog creates or edits a race event.
const EventDialog: React.FC<EventDialogProps> = ({ event, onClose }) => {
  const queryClient = useQueryClient();
  const [name, setName] = useState(event?.name ?? "");
  const [location, setLocation] = useState(event?.location ?? "");
  const [website, setWebsite] = useState(event?.websiteUrl ?? "");
  const [notes, setNotes] = useState(event?.notes ?? "");
  const onSuccess = async () => {
    await invalidateServices(queryClient, [RaceService]);
    onClose();
  };
  const create = useMutation(createRaceEvent, { onSuccess });
  const update = useMutation(updateRaceEvent, { onSuccess });
  const dirty =
    name !== (event?.name ?? "") ||
    location !== (event?.location ?? "") ||
    website !== (event?.websiteUrl ?? "") ||
    notes !== (event?.notes ?? "");

  const submit = () => {
    const fields = {
      location: location.trim(),
      name: name.trim(),
      notes,
      websiteUrl: website.trim(),
    };
    if (event === undefined) {
      create.mutate({ event: fields });
    } else {
      update.mutate({
        event: { ...fields, id: event.id },
        updateMask: { paths: ["name", "location", "website_url", "notes"] },
      });
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={event === undefined ? "Add event" : "Edit event"}
      dirty={dirty}
    >
      <form
        className="flex flex-col gap-3"
        onSubmit={(formEvent) => {
          formEvent.preventDefault();
          submit();
        }}
      >
        <p className="text-sm text-gray-600">
          An event groups races that belong together, like a 50 mile race on the
          Saturday and a 50K on the Sunday.
        </p>
        <TextField
          label="Name"
          value={name}
          onChange={setName}
          placeholder="e.g. Squamish 50/50"
          autoFocus
          required
        />
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
        <TextAreaField
          label="Notes"
          value={notes}
          onChange={setNotes}
          placeholder="Travel, accommodation, combined results"
        />
        <ErrorText error={create.error ?? update.error} />
        <div className="flex justify-end gap-2 pt-2">
          <button type="button" className={secondaryButton} onClick={onClose}>
            Cancel
          </button>
          <button
            type="submit"
            className={primaryButton}
            disabled={
              name.trim() === "" || create.isPending || update.isPending
            }
          >
            Save
          </button>
        </div>
      </form>
    </Modal>
  );
};

export default EventDialog;
