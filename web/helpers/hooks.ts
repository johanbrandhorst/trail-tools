import type { RefObject } from "react";
import { useEffect, useState } from "react";
import { useLocation } from "react-router";

// UseElementWidth tracks the rendered width of an element in pixels.
export const useElementWidth = (ref: RefObject<HTMLElement | null>): number => {
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const element = ref.current;
    if (element === null) {
      return undefined;
    }
    setWidth(element.getBoundingClientRect().width);
    const observer = new ResizeObserver((entries) => {
      for (const entry of entries) {
        setWidth(entry.contentRect.width);
      }
    });
    observer.observe(element);
    return () => {
      observer.disconnect();
    };
  }, [ref]);
  return width;
};

/**
 * UseFragmentToken returns the secret token in the fragment of the page's
 * URL, e.g. of an invitation link, /invite#<token>. Links carry their tokens
 * there because browsers don't send fragments to the server, so the tokens
 * stay out of request logs. It's "" if there is none.
 */
export const useFragmentToken = (): string => {
  const fragment = useLocation().hash.slice(1);
  try {
    return decodeURIComponent(fragment);
  } catch {
    return fragment;
  }
};

/**
 * UseWakeLock keeps the screen on while active and the page is shown, so a
 * phone doesn't lock and pause the page, e.g. between sets or during an
 * import. The browser releases the lock when the page is hidden, so it's
 * taken again when the page is shown. Browsers without the Screen Wake Lock
 * API, and low battery modes that refuse it, lock as usual.
 */
export const useWakeLock = (active: boolean): void => {
  useEffect(() => {
    if (!active) {
      return undefined;
    }
    let lock: WakeLockSentinel | undefined = undefined;
    let released = false;
    const acquire = async () => {
      if (
        document.visibilityState !== "visible" ||
        !("wakeLock" in navigator)
      ) {
        return;
      }
      try {
        lock = await navigator.wakeLock.request("screen");
        if (released) {
          await lock.release();
        }
      } catch {
        // The screen locks as usual.
      }
    };
    const onVisibility = () => {
      void acquire();
    };
    void acquire();
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      released = true;
      document.removeEventListener("visibilitychange", onVisibility);
      void lock?.release();
    };
  }, [active]);
};
