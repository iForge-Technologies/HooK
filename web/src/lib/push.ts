import { api } from "./api";

export interface Device {
  id: number;
  label: string;
  host: string;
  createdAt: number;
  lastOk?: number;
  failures: number;
}

export type PushSupport = "ok" | "insecure" | "unsupported" | "ios-install";

export function pushSupport(): PushSupport {
  if (!window.isSecureContext) return "insecure";
  const ios = /iPhone|iPad|iPod/.test(navigator.userAgent);
  const standalone = matchMedia("(display-mode: standalone)").matches || (navigator as { standalone?: boolean }).standalone === true;
  // iOS only offers Web Push to sites added to the Home Screen.
  if (ios && !standalone) return "ios-install";
  if (!("serviceWorker" in navigator) || !("PushManager" in window) || !("Notification" in window)) return "unsupported";
  return "ok";
}

export function registerServiceWorker() {
  if ("serviceWorker" in navigator && window.isSecureContext) {
    navigator.serviceWorker.register("/sw.js").catch(() => {});
  }
}

function keyBytes(b64: string) {
  const s = atob(b64.replace(/-/g, "+").replace(/_/g, "/") + "=".repeat((4 - (b64.length % 4)) % 4));
  return Uint8Array.from(s, (c) => c.charCodeAt(0));
}

export async function currentSubscription() {
  const reg = await navigator.serviceWorker.getRegistration();
  return (await reg?.pushManager.getSubscription()) ?? null;
}

export function deviceLabel() {
  const ua = navigator.userAgent;
  const os = /Android/.test(ua) ? "Android" : /iPhone|iPad/.test(ua) ? "iOS" : /Mac/.test(ua) ? "macOS" : /Windows/.test(ua) ? "Windows" : /Linux/.test(ua) ? "Linux" : "Device";
  const browser = /Edg\//.test(ua) ? "Edge" : /Firefox\//.test(ua) ? "Firefox" : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari" : "browser";
  return `${browser} on ${os}`;
}

/** Asks permission, subscribes this browser and registers it with the hub. */
export async function enablePush() {
  if ((await Notification.requestPermission()) !== "granted") {
    throw new Error("Notifications are blocked for this site. Allow them in the browser's site settings, then try again.");
  }
  const reg = await navigator.serviceWorker.register("/sw.js");
  await navigator.serviceWorker.ready;
  const { publicKey } = await api.get<{ publicKey: string }>("/webpush/key");
  let sub = await reg.pushManager.getSubscription();
  // A subscription made with another key (a restored hub) can't receive our pushes.
  const existing = sub?.options.applicationServerKey;
  if (sub && existing && btoa(String.fromCharCode(...new Uint8Array(existing))).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "") !== publicKey) {
    await sub.unsubscribe();
    sub = null;
  }
  sub ??= await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(publicKey) });
  const { id } = await api.post<{ id: number }>("/webpush/devices", { ...sub.toJSON(), label: deviceLabel() });
  try {
    localStorage.setItem(DEVICE_KEY, String(id));
  } catch {
    /* only used to mark "this device" in the list */
  }
  return sub;
}

const DEVICE_KEY = "hook.pushDevice";

export function thisDeviceId() {
  try {
    return Number(localStorage.getItem(DEVICE_KEY)) || 0;
  } catch {
    return 0;
  }
}

export async function disablePush() {
  const sub = await currentSubscription();
  if (!sub) return;
  await api.post("/webpush/forget", { endpoint: sub.endpoint });
  await sub.unsubscribe();
}
