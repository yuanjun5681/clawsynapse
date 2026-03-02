import path from 'path';

export const ASSISTANT_NAME = process.env.ASSISTANT_NAME || 'Andy';
export const HTTP_PORT = parseInt(process.env.PORT || '3100', 10);
export const HTTP_HOST = process.env.HTTP_HOST || '127.0.0.1';
export const API_AUTH_TOKEN = process.env.NANOCLAW_API_TOKEN;
export const MAX_REQUEST_BODY_BYTES = parseInt(
  process.env.MAX_REQUEST_BODY_BYTES || '1048576',
  10,
);
export const SCHEDULER_POLL_INTERVAL = 60000;

// In packaged builds, Rust sets these env vars to split bundled resources from mutable data.
// In dev mode both are unset → process.cwd() for both → identical to previous behavior.
const BUNDLE_DIR = process.env.NANOCLAW_BUNDLE_DIR || process.cwd();
const USER_DATA_DIR = process.env.NANOCLAW_DATA_DIR || process.cwd();

const HOME_DIR = process.env.HOME || '/Users/user';

// Bundled resources (read-only in packaged app)
export const BUNDLE_ROOT = BUNDLE_DIR;

// Mount security: allowlist stored OUTSIDE project root, never mounted into containers
export const MOUNT_ALLOWLIST_PATH = path.join(
  HOME_DIR,
  '.config',
  'nanoclaw',
  'mount-allowlist.json',
);

// Mutable user data
export const STORE_DIR = path.resolve(USER_DATA_DIR, 'store');
export const GROUPS_DIR = path.resolve(USER_DATA_DIR, 'groups');
export const DATA_DIR = path.resolve(USER_DATA_DIR, 'data');
export const MAIN_GROUP_FOLDER = 'main';

export const CONTAINER_IMAGE =
  process.env.CONTAINER_IMAGE || 'nanoclaw-agent-agno:latest';
export const CONTAINER_TIMEOUT = parseInt(
  process.env.CONTAINER_TIMEOUT || '1800000',
  10,
);
export const CONTAINER_MAX_OUTPUT_SIZE = parseInt(
  process.env.CONTAINER_MAX_OUTPUT_SIZE || '10485760',
  10,
); // 10MB default
export const IPC_POLL_INTERVAL = 1000;
export const IDLE_TIMEOUT = parseInt(process.env.IDLE_TIMEOUT || '1800000', 10); // 30min default — how long to keep container alive after last result
export const MAX_CONCURRENT_CONTAINERS = Math.max(
  1,
  parseInt(process.env.MAX_CONCURRENT_CONTAINERS || '5', 10) || 5,
);
export const TASK_COMPLETION_WEBHOOK_URL = (
  process.env.TASK_COMPLETION_WEBHOOK_URL || ''
).trim();

// Timezone for scheduled tasks (cron expressions, etc.)
// Uses system timezone by default
export const TIMEZONE =
  process.env.TZ || Intl.DateTimeFormat().resolvedOptions().timeZone;

/**
 * Parse a naive (no timezone suffix) ISO timestamp as if it were in TIMEZONE,
 * returning a proper UTC Date. This avoids the bug where `new Date(naiveISO)`
 * uses the host system timezone, which may differ from the configured TIMEZONE.
 */
export function parseLocalTimestamp(naiveISO: string): Date | null {
  const m = naiveISO.match(/(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):?(\d{2})?/);
  if (!m) return null;
  const [, y, mo, d, h, min, sec] = m;
  // Treat the components as UTC to get a reference point
  const asUTC = Date.UTC(+y, +mo - 1, +d, +h, +min, +(sec || 0));
  // Find what local time asUTC maps to in TIMEZONE
  const inTZ = new Date(asUTC).toLocaleString('sv-SE', { timeZone: TIMEZONE });
  const asUTC2 = Date.parse(inTZ.replace(' ', 'T') + 'Z');
  // offset = how far ahead TIMEZONE is from UTC
  const offset = asUTC2 - asUTC;
  // The user meant naiveISO in TIMEZONE, so: UTC = components_as_utc - offset
  return new Date(asUTC - offset);
}
