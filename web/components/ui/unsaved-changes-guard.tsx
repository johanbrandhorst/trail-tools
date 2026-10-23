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
}

/**
 * UnsavedChangesGuard asks before leaving a page with unsaved changes: with a
 * dialog when following a link in the app or going back, and with the
 * browser's own prompt when closing or reloading the tab. Navigations that
 * stay on the page, like changing its search parameters, go through.
 */
const UnsavedChangesGuard: React.FC<UnsavedChangesGuardProps> = ({ dirty }) => {
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
      title="Leave without saving?"
      message="Your changes haven't been saved, and are lost if you leave this page."
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
