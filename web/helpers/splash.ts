/**
 * The time the splash screen takes to fade out, matching its transition in
 * base.css, after which it's removed from the page.
 */
const fadeMilliseconds = 300;

/**
 * HideSplash fades out the splash screen that index.html shows while the app
 * loads, and removes it. The splash is outside the app's root, so React
 * doesn't replace it when it first renders, and its animation carries on
 * without restarting until the app has something to show.
 */
export const hideSplash = (): void => {
  const splash = document.querySelector("#splash");
  if (splash === null || splash.classList.contains("splash-hidden")) {
    return;
  }
  splash.classList.add("splash-hidden");
  setTimeout(() => {
    splash.remove();
  }, fadeMilliseconds);
};
