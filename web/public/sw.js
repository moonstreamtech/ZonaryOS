// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).
//
// PWA foundation batch, Part 2: service worker implementing a strict
// two-strategy split -
//
//   1. CACHE-FIRST for the app shell: the root document and Next.js's
//      own hashed build output under /_next/static/ (immutable -
//      content-addressed filenames, safe to serve from cache forever
//      and only ever refreshed by a new deployment's new hashes), plus
//      this same batch's manifest.json/icon.svg/icon-maskable.svg. This
//      is what keeps the shell viewable offline.
//   2. NETWORK-FIRST for everything else (falling back to a previously
//      cached copy only if the network genuinely fails, e.g. offline) -
//      EXCEPT any /api/* request, which is explicitly never cached at
//      all: stale financial/business data is worse than no data, so an
//      /api/* request always goes straight to the network with no
//      caching read or write, network failures included (no stale
//      fallback for these).
//
// Bumping CACHE_VERSION (on any app-shell-affecting change to this file
// or the precache list) is what drives the old-cache cleanup in
// `activate` below - the standard cache-versioning pattern.
const CACHE_VERSION = "zonaryos-v1";
const SHELL_CACHE = `${CACHE_VERSION}-shell`;
const RUNTIME_CACHE = `${CACHE_VERSION}-runtime`;

// Precached at install time. Next.js's hashed /_next/static/* chunks for
// the *current* build aren't known ahead of time here (their filenames
// are content-hashed per build), so those are added to SHELL_CACHE
// opportunistically the first time each is fetched (see the cache-first
// branch in `fetch` below) rather than enumerated here - this list only
// covers the fixed-path shell assets that exist regardless of build.
const PRECACHE_URLS = ["/", "/manifest.json", "/icon.svg", "/icon-maskable.svg"];

function isApiRequest(url) {
  return url.pathname.startsWith("/api/");
}

// Next.js's own immutable, content-hashed build output - safe to treat
// as cache-first/cache-forever, same reasoning as the fixed shell paths
// above.
function isBuildAsset(url) {
  return url.pathname.startsWith("/_next/static/");
}

function isShellAsset(url) {
  return isBuildAsset(url) || PRECACHE_URLS.includes(url.pathname);
}

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(SHELL_CACHE)
      .then((cache) => cache.addAll(PRECACHE_URLS))
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) =>
        Promise.all(
          keys
            .filter((key) => key !== SHELL_CACHE && key !== RUNTIME_CACHE)
            .map((key) => caches.delete(key)),
        ),
      )
      .then(() => self.clients.claim()),
  );
});

async function cacheFirst(request, cacheName) {
  const cached = await caches.match(request);
  if (cached) return cached;
  const response = await fetch(request);
  if (response && response.ok) {
    const cache = await caches.open(cacheName);
    cache.put(request, response.clone());
  }
  return response;
}

async function networkFirst(request) {
  try {
    const response = await fetch(request);
    if (response && response.ok) {
      const cache = await caches.open(RUNTIME_CACHE);
      cache.put(request, response.clone());
    }
    return response;
  } catch (err) {
    const cached = await caches.match(request);
    if (cached) return cached;
    throw err;
  }
}

self.addEventListener("fetch", (event) => {
  const { request } = event;
  if (request.method !== "GET") return; // never intercept mutations

  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return; // same-origin only

  // Hard requirement: /api/* is NEVER cached, in either direction - no
  // cache read, no cache write, no offline fallback. Always network.
  if (isApiRequest(url)) {
    event.respondWith(fetch(request));
    return;
  }

  if (isShellAsset(url)) {
    event.respondWith(cacheFirst(request, SHELL_CACHE));
    return;
  }

  event.respondWith(networkFirst(request));
});
