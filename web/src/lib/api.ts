export type Status = "up" | "down" | "pending" | "degraded" | "paused" | "maintenance";
export type MonitorType = "http" | "keyword" | "tcp" | "dns" | "tls" | "postgres" | "mysql" | "redis" | "mongodb" | "push" | "vercel" | "render";
export const DB_TYPES: MonitorType[] = ["postgres", "mysql", "redis", "mongodb"];

export interface Timings {
  dns: number;
  connect: number;
  tls: number;
  ttfb: number;
  transfer: number;
  total: number;
}

export interface Beat {
  monitorId: number;
  ts: number;
  status: Status;
  latency: number;
  code?: number;
  msg?: string;
  timings?: Timings;
}

export interface MonitorConfig {
  method?: string;
  headers?: Record<string, string>;
  body?: string;
  expectedStatus?: string;
  followRedirects: boolean;
  ignoreTls: boolean;
  keyword?: string;
  keywordInvert: boolean;
  certAlerts: boolean;
  noConfirm?: boolean;
  dnsRecord?: string;
  dnsResolver?: string;
  dnsExpected?: string;
  dsn?: string;
  pushToken?: string;
  integrationId?: number;
  projectId?: string;
  projectName?: string;
}

export interface Monitor {
  id: number;
  name: string;
  type: MonitorType;
  target: string;
  interval: number;
  retryInterval: number;
  timeout: number;
  retries: number;
  degradedMs: number;
  group: string;
  active: boolean;
  config: MonitorConfig;
  channelIds: number[];
  createdAt: number;
  status: Status;
  statusSince: number;
  lastCheck: number;
  lastLatency: number;
  lastMsg: string;
  certExpires: number;
  certIssuer: string;
}

export interface MonitorListItem extends Monitor {
  beats: Beat[];
  uptime24h: number | null;
  avgLatency24h: number | null;
}

export interface Incident {
  id: number;
  monitorId: number;
  startedAt: number;
  resolvedAt: number | null;
  cause: string;
}

export interface MonitorDetail {
  monitor: Monitor;
  uptime: Record<"24h" | "7d" | "30d" | "90d", number | null>;
  avgLatency24h: number | null;
  beats: Beat[];
  incidents: Incident[];
}

export interface SeriesPoint {
  ts: number;
  latency: number;
  max?: number;
  status: Status;
  msg?: string;
  up?: number;
  down?: number;
}

export interface Series {
  range: string;
  resolution: "raw" | "1h";
  points: SeriesPoint[];
}

export interface Channel {
  id: number;
  name: string;
  type: string;
  config: Record<string, string>;
  isDefault: boolean;
  active: boolean;
}

export interface Meta {
  version: string;
  publicUrl: string;
  groups: string[];
  checkTypes: MonitorType[];
  dnsRecords: string[];
  channelTypes: string[];
  locations: string[] | null;
}

export interface AuthStatus {
  setupRequired: boolean;
  authenticated: boolean;
  mfaPending: boolean;
  version: string;
  user?: { id: number; username: string; totpEnabled: boolean };
}

/** Sent back for secret fields we never reveal; the server keeps the stored value. */
export const MASK = "••••••••";

export class ApiError extends Error {
  constructor(message: string, public status: number) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, credentials: "same-origin" };
  if (method !== "GET") {
    // The hub rejects non-JSON writes (CSRF guard), so always send JSON.
    init.headers = { "Content-Type": "application/json" };
    init.body = JSON.stringify(body ?? {});
  }
  const res = await fetch("/api" + path, init);
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(data.error || res.statusText || "Request failed", res.status);
  return data as T;
}

export const api = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T = { ok: boolean }>(path: string, body?: unknown) => request<T>("POST", path, body),
  put: <T = { ok: boolean }>(path: string, body?: unknown) => request<T>("PUT", path, body),
  del: <T = { ok: boolean }>(path: string) => request<T>("DELETE", path),
};

/* ---------------- servers (agent) ---------------- */

export interface HostInfo {
  agentVersion: string;
  hostname: string;
  os: string;
  platform: string;
  kernel: string;
  arch: string;
  virtualization?: string;
  cpuModel: string;
  cores: number;
  memTotal: number;
  swapTotal: number;
  bootTime: number;
  probes?: boolean;
  noSpeedTest?: boolean;
}

export interface Metrics {
  ts: number;
  cpu: number;
  iowait: number;
  steal: number;
  load1: number;
  load5: number;
  load15: number;
  memUsed: number;
  memTotal: number;
  swapUsed: number;
  swapTotal: number;
  diskRead: number;
  diskWrite: number;
  netRx: number;
  netTx: number;
  tcp: number;
  procs: number;
  diskPct: number;
  gpu: number;
  gpuMem: number;
}

/** Rollup points also carry the peak inside the bucket. */
export interface MetricPoint extends Metrics {
  cpuMax?: number;
  memUsedMax?: number;
  load1Max?: number;
  diskReadMax?: number;
  diskWriteMax?: number;
  netRxMax?: number;
  netTxMax?: number;
  gpuMax?: number;
}

export interface Thresholds {
  cpu: number;
  mem: number;
  disk: number;
  minutes: number;
  offline: boolean;
  containers: boolean;
  services: boolean;
  units: string[];
  trafficQuotaGb: number;
  trafficResetDay: number;
  speedTestHours: number;
}

export type ServerStatus = "new" | "online" | "offline";

export interface Server {
  id: number;
  name: string;
  group: string;
  info: HostInfo;
  thresholds: Thresholds;
  channelIds: number[];
  status: ServerStatus;
  statusSince: number;
  lastSeen: number;
  remoteIp: string;
  createdAt: number;
  metrics: Metrics | null;
  connected: boolean;
  openAlerts: string[];
  cpuSpark?: number[];
  diskDaysToFull?: number;
}

export interface Snapshot {
  ts: number;
  uptime: number;
  cores: number[] | null;
  mounts: { path: string; device: string; fstype: string; total: number; used: number; usedPct: number; inodesPct: number }[] | null;
  ifaces: Iface[] | null;
  topCpu: Proc[] | null;
  topMem: Proc[] | null;
  temps: { sensor: string; c: number }[] | null;
  rebootRequired: boolean;
  docker?: Docker;
  gpu?: GPUs;
  units: Unit[] | null;
  failedUnits: string[] | null;
}

export interface Iface {
  name: string;
  rx: number;
  tx: number;
  rxErr: number;
  txErr: number;
  drops: number;
  /* newer agents */
  rxTotal?: number;
  txTotal?: number;
  up?: boolean;
  speed?: number; // Mbit/s, 0 = unknown
  mtu?: number;
  addrs?: string[];
}

export interface GPUs {
  present: boolean;
  note?: string;
  devices: GPU[];
  procs: { pid: number; name: string; gpu: number; mem: number }[] | null;
}

export interface GPU {
  index: number;
  vendor: "nvidia" | "amd" | "intel";
  name: string;
  util: number; // -1 = not reported
  memUsed: number;
  memTotal: number;
  temp?: number;
  power?: number;
  powerLimit?: number;
  fan?: number;
  clockMhz?: number;
  driver?: string;
}

export interface Docker {
  available: boolean;
  error?: string;
  version?: string;
  containers: Container[] | null;
  images?: DockerImage[] | null;
  volumes?: DockerVolume[] | null;
  usage?: { at: number; images: number; containers: number; volumes: number; buildCache: number; reclaimable: number };
  detailError?: string;
}

export interface Container {
  id: string;
  name: string;
  image: string;
  state: string;
  status: string;
  health?: string;
  restarts: number;
  cpu: number;
  mem: number;
  memLimit: number;
  /* newer agents */
  created?: number;
  startedAt?: number;
  ports?: string[];
  networks?: string[];
  mounts?: { type: string; name: string; target: string; rw: boolean }[];
  netRx?: number;
  netTx?: number;
  blockRead?: number;
  blockWrite?: number;
  pids?: number;
  sizeRw?: number;
}

export interface DockerImage {
  id: string;
  tags: string[];
  size: number;
  created: number;
  containers: number;
  dangling: boolean;
}

export interface DockerVolume {
  name: string;
  driver: string;
  size: number;
  created?: number;
  usedBy: string[];
  anonymous: boolean;
}

export interface SpeedTest {
  id: number;
  ts: number;
  downBps: number;
  upBps: number;
  latencyMs: number;
  jitterMs: number;
  server: string;
  bytes: number;
  error?: string;
  scheduled: boolean;
}

export interface ServerNetwork {
  days: { day: number; rx: number; tx: number }[];
  cycle: { start: number; end: number; rx: number; tx: number; quotaGb: number };
  speedTests: SpeedTest[];
  speedTestHours: number;
  speedTestEnabled: boolean;
}

export interface Unit {
  name: string;
  active: string;
  sub: string;
  restarts: number;
  since: number;
}

/* ---------------- platforms ---------------- */

export interface Integration {
  id: number;
  kind: "vercel" | "render";
  name: string;
  config: { token?: string; teamId?: string };
}

export interface Project {
  id: string;
  name: string;
  kind: string;
  url: string;
  state: "ready" | "building" | "failed" | "canceled" | "none" | "suspended";
  detail: string;
  updatedAt: number;
  freePlan: boolean;
  dashboard: string;
}

export interface Proc {
  pid: number;
  name: string;
  user: string;
  cpu: number;
  rss: number;
}

export interface HostEvent {
  id: number;
  hostId: number;
  kind: "offline" | "cpu" | "mem" | "disk" | "container" | "service" | "forecast";
  subject: string;
  startedAt: number;
  resolvedAt: number | null;
  peak: number;
  detail: string;
}

export interface ServerDetail {
  server: Server;
  snapshot: Snapshot | null;
  events: HostEvent[];
  forecast: { disk: DiskForecast; sizing: Sizing };
}

export interface DiskForecast {
  current: number;
  slopePerDay: number;
  r2: number;
  daysToFull: number | null;
  confident: boolean;
  points: number;
  line: [[number, number], [number, number]];
}

export interface Sizing {
  days: number;
  cpuPeak: number;
  cpuP95: number;
  memPeak: number;
  verdict: "" | "ok" | "oversized" | "tight";
}

export interface Maintenance {
  id: number;
  title: string;
  startsAt: number;
  endsAt: number;
  repeat: "" | "daily" | "weekly";
  everything: boolean;
  monitorIds: number[];
  hostIds: number[];
  active?: boolean;
  nextStart?: number;
  nextEnd?: number;
}

export interface StatusPage {
  id: number;
  slug: string;
  title: string;
  description: string;
  monitorIds: number[];
  showLatency: boolean;
}

export interface PublicStatus {
  title: string;
  description: string;
  overall: "up" | "degraded" | "down" | "maintenance";
  updatedAt: number;
  monitors: { name: string; group: string; status: Status; uptime90: number | null; days: { day: number; uptime: number | null }[]; latency?: number }[];
  incidents: { name: string; startedAt: number; resolvedAt: number | null }[];
  maintenance: { title: string; startsAt: number; endsAt: number; active: boolean }[];
}

export interface ServerSeries {
  range: string;
  resolution: "raw" | "1m" | "1h";
  points: MetricPoint[];
}
