// The service worker of trailrunn.ing, which lets the app be installed and
// open without a connection. It's written by hand, not built, as it must be
// served from the root of the site to control all its pages.
//
// The bundle's files are requested with the hash of their contents in the
// URL (index.js?v=<hash>), so they're cached for good, keeping only the
// latest of each. Pages are fetched from the network, falling back to the
// last page seen, which loads the app; the app's API requests always go to
// the network, so offline it shows what it has in memory, and the strength
// workout session keeps its log on the phone until it can be saved.

const CACHE = "trailrunn.ing";
// APP_SHELL is the key the last page is cached under, for any page.
const APP_SHELL = "/__app";

self.addEventListener("install", () => {
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(self.clients.claim());
});

// isStatic reports whether a request is for a file of the bundle.
const isStatic = (url) =>
  url.pathname === "/index.js" ||
  url.pathname === "/index.css" ||
  url.pathname === "/favicon.svg" ||
  url.pathname.startsWith("/img/") ||
  url.pathname.startsWith("/fonts/");

// cacheFirst answers from the cache, and caches what the network returns,
// dropping older versions of the same file.
const cacheFirst = async (request) => {
  const cache = await caches.open(CACHE);
  const cached = await cache.match(request);
  if (cached !== undefined) {
    return cached;
  }
  const response = await fetch(request);
  if (response.ok) {
    const url = new URL(request.url);
    for (const old of await cache.keys()) {
      if (new URL(old.url).pathname === url.pathname) {
        await cache.delete(old);
      }
    }
    await cache.put(request, response.clone());
  }
  return response;
};

// networkFirst fetches a page, keeping it to open the app offline.
const networkFirst = async (request) => {
  const cache = await caches.open(CACHE);
  try {
    const response = await fetch(request);
    // Only the app's pages, not downloads like GPX files.
    if (response.ok && (response.headers.get("Content-Type") ?? "").startsWith("text/html")) {
      await cache.put(APP_SHELL, response.clone());
    }
    return response;
  } catch (error) {
    const cached = await cache.match(APP_SHELL);
    if (cached !== undefined) {
      return cached;
    }
    throw error;
  }
};

self.addEventListener("fetch", (event) => {
  const { request } = event;
  const url = new URL(request.url);
  if (request.method !== "GET" || url.origin !== self.location.origin) {
    return;
  }
  if (request.mode === "navigate") {
    event.respondWith(networkFirst(request));
    return;
  }
  if (isStatic(url)) {
    event.respondWith(cacheFirst(request));
  }
});
