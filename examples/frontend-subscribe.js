/**
 * Contoh integrasi frontend (PWA) untuk subscribe ke push notification lewat PushHub.
 * Pakai ini di riwariwi, briixbroadband, atau briix-capital/mifos mobile PWA —
 * cukup ganti PUSHHUB_URL dan APP_SLUG sesuai project.
 *
 * Syarat: service worker sudah terdaftar (lihat examples/service-worker.js).
 */

const PUSHHUB_URL = "https://push.briix.co.id"; // ganti dengan URL deploy PushHub kamu
const APP_SLUG = "riwariwi"; // "briixbroadband" | "riwariwi" | "briix-capital" | "kasir"

function urlBase64ToUint8Array(base64String) {
  const padding = "=".repeat((4 - (base64String.length % 4)) % 4);
  const base64 = (base64String + padding).replace(/-/g, "+").replace(/_/g, "/");
  const rawData = atob(base64);
  return Uint8Array.from([...rawData].map((c) => c.charCodeAt(0)));
}

/**
 * Panggil fungsi ini setelah user login / setelah user mengizinkan notifikasi,
 * misalnya lewat tombol "Aktifkan Notifikasi".
 * @param {string} [userId] - id user di sistem kamu sendiri, dipakai untuk kirim notif tertarget.
 */
export async function enablePushNotifications(userId) {
  if (!("serviceWorker" in navigator) || !("PushManager" in window)) {
    throw new Error("Browser tidak mendukung push notification");
  }

  const permission = await Notification.requestPermission();
  if (permission !== "granted") {
    throw new Error("Izin notifikasi ditolak user");
  }

  const registration = await navigator.serviceWorker.ready;

  // Ambil VAPID public key milik app ini dari PushHub.
  const keyRes = await fetch(`${PUSHHUB_URL}/vapid-public-key?app=${APP_SLUG}`);
  if (!keyRes.ok) throw new Error("Gagal mengambil VAPID public key");
  const { vapid_public_key } = await keyRes.json();

  const subscription = await registration.pushManager.subscribe({
    userVisibleOnly: true,
    applicationServerKey: urlBase64ToUint8Array(vapid_public_key),
  });

  // Kirim subscription ke PushHub, terkait dengan app + (opsional) user_id.
  const subRes = await fetch(`${PUSHHUB_URL}/subscribe`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      app: APP_SLUG,
      user_id: userId ?? null,
      subscription: subscription.toJSON(),
    }),
  });
  if (!subRes.ok) throw new Error("Gagal menyimpan subscription ke PushHub");

  return subscription;
}

/** Panggil ini saat user mematikan notifikasi / logout. */
export async function disablePushNotifications() {
  const registration = await navigator.serviceWorker.ready;
  const subscription = await registration.pushManager.getSubscription();
  if (!subscription) return;

  await fetch(`${PUSHHUB_URL}/unsubscribe`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ app: APP_SLUG, endpoint: subscription.endpoint }),
  });

  await subscription.unsubscribe();
}
