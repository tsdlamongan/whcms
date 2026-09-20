import { env } from '$env/dynamic/public';

/**
 * Product brand name shown in page titles and UI chrome.
 * Configurable via PUBLIC_APP_NAME (see frontend/.env.example); falls back to "WHCMS".
 */
export const appName = env.PUBLIC_APP_NAME || 'WHCMS';
