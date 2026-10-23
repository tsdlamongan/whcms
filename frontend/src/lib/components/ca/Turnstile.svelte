<script lang="ts" module>
	/**
	 * Cloudflare's api.js is loaded once per page and shared by every widget.
	 * A failed or stalled load is cleared (tag removed, promise dropped) so the
	 * next attempt starts clean - a stale tag would make every later mount wait
	 * on `load`/`error` events that never fire again, and only a full page
	 * refresh would recover.
	 */
	interface TurnstileAPI {
		render: (el: HTMLElement, opts: Record<string, unknown>) => string | undefined;
		reset: (id?: string) => void;
		remove: (id?: string) => void;
		ready?: (cb: () => void) => void;
	}

	const SCRIPT_SRC = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit';
	const SCRIPT_TIMEOUT_MS = 15_000;

	let scriptLoad: Promise<void> | null = null;

	function api(): TurnstileAPI | undefined {
		return (globalThis as unknown as { turnstile?: TurnstileAPI }).turnstile;
	}

	function loadScript(): Promise<void> {
		if (api()) return Promise.resolve();
		if (scriptLoad) return scriptLoad;
		scriptLoad = new Promise<void>((resolve, reject) => {
			document.querySelector('script[data-turnstile]')?.remove();
			const s = document.createElement('script');
			s.src = SCRIPT_SRC;
			s.async = true;
			s.defer = true;
			s.dataset.turnstile = 'true';
			const fail = (message: string) => {
				clearTimeout(timer);
				s.remove();
				scriptLoad = null;
				reject(new Error(message));
			};
			const timer = setTimeout(() => fail('turnstile load timed out'), SCRIPT_TIMEOUT_MS);
			s.addEventListener(
				'load',
				() => {
					clearTimeout(timer);
					resolve();
				},
				{ once: true }
			);
			s.addEventListener('error', () => fail('turnstile load failed'), { once: true });
			document.head.appendChild(s);
		});
		return scriptLoad;
	}
</script>

<script lang="ts">
	/**
	 * Cloudflare Turnstile widget (Twenty-One `.ca` theme). Renders the real
	 * Cloudflare widget explicitly on mount, keeps the verification token in a
	 * hidden input (`name`, default `captcha_token`) so it posts with the
	 * enclosing form, and re-arms after a failed submit (tokens are single-use).
	 * Only render this when captcha is enabled.
	 *
	 * The widget is ALWAYS visible (`appearance: 'always'`). It used to be
	 * `interaction-only`, which hides the widget while Cloudflare verifies
	 * silently - and that takes a few seconds that vary with the visitor's
	 * IP/browser reputation. A visitor who submitted in that window posted an
	 * empty token and got "captcha verification required" without ever having
	 * seen a captcha. Now the widget shows its own "verifying" state, and
	 * `bind:ready` tells the parent when a token exists so it can keep the
	 * submit button disabled until then.
	 *
	 * Responsive: `flexible` fills the container (Cloudflare's minimum is 300px),
	 * so below that - small phones - it falls back to `compact`.
	 *
	 * Never blank: a placeholder shows while api.js loads, and a retry button
	 * replaces the old "refresh the page" recovery if the script fails to load,
	 * the widget errors, or verification stalls.
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

	type Status = 'loading' | 'verifying' | 'interactive' | 'ready' | 'slow' | 'error';

	interface Props {
		siteKey: string;
		name?: string;
		theme?: 'auto' | 'light' | 'dark';
		/** Bump this (e.g. after a failed submit) to re-arm the single-use widget. */
		resetSignal?: number;
		/** True while a verification token is held; bind it to gate the submit button. */
		ready?: boolean;
	}

	let {
		siteKey,
		name = 'captcha_token',
		theme = 'light',
		resetSignal = 0,
		ready = $bindable(false)
	}: Props = $props();

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
	// Below this container width Cloudflare's `flexible` size no longer fits.
	const FLEXIBLE_MIN_PX = 300;
	// Silent verification normally finishes within a few seconds.
	const STALL_MS = 15_000;

	let token = $state('');
	let status = $state<Status>('loading');
	let container: HTMLDivElement | undefined = $state();
	let widgetId: string | undefined;
	// Set on mount when running headless automation against a test key.
	let autoPass = false;
	let alive = true;
	// Guards against a stale async mount finishing after a newer one started.
	let attempt = 0;
	let stallTimer: ReturnType<typeof setTimeout> | undefined;

	$effect(() => {
		ready = token !== '';
	});

	function clearStall(): void {
		clearTimeout(stallTimer);
		stallTimer = undefined;
	}

	/** If no token arrives in time, surface a way out instead of a silent hang. */
	function armStall(): void {
		clearStall();
		stallTimer = setTimeout(() => {
			if (alive && !token) status = 'slow';
		}, STALL_MS);
	}

	function removeWidget(): void {
		const ts = api();
		if (ts && widgetId !== undefined) ts.remove(widgetId);
		widgetId = undefined;
	}

	function mountWidget(): void {
		const mine = ++attempt;
		status = 'loading';
		clearStall();
		loadScript().then(
			() => {
				const ts = api();
				if (!alive || mine !== attempt || !ts || !container) return;
				const render = () => {
					if (!alive || mine !== attempt || !container) return;
					removeWidget();
					try {
						widgetId = ts.render(container, {
							sitekey: siteKey,
							theme,
							size:
								container.clientWidth > 0 && container.clientWidth < FLEXIBLE_MIN_PX
									? 'compact'
									: 'flexible',
							appearance: 'always',
							'retry-interval': 4000,
							callback: (tok: string) => {
								token = tok;
								status = 'ready';
								clearStall();
							},
							'expired-callback': () => {
								token = '';
								status = 'verifying';
								armStall();
								if (widgetId !== undefined) ts.reset(widgetId);
							},
							'error-callback': () => {
								token = '';
								status = 'error';
								clearStall();
							},
							'before-interactive-callback': () => {
								// The visitor has to act; don't call that "stalled".
								status = 'interactive';
								clearStall();
							},
							'after-interactive-callback': () => {
								status = 'verifying';
								armStall();
							}
						});
						status = 'verifying';
						armStall();
					} catch {
						status = 'error';
					}
				};
				// api.js is loaded async: `ready` defers render until Cloudflare's
				// own bootstrap has finished (immediately if it already has).
				if (typeof ts.ready === 'function') ts.ready(render);
				else render();
			},
			() => {
				/* CDN/network failure: token stays empty and the retry UI shows; the
				   server would reject an empty token with a retryable VALIDATION
				   error rather than silently allowing the action. */
				if (alive && mine === attempt) status = 'error';
			}
		);
	}

	/** Manual recovery: drop whatever is there and start over, no page refresh. */
	function retry(): void {
		token = '';
		removeWidget();
		mountWidget();
	}

	/** Clear the token and re-arm the widget (single-use tokens). */
	function doReset(): void {
		if (autoPass) {
			token = TEST_TOKEN;
			return;
		}
		token = '';
		const ts = api();
		if (ts && widgetId !== undefined) {
			status = 'verifying';
			armStall();
			ts.reset(widgetId);
		} else {
			// Nothing rendered yet (script still loading, or it failed): start over.
			retry();
		}
	}

	// Re-arm whenever the parent bumps resetSignal (skips the initial 0).
	let lastSignal = 0;
	$effect(() => {
		if (resetSignal !== lastSignal) {
			lastSignal = resetSignal;
			if (resetSignal > 0) untrack(doReset);
		}
	});

	onMount(() => {
		// Headless automation against a test key: fill a dummy token, skip the CDN.
		if (isTestKey && navigator.webdriver === true) {
			autoPass = true;
			token = TEST_TOKEN;
			status = 'ready';
			return;
		}

		// Everyone else (humans, and any production sitekey): the real widget.
		mountWidget();

		return () => {
			alive = false;
			clearStall();
			removeWidget();
		};
	});
</script>

<div class="ca-turnstile" data-testid="captcha" data-status={status}>
	<div class="ca-turnstile-box" bind:this={container}></div>
	{#if status === 'loading'}
		<div class="ca-turnstile-note" role="status" data-testid="captcha-hint">
			<span class="ca-spinner" aria-hidden="true"></span>
			{t('portal.captcha.loading')}
		</div>
	{:else if status === 'error' || status === 'slow'}
		<div class="ca-turnstile-note ca-turnstile-note--warn" role="alert" data-testid="captcha-hint">
			<span>{status === 'error' ? t('portal.captcha.failed') : t('portal.captcha.slow')}</span>
			<button
				type="button"
				class="ca-btn ca-btn-default ca-turnstile-retry"
				onclick={retry}
				data-testid="captcha-retry"
			>
				<i class="fas fa-rotate-right" aria-hidden="true"></i>
				{t('portal.captcha.retry')}
			</button>
		</div>
	{/if}
	<input type="hidden" {name} value={token} data-testid="captcha-token" />
	<noscript>
		<p class="ca-turnstile-nojs">{t('portal.captcha.jsRequired')}</p>
	</noscript>
</div>

<style>
	/* Cloudflare's standard widget is 65px tall; reserving that height means the
	   placeholder -> widget swap never shifts the form below it. */
	.ca-turnstile {
		margin: 0 0 16px;
		min-height: 65px;
	}
	.ca-turnstile-box {
		max-width: 100%;
	}
	.ca-turnstile-note {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: center;
		gap: 8px 12px;
		min-height: 65px;
		padding: 8px 12px;
		border: 1px solid #d9dde1;
		border-radius: var(--ca-radius, 3.5px);
		background: #fafafa;
		color: #555;
		font-size: 0.85rem;
		text-align: center;
	}
	/* Once the widget itself is on screen the warning only needs to sit under it. */
	.ca-turnstile-box:not(:empty) + .ca-turnstile-note {
		min-height: 0;
		margin-top: 8px;
	}
	.ca-turnstile-note--warn {
		border-color: #e7b9b5;
		background: #fdf3f2;
		color: #c43c35;
	}
	.ca-turnstile-retry {
		padding: 3px 10px;
		font-size: 0.8rem;
	}
	.ca-turnstile-nojs {
		font-size: 0.85rem;
		color: #c43c35;
		margin: 4px 0 0;
	}
</style>
