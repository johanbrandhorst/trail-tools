import React, { useCallback, useEffect } from "react";
import type { BlockerFunction, NavigateOptions } from "react-router";
import { useBlocker } from "react-router";

import { DiscardDialog } from "./modal";

/**
 * LeaveUnsaved is the navigation option that lets a navigation past an
 * UnsavedChangesGuard without asking, e.g. to the list after deleting what was
 * being edited: navigate("/trip-reports", leaveUnsaved).
 */
export const leaveUnsaved: NavigateOptions = {
  state: { leaveUnsaved: true },
};

// IsLeavingUnsaved reports whether a location's state is leaveUnsaved's.
const isLeavingUnsaved = (state: unknown): boolean =>
  typeof state === "object" &&
  state !== null &&
  "leaveUnsaved" in state &&
  state.leaveUnsaved === true;

interface UnsavedChangesGuardProps {
  // Whether there are unsaved changes. Saving must clear it.
  dirty: boolean;
  /*
   * The dialog's text and buttons, for work that leaving loses other than
   * unsaved changes, like an import in progress.
   */
  title?: string | undefined;
  message?: string | undefined;
  keepLabel?: string | undefined;
  leaveLabel?: string | undefined;
}

/**
 * UnsavedChangesGuard asks before leaving a page with unsaved changes: with a
 * dialog when following a link in the app or going back, and with the
 * browser's own prompt when closing or reloading the tab. Navigations that
 * stay on the page, like changing its search parameters, go through.
 */
const UnsavedChangesGuard: React.FC<UnsavedChangesGuardProps> = ({
  dirty,
  title = "Leave without saving?",
  message = "Your changes haven't been saved, and are lost if you leave this page.",
  keepLabel,
  leaveLabel,
}) => {
  const shouldBlock = useCallback<BlockerFunction>(
    ({ currentLocation, nextLocation }) =>
      dirty &&
      nextLocation.pathname !== currentLocation.pathname &&
      !isLeavingUnsaved(nextLocation.state),
    [dirty]
  );
  const blocker = useBlocker(shouldBlock);

  useEffect(() => {
    if (!dirty) {
      return undefined;
    }
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
    };
    globalThis.addEventListener("beforeunload", onBeforeUnload);
    return () => {
      globalThis.removeEventListener("beforeunload", onBeforeUnload);
    };
  }, [dirty]);

  if (blocker.state !== "blocked") {
    return null;
  }
  return (
    <DiscardDialog
      open
      title={title}
      message={message}
      keepLabel={keepLabel}
      discardLabel={leaveLabel}
      onKeepEditing={() => {
        blocker.reset();
      }}
      onDiscard={() => {
        blocker.proceed();
      }}
    />
  );
};

export default UnsavedChangesGuard;
