<script lang="ts">
	import { appName } from '$lib/appName';
	import { Alert } from '$lib/components';
	import CaDomainHero from '$lib/components/ca/CaDomainHero.svelte';
	import CaProductCard from '$lib/components/ca/CaProductCard.svelte';
	import CaTile from '$lib/components/ca/CaTile.svelte';
	import { t } from '$lib/i18n';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	const groups = $derived(
		data.groups.filter((g) => !g.hidden).sort((a, b) => a.sort - b.sort || a.id - b.id)
	);

	// §7c "How can we help today" tiles.
	const helpTiles = [
		{ id: 'announcements', icon: 'fas fa-bullhorn', border: 'teal', href: '/announcements' },
		{ id: 'network-status', icon: 'fas fa-server', border: 'red', href: '/network-status' },
		{ id: 'knowledgebase', icon: 'fas fa-book', border: 'yellow', href: '/knowledgebase' },
		{ id: 'downloads', icon: 'fas fa-download', border: 'gray', href: '/knowledgebase' },
		{ id: 'submit-ticket', icon: 'fas fa-life-ring', border: 'green', href: '/contact' }
	] as const;

	// §7d "Your Account" tiles (anon -> routed to login/public entry points).
	const accountTiles = [
		{ id: 'your-account', icon: 'fas fa-home', href: '/login' },
		{ id: 'manage-services', icon: 'fas fa-cubes', href: '/services' },
		{ id: 'manage-domains', icon: 'fas fa-globe', href: '/domains' },
		{ id: 'support-requests', icon: 'fas fa-comments', href: '/support' },
		{ id: 'make-payment', icon: 'fas fa-credit-card', href: '/billing' }
	] as const;

	const tileLabel: Record<string, string> = {
		announcements: 'portal.tiles.announcements',
		'network-status': 'portal.tiles.networkStatus',
		knowledgebase: 'portal.tiles.knowledgebase',
		downloads: 'portal.tiles.downloads',
		'submit-ticket': 'portal.tiles.submitTicket',
		'your-account': 'portal.tiles.yourAccount',
		'manage-services': 'portal.tiles.manageServices',
		'manage-domains': 'portal.tiles.manageDomains',
		'support-requests': 'portal.tiles.supportRequests',
		'make-payment': 'portal.tiles.makePayment'
	};
</script>

<svelte:head>
	<title>{appName}</title>
</svelte:head>

<div data-testid="portal-home">
	{#if data.loadError}
		<Alert type="warning">{data.loadError}</Alert>
	{/if}

	<!-- §7a Domain search hero -->
	<CaDomainHero testid="home-domain-search" />

	<!-- §7b Browse our Products / Services -->
	<section class="ca-hero">
		<h2 class="ca-h2 ca-hero-title">{t('portal.home.browseTitle')}</h2>
		<div class="ca-product-grid">
			{#each groups as group (group.id)}
				<CaProductCard
					name={group.name}
					orderHref={`/order?group=${encodeURIComponent(group.slug)}`}
					ctaLabel={t('portal.home.browseProducts')}
					testid={`home-product-${group.slug}`}
				/>
			{/each}
			<CaProductCard
				name={t('portal.home.registerName')}
				description={t('portal.home.registerDesc')}
				orderHref="/order/domain"
				ctaLabel={t('portal.home.registerCta')}
				testid="home-product-register"
			/>
			<CaProductCard
				name={t('portal.home.transferName')}
				description={t('portal.home.transferDesc')}
				orderHref="/order/domain"
				ctaLabel={t('portal.home.transferCta')}
				testid="home-product-transfer"
			/>
		</div>
	</section>

	<!-- §7c How can we help today -->
	<section class="ca-hero">
		<h2 class="ca-h2 ca-hero-title">{t('portal.help.title')}</h2>
		<div class="ca-tiles-5">
			{#each helpTiles as tile (tile.id)}
				<CaTile
					icon={tile.icon}
					label={t(tileLabel[tile.id])}
					href={tile.href}
					border={tile.border}
					testid={`home-tile-${tile.id}`}
				/>
			{/each}
		</div>
	</section>

	<!-- §7d Your Account -->
	<section class="ca-hero">
		<h2 class="ca-h2 ca-hero-title">{t('portal.help.yourAccount')}</h2>
		<div class="ca-tiles-5">
			{#each accountTiles as tile (tile.id)}
				<CaTile
					icon={tile.icon}
					label={t(tileLabel[tile.id])}
					href={tile.href}
					border="dark"
					testid={`home-tile-${tile.id}`}
				/>
			{/each}
		</div>
	</section>
</div>
