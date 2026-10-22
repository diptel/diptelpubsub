/**
 * Service worker minimal untuk menerima push notification dari PushHub.
 * Simpan sebagai /sw.js di root tiap app (riwariwi, briixbroadband, dll),
 * lalu daftarkan di awal aplikasi dengan:
 *   navigator.serviceWorker.register('/sw.js')
 */

self.addEventListener("push", (event) => {
  if (!event.data) return;

  const data = event.data.json(); // { title, body, icon, url, data }

  const options = {
    body: data.body || "",
    icon: data.icon || "/icons/icon-192.png",
    badge: "/icons/badge-72.png",
    data: { url: data.url || "/" },
  };

  event.waitUntil(self.registration.showNotification(data.title || "Notifikasi", options));
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const url = event.notification.data?.url || "/";

  event.waitUntil(
    clients.matchAll({ type: "window", includeUncontrolled: true }).then((clientList) => {
      for (const client of clientList) {
        if (client.url === url && "focus" in client) return client.focus();
      }
      if (clients.openWindow) return clients.openWindow(url);
    })
  );
});
