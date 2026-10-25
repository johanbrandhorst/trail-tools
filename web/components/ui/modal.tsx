import {
  Dialog,
  DialogBackdrop,
  DialogPanel,
  DialogTitle,
} from "@headlessui/react";
import { XMarkIcon } from "@heroicons/react/24/outline";
import type { ReactNode } from "react";
import React, { useState } from "react";

import { dangerButton, iconButton, secondaryButton } from "./styles";

interface ModalProps {
  open: boolean;
  onClose: () => void;
  title: string;
  children: ReactNode;
  // Wide modals are used for larger forms.
  wide?: boolean;
  /**
   * Whether the modal has unsaved edits. Escape, a click on the backdrop and
   * the close button then ask before discarding them, since they're easy to
   * hit by accident. The modal's own buttons, like Cancel, still close it
   * right away.
   */
  dirty?: boolean | undefined;
}

const Modal: React.FC<ModalProps> = ({
  open,
  onClose,
  title,
  children,
  wide = false,
  dirty = false,
}) => {
  const [confirming, setConfirming] = useState(false);
  const dismiss = () => {
    if (dirty) {
      setConfirming(true);
    } else {
      onClose();
    }
  };
  return (
    <Dialog open={open} onClose={dismiss} className="relative z-[2000]">
      <DialogBackdrop
        transition
        className="fixed inset-0 bg-gray-900/50 transition-opacity data-[closed]:opacity-0 data-[enter]:duration-200 data-[leave]:duration-150"
      />
      <div className="fixed inset-0 w-screen overflow-y-auto">
        <div className="flex min-h-full items-start justify-center p-2 sm:items-center sm:p-4">
          <DialogPanel
            transition
            className={`relative w-full ${wide ? "max-w-3xl" : "max-w-lg"} rounded-lg bg-white shadow-xl transition-all data-[closed]:translate-y-4 data-[closed]:opacity-0 data-[enter]:duration-200 data-[leave]:duration-150`}
          >
            <div className="flex items-center justify-between border-b border-gray-200 px-4 py-3">
              <DialogTitle as="h3" className="text-base font-semibold">
                {title}
              </DialogTitle>
              <button
                type="button"
                className={iconButton}
                onClick={dismiss}
                aria-label="Close"
              >
                <XMarkIcon className="size-5" />
              </button>
            </div>
            <div className="px-4 py-4">{children}</div>
            {/*
             * The confirmation is nested in the panel, so that Headless UI
             * treats it as a child dialog: Escape and clicks on it then
             * don't dismiss this one.
             */}
            <DiscardDialog
              open={open && confirming}
              title="Discard changes?"
              message="Your changes haven't been saved."
              onKeepEditing={() => {
                setConfirming(false);
              }}
              onDiscard={() => {
                setConfirming(false);
                onClose();
              }}
            />
          </DialogPanel>
        </div>
      </div>
    </Dialog>
  );
};

interface DiscardDialogProps {
  open: boolean;
  title: string;
  message: ReactNode;
  // Called to close the dialog and keep the unsaved changes.
  onKeepEditing: () => void;
  // Called to throw the unsaved changes away.
  onDiscard: () => void;
}

/**
 * DiscardDialog asks whether to throw unsaved changes away. Dismissing it
 * keeps them, since that's the choice that loses nothing.
 */
export const DiscardDialog: React.FC<DiscardDialogProps> = ({
  open,
  title,
  message,
  onKeepEditing,
  onDiscard,
}) => (
  <Modal open={open} onClose={onKeepEditing} title={title}>
    <div className="flex flex-col gap-4">
      <div className="text-sm text-gray-700">{message}</div>
      <div className="flex justify-end gap-2">
        <button
          type="button"
          className={secondaryButton}
          onClick={onKeepEditing}
        >
          Keep editing
        </button>
        <button type="button" className={dangerButton} onClick={onDiscard}>
          Discard changes
        </button>
      </div>
    </div>
  </Modal>
);

export default Modal;
