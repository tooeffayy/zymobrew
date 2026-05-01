// Zymo service worker — web push only.
//
// Scope: served from /sw.js, so the worker controls every same-origin
// path. We deliberately don't claim() clients or fetch-intercept anything;
// the SPA handles its own routing and we just want to be alive when the
// browser delivers a push.
//
// Payload contract (set by internal/jobs/reminder_dispatcher.go):
//   { "title": "...", "body": "...", "url_path": "/batches/abc..." }

self.addEventListener("install", () => {
  // Skip waiting so the new worker takes over on the next push without
  // requiring a tab reload. There's no app-data versioning to migrate.
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener("push", (event) => {
  let data = {};
  if (event.data) {
    try {
      data = event.data.json();
    } catch {
      // Some push services may deliver a bare text payload during testing.
      // Treat it as the body and use a generic title.
      data = { title: "Zymo", body: event.data.text() };
    }
  }
  const title = data.title || "Zymo";
  const options = {
    body: data.body || "",
    // tag collapses repeated notifications for the same reminder so a
    // brewer doesn't get a stack of identical pings on a flaky connection.
    // The dispatcher today doesn't include a stable id; fall back to
    // url_path which is at least batch-scoped.
    tag: data.tag || data.url_path || "zymo",
    data: { url_path: data.url_path || "/" },
  };
  event.waitUntil(self.registration.showNotification(title, options));
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const urlPath = (event.notification.data && event.notification.data.url_path) || "/";
  const targetURL = new URL(urlPath, self.location.origin).href;

  event.waitUntil(
    (async () => {
      const wins = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
      // Prefer an existing tab already at the target — focus and we're done.
      for (const w of wins) {
        if (w.url === targetURL) {
          return w.focus();
        }
      }
      // Otherwise focus any same-origin tab and try to navigate it.
      // WindowClient.navigate() is only available in tabs the SW controls;
      // fall through to openWindow if it throws.
      for (const w of wins) {
        if (new URL(w.url).origin === self.location.origin) {
          await w.focus();
          try {
            await w.navigate(urlPath);
            return;
          } catch {
            // Browser can't navigate this client (different scope, cross-origin
            // history, etc.). Open a fresh tab below.
            break;
          }
        }
      }
      await self.clients.openWindow(urlPath);
    })(),
  );
});
