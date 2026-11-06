<script lang="ts">
	import { navigating, page } from '$app/state';
	import { t } from '$lib/i18n';
	import type { Snippet } from 'svelte';
	import '../../styles/twentyone.css';
	import CaBreadcrumb from './CaBreadcrumb.svelte';
	import CaFooter from './CaFooter.svelte';
	import CaHeader from './CaHeader.svelte';
	import CaNav from './CaNav.svelte';

	/**
	 * Twenty-One shared shell - header + primary nav + optional breadcrumb + footer
	 * around the page `children`. Scoped entirely under `.ca` so the theme never
	 * leaks into the admin (`.hp-admin`) area.
	 *
	 *  - variant 'portal'  -> plain content column (home, public pages).
	 *  - variant 'auth'    -> children centered inside a white auth card.
	 *  - variant 'store'   -> 2-col layout with the `sidebar` snippet (store/cart).
	 *  - variant 'client'  -> same chrome as portal, logged-in nav via `user`.
	 */
	interface Crumb {
		label: string;
		href?: string;
	}

	interface Props {
		variant?: 'portal' | 'auth' | 'store' | 'client';
		user?: SessionUser | null;
		breadcrumb?: Crumb[];
		children: Snippet;
		sidebar?: Snippet;
	}

	let { variant = 'portal', user, breadcrumb = [], children, sidebar }: Props = $props();

	const pathname = $derived(page.url.pathname);

	// Top progress bar while the router is navigating. Held back ~150ms so the
	// instant navigations (most of them) never flash it, but a slow load() -
	// registrar lookups, big lists - always gets visible feedback.
	const NAV_BAR_DELAY_MS = 150;
	let navBusy = $state(false);
	$effect(() => {
		if (!navigating.to) return;
		const timer = setTimeout(() => (navBusy = true), NAV_BAR_DELAY_MS);
		return () => {
			clearTimeout(timer);
			navBusy = false;
		};
	});

	// Brand link + testid depend on the area so legacy testids are preserved.
	const brandHref = $derived(
		variant === 'store' ? '/order' : variant === 'client' ? '/dashboard' : user ? '/dashboard' : '/'
	);
	const brandTestid = $derived(
		variant === 'store' ? 'order-home-link' : variant === 'client' ? 'nav-brand' : undefined
	);
	const loginTestid = $derived(variant === 'store' ? 'order-login-link' : undefined);
	const clientAreaTestid = $derived(variant === 'store' ? 'client-area-link' : undefined);
	const localeTestidPrefix = $derived(variant === 'store' ? 'locale' : 'locale-switch');
</script>

<svelte:head>
	<meta name="viewport" content="width=device-width, initial-scale=1, shrink-to-fit=no" />
	<link rel="preconnect" href="https://fonts.googleapis.com" />
	<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin="anonymous" />
	<link
		href="https://fonts.googleapis.com/css2?family=Open+Sans:wght@400;600;700;800&display=swap"
		rel="stylesheet"
	/>
	<link
		rel="stylesheet"
		href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.5.1/css/all.min.css"
		integrity="sha384-t1nt8BQoYMLFN5p42tRAtuAAFQaCQODekUVeKKZrEnEyp4H2R0RHFz0KWpmj7i8g"
		crossorigin="anonymous"
	/>
</svelte:head>

<div class="ca ca-shell">
	{#if navBusy}
		<div
			class="ca-navprogress"
			role="progressbar"
			aria-label={t('common.loading')}
			data-testid="nav-progress"
		></div>
	{/if}
	<CaHeader {user} {brandHref} {brandTestid} />
	<CaNav {user} {pathname} {loginTestid} {clientAreaTestid} />

	{#if breadcrumb.length}
		<CaBreadcrumb items={breadcrumb} />
	{/if}

	<main class="ca-main">
		<div class="ca-container">
			{#if variant === 'auth'}
				<div class="ca-auth-wrap">
					<div class="ca-auth-card">
						{@render children()}
					</div>
				</div>
			{:else if variant === 'store'}
				<div class="ca-store-grid">
					{#if sidebar}
						{@render sidebar()}
					{/if}
					<div>
						{@render children()}
					</div>
				</div>
			{:else}
				{@render children()}
			{/if}
		</div>
	</main>

	<CaFooter {localeTestidPrefix} />
</div>
