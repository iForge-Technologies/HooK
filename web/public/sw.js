// Hook service worker: shows Web Push alerts. It caches nothing on purpose:
// a monitoring dashboard showing stale data offline would be worse than none.

self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (e) => e.waitUntil(self.clients.claim()));

self.addEventListener("push", (e) => {
  let n = {};
  try {
    n = e.data ? e.data.json() : {};
  } catch {
    n = { body: e.data ? e.data.text() : "" };
  }
  e.waitUntil(
    self.registration.showNotification(n.title || "Hook", {
      body: n.body || "",
      tag: n.tag,
      renotify: !!n.tag, // a new state for the same monitor still buzzes
      icon: "/icon-192.png",
      data: { url: n.url || "/" },
    }),
  );
});

self.addEventListener("notificationclick", (e) => {
  e.notification.close();
  const url = new URL(e.notification.data?.url || "/", self.location.origin).href;
  e.waitUntil(
    (async () => {
      const wins = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
      const win = wins.find((c) => new URL(c.url).origin === self.location.origin);
      if (win) {
        await win.focus();
        return win.navigate(url).catch(() => {});
      }
      return self.clients.openWindow(url);
    })(),
  );
});

// Browsers occasionally rotate a subscription; re-register it so alerts keep arriving.
self.addEventListener("pushsubscriptionchange", (e) => {
  e.waitUntil(
    (async () => {
      const opts = e.oldSubscription?.options;
      if (!opts) return;
      const sub = e.newSubscription || (await self.registration.pushManager.subscribe(opts));
      const body = { ...sub.toJSON(), label: "Renewed device" };
      await fetch("/api/webpush/devices", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
    })(),
  );
});
