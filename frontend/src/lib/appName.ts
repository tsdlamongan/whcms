import { env } from '$env/dynamic/public';

/**
 * Product brand name shown in page titles and UI chrome.
 * Configurable via PUBLIC_APP_NAME (see .env.example at the repo root); falls back to "WHCMS".
 */
export const appName = env.PUBLIC_APP_NAME || 'WHCMS';
