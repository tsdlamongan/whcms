<script lang="ts">
	/**
	 * Cloudflare Turnstile widget (Twenty-One `.ca` theme). Renders the real
	 * Cloudflare widget explicitly on mount, keeps the verification token in a
	 * hidden input (`name`, default `captcha_token`) so it posts with the
	 * enclosing form, and re-arms after a failed submit (tokens are single-use).
	 * Only render this when captcha is enabled.
	 *
	 * `appearance: 'interaction-only'` + `size: 'flexible'`: stays invisible
	 * (0 height, full container width) when Cloudflare can verify silently,
	 * only expanding to show its challenge UI when one is actually needed.
	 * Without this the "Berhasil!" success badge always renders full-size,
	 * dwarfing the compact login/register/checkout panels it sits in.
	 *
	 * Automation fast-path: when running under a WebDriver-controlled browser
	 * (Playwright/Selenium set `navigator.webdriver`) AND the configured sitekey
	 * is one of Cloudflare's documented TEST sitekeys (only ever used in dev/CI,
	 * never in production), the widget skips the CDN and fills a dummy token
	 * synchronously so E2E is deterministic, offline, and never blocks
	 * `networkidle`. Real humans - and any production sitekey - always get the
	 * real Cloudflare widget, so there is no bypass in production.
	 */
	import { onMount, untrack } from 'svelte';
	import { t } from '$lib/i18n';

	interface Props {
		siteKey: string;
		name?: string;
		theme?: 'auto' | 'light' | 'dark';
		/** Bump this (e.g. after a failed submit) to re-arm the single-use widget. */
		resetSignal?: number;
	}

	let { siteKey, name = 'captcha_token', theme = 'light', resetSignal = 0 }: Props = $props();

	// Cloudflare's documented testing sitekeys (dev/CI only). See
	// https://developers.cloudflare.com/turnstile/troubleshooting/testing/
	const TEST_SITEKEYS = new Set([
		'1x00000000000000000000AA', // always passes, visible
		'1x00000000000000000000BB', // always passes, invisible
		'2x00000000000000000000AB', // always blocks, visible
		'2x00000000000000000000BB', // always blocks, invisible
		'3x00000000000000000000FF' // forces an interactive challenge
	]);
	// siteKey is a static prop for a given widget instance, so capture it once
	// (untrack matches the repo idiom for reading props at init).
	const isTestKey = untrack(() => TEST_SITEKEYS.has(siteKey));
	// Cloudflare's own dummy token shape; the backend TEST secret ignores its value.
	const TEST_TOKEN = 'XXXX.DUMMY.TOKEN.XXXX';

	interface TurnstileAPI {
		render: (el: HTMLElement, opts: Record<string, unknown>) => string;
		reset: (id?: string) => void;
		remove: (id?: string) => void;
	}

	const SCRIPT_SRC = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit';

	let token = $state('');
	let container: HTMLDivElement | undefined = $state();
	let widgetId: string | undefined;
	// Set on mount when running headless automation against a test key.
	let autoPass = false;

	function api(): TurnstileAPI | undefined {
		return (globalThis as unknown as { turnstile?: TurnstileAPI }).turnstile;
	}

	function isAutomation(): boolean {
		return typeof navigator !== 'undefined' && navigator.webdriver === true;
	}

	/** Clear the token and re-arm the widget (single-use tokens). */
	function doReset(): void {
		if (autoPass) {
			token = TEST_TOKEN;
			return;
		}
		token = '';
		const ts = api();
		if (ts && widgetId !== undefined) ts.reset(widgetId);
	}

	// Re-arm whenever the parent bumps resetSignal (skips the initial 0).
	let lastSignal = 0;
	$effect(() => {
		if (resetSignal !== lastSignal) {
			lastSignal = resetSignal;
			if (resetSignal > 0) doReset();
		}
	});

	function loadScript(): Promise<void> {
		return new Promise((resolve, reject) => {
			if (api()) return resolve();
			const existing = document.querySelector<HTMLScriptElement>('script[data-turnstile]');
			if (existing) {
				existing.addEventListener('load', () => resolve(), { once: true });
				existing.addEventListener('error', () => reject(new Error('turnstile load failed')), {
					once: true
				});
				if (api()) resolve();
				return;
			}
			const s = document.createElement('script');
			s.src = SCRIPT_SRC;
			s.async = true;
			s.defer = true;
			s.dataset.turnstile = 'true';
			s.addEventListener('load', () => resolve(), { once: true });
			s.addEventListener('error', () => reject(new Error('turnstile load failed')), { once: true });
			document.head.appendChild(s);
		});
	}

	onMount(() => {
		// Headless automation against a test key: fill a dummy token, skip the CDN.
		if (isTestKey && isAutomation()) {
			autoPass = true;
			token = TEST_TOKEN;
			return;
		}

		// Everyone else (humans, and any production sitekey): the real widget.
		let cancelled = false;
		loadScript()
			.then(() => {
				const ts = api();
				if (cancelled || !ts || !container) return;
				widgetId = ts.render(container, {
					sitekey: siteKey,
					theme,
					size: 'flexible',
					appearance: 'interaction-only',
					callback: (tok: string) => {
						token = tok;
					},
					'expired-callback': () => {
						token = '';
						if (widgetId !== undefined) ts.reset(widgetId);
					},
					'error-callback': () => {
						token = '';
					}
				});
			})
			.catch(() => {
				/* CDN/network failure: token stays empty; the server rejects with a
				   retryable VALIDATION error rather than silently allowing the action. */
			});

		return () => {
			cancelled = true;
			const ts = api();
			if (ts && widgetId !== undefined) ts.remove(widgetId);
		};
	});
</script>

<div class="ca-turnstile" data-testid="captcha">
	<div bind:this={container}></div>
	<input type="hidden" {name} value={token} data-testid="captcha-token" />
	<noscript>
		<p class="ca-turnstile-note">{t('portal.captcha.jsRequired')}</p>
	</noscript>
</div>

<style>
	.ca-turnstile {
		margin: 0 0 16px;
	}
	.ca-turnstile-note {
		font-size: 0.85rem;
		color: #c43c35;
		margin: 4px 0 0;
	}
</style>
