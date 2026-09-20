<script lang="ts">
	import { appName } from '$lib/appName';
	import { browser } from '$app/environment';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	type CategoryId = 'all' | 'system' | 'apps' | 'users' | 'products' | 'support' | 'security';

	const categories: { id: CategoryId; label: string }[] = [
		{ id: 'all', label: 'All' },
		{ id: 'system', label: 'System' },
		{ id: 'apps', label: 'Apps & Integrations' },
		{ id: 'users', label: 'User Management' },
		{ id: 'products', label: 'Products & Services' },
		{ id: 'support', label: 'Support' },
		{ id: 'security', label: 'API & Security' }
	];

	interface SettingCard {
		title: string;
		href: string;
		icon: string;
		desc: string;
		category: Exclude<CategoryId, 'all'>;
		/** Real signal only - see `settingsGroupKey` below. No fabricated "NEW" badges. */
		settingsGroupKey?: (typeof data.configuredKeys)[number];
	}

	// Each card links to an already-built, real admin route. The 5 "General
	// Settings" tabs (company/billing/automation/mail/tickets) all currently
	// live on the single classic `/admin/settings` tabbed page (no `?tab=`
	// deep-linking there yet - see the note near the bottom of this file), so
	// those 5 cards share one href; every other card links straight to its own
	// distinct, existing page.
	const cards: SettingCard[] = [
		{
			title: 'General Settings',
			href: '/admin/settings',
			icon: 'fas fa-sliders-h',
			desc: 'Company name, contact email and other core system-wide details.',
			category: 'system',
			settingsGroupKey: 'company'
		},
		{
			title: 'Billing & Tax',
			href: '/admin/settings',
			icon: 'fas fa-file-invoice-dollar',
			desc: 'Tax rate, invoice due days, late fees and payment reminders.',
			category: 'system',
			settingsGroupKey: 'billing'
		},
		{
			title: 'Automation',
			href: '/admin/settings',
			icon: 'fas fa-robot',
			desc: 'Days overdue before a service is automatically suspended or terminated.',
			category: 'system',
			settingsGroupKey: 'automation'
		},
		{
			title: 'Mail Settings',
			href: '/admin/settings',
			icon: 'far fa-envelope',
			desc: 'Default from-name and from-address used for outbound system email.',
			category: 'system',
			settingsGroupKey: 'mail'
		},
		{
			title: 'Ticket Settings',
			href: '/admin/settings',
			icon: 'fas fa-life-ring',
			desc: 'Allowed attachment extensions and max attachment size for tickets.',
			category: 'system',
			settingsGroupKey: 'tickets'
		},
		{
			title: 'Email Templates',
			href: '/admin/email-templates',
			icon: 'far fa-file-alt',
			desc: 'Edit the outbound email templates used across the system.',
			category: 'system'
		},
		{
			title: 'Email Log',
			href: '/admin/logs/email',
			icon: 'far fa-envelope-open',
			desc: 'Outbound email history and delivery status.',
			category: 'system'
		},
		{
			title: 'Payment Gateways',
			href: '/admin/gateways',
			icon: 'fas fa-credit-card',
			desc: 'Configure the Duitku payment gateway mode and activation.',
			category: 'apps'
		},
		{
			title: 'Integration Log',
			href: '/admin/logs/integration',
			icon: 'fas fa-plug',
			desc: 'External API calls to payment gateway, control panels and registrar.',
			category: 'apps'
		},
		{
			title: 'Administrator Users',
			href: '/admin/staff',
			icon: 'fas fa-user-shield',
			desc: `Manage admin and staff accounts that can sign in to ${appName}.`,
			category: 'users'
		},
		{
			title: 'Products/Services',
			href: '/admin/products',
			icon: 'fas fa-box',
			desc: 'Configure the products and services offered on the order page.',
			category: 'products'
		},
		{
			title: 'Product Groups',
			href: '/admin/product-groups',
			icon: 'fas fa-layer-group',
			desc: 'Group products in the order catalog and control their display order.',
			category: 'products'
		},
		{
			title: 'Servers',
			href: '/admin/servers',
			icon: 'fas fa-server',
			desc: `Servers ${appName} provisions on for automatic setup.`,
			category: 'products'
		},
		{
			title: 'Domain Registrars',
			href: '/admin/registrars',
			icon: 'fas fa-globe',
			desc: 'Configure the RDash domain registrar integration.',
			category: 'products'
		},
		{
			title: 'Domain Pricing',
			href: '/admin/domains/pricing',
			icon: 'fas fa-globe-americas',
			desc: 'Configure per-TLD registration/renewal pricing and premium domain overrides.',
			category: 'products'
		},
		{
			title: 'Domain Addons',
			href: '/admin/domains/addons',
			icon: 'fas fa-puzzle-piece',
			desc: 'Configure ID Protection, DNS Management and Email Forwarding pricing.',
			category: 'products'
		},
		{
			title: 'Promotions',
			href: '/admin/coupons',
			icon: 'fas fa-tags',
			desc: 'Manage discount codes for orders.',
			category: 'products'
		},
		{
			title: 'Support Tickets',
			href: '/admin/tickets',
			icon: 'fas fa-headset',
			desc: 'View and respond to all customer support tickets.',
			category: 'support'
		},
		{
			title: 'Support Departments',
			href: '/admin/departments',
			icon: 'fas fa-sitemap',
			desc: 'Manage the departments customers can open tickets against.',
			category: 'support'
		},
		{
			title: 'Activity Log',
			href: '/admin/logs/audit',
			icon: 'fas fa-shield-alt',
			desc: 'Audit trail of sensitive actions taken by admins and staff.',
			category: 'security'
		}
	];

	let selectedCategory = $state<CategoryId>('all');
	let query = $state('');
	let sortBy = $state<'az' | 'category'>('az');

	const filteredCards = $derived.by(() => {
		const q = query.trim().toLowerCase();
		let out = cards.filter(
			(c) =>
				(selectedCategory === 'all' || c.category === selectedCategory) &&
				(q === '' || c.title.toLowerCase().includes(q))
		);
		out = [...out].sort((a, b) =>
			sortBy === 'category'
				? a.category.localeCompare(b.category) || a.title.localeCompare(b.title)
				: a.title.localeCompare(b.title)
		);
		return out;
	});

	function isUpdated(card: SettingCard): boolean {
		return !!card.settingsGroupKey && data.configuredKeys.includes(card.settingsGroupKey);
	}

	function slug(title: string): string {
		return title
			.toLowerCase()
			.replace(/[^a-z0-9]+/g, '-')
			.replace(/^-+|-+$/g, '');
	}

	// "Recently Visited" is a real, functioning feature (not a static mockup):
	// it's backed by the browser's localStorage, updated whenever a card is
	// clicked, and read back on load - there's no server-side visit-tracking
	// signal available to this page, so this is the truthful way to implement
	// it client-side.
	const RECENT_KEY = 'whcms_admin_settings_recent';
	let recent = $state<{ title: string; href: string }[]>([]);

	$effect(() => {
		if (!browser) return;
		try {
			const raw = localStorage.getItem(RECENT_KEY);
			recent = raw ? JSON.parse(raw) : [];
		} catch {
			recent = [];
		}
	});

	function trackVisit(card: SettingCard) {
		if (!browser) return;
		try {
			const next = [
				{ title: card.title, href: card.href },
				...recent.filter((r) => r.href !== card.href)
			].slice(0, 5);
			localStorage.setItem(RECENT_KEY, JSON.stringify(next));
			recent = next;
		} catch {
			// Private-mode / quota errors are non-fatal - recently-visited is a nicety.
		}
	}
</script>

<svelte:head>
	<title>System Settings — {appName} Admin</title>
</svelte:head>

<div class="ov-header">
	<div class="ov-header-main">
		<h1 class="ov-title">System Settings</h1>
		<p class="ov-subtitle">Set up and configure your {appName} installation.</p>
	</div>

	<div class="ov-header-setup" data-testid="setup-progress">
		<a class="ov-setup-link" href="#all-settings">Click here to view the setup tasks</a>
		<div class="ov-progress-row">
			<div class="ov-progress-track">
				<div class="ov-progress-fill" style={`width:${data.setupPercent}%`}></div>
			</div>
			<span class="ov-progress-pct">{data.setupPercent}%</span>
		</div>
		<span class="ov-progress-sub"
			>{data.configuredGroups} of {data.totalGroups} settings areas configured</span
		>
	</div>
</div>

{#if data.listError}
	<div class="hp-alert-yellow" style="margin-bottom:14px" data-testid="setup-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

<div class="ov-layout">
	<aside class="ov-aside">
		<div class="ov-search">
			<i class="fas fa-search ov-search-icon" aria-hidden="true"></i>
			<input
				class="ov-search-input"
				type="search"
				placeholder="Search"
				bind:value={query}
				data-testid="setup-search"
			/>
		</div>

		<nav class="ov-cats" aria-label="Settings categories">
			{#each categories as c (c.id)}
				<button
					type="button"
					class="ov-cat"
					class:active={selectedCategory === c.id}
					onclick={() => (selectedCategory = c.id)}
					data-testid={`setup-category-${c.id}`}
				>
					{c.label}
				</button>
			{/each}
		</nav>

		<div class="ov-recent">
			<div class="ov-recent-hd">Recently Visited</div>
			{#if recent.length === 0}
				<p class="ov-recent-empty">Pages you open from this screen will show up here.</p>
			{:else}
				{#each recent as r (r.href)}
					<a class="ov-recent-link" href={r.href}>{r.title}</a>
				{/each}
			{/if}
		</div>
	</aside>

	<section class="ov-main" id="all-settings">
		<div class="ov-main-hd">
			<h2 class="ov-main-title">All Settings</h2>
			<select class="ov-sort" bind:value={sortBy} data-testid="setup-sort">
				<option value="az">Alphabetical</option>
				<option value="category">Category</option>
			</select>
		</div>

		<div class="ov-grid">
			{#each filteredCards as card (card.title)}
				<a
					class="ov-card"
					href={card.href}
					onclick={() => trackVisit(card)}
					data-testid={`setup-card-${slug(card.title)}`}
				>
					<div class="ov-card-iconwrap">
						<i class={`${card.icon} ov-card-icon`}></i>
					</div>
					<div class="ov-card-body">
						<div class="ov-card-title-row">
							<span class="ov-card-title">{card.title}</span>
							{#if isUpdated(card)}
								<span class="ov-badge ov-badge-updated">UPDATED</span>
							{/if}
						</div>
						<p class="ov-card-desc">{card.desc}</p>
					</div>
				</a>
			{:else}
				<div class="ov-empty">No settings match your search.</div>
			{/each}
		</div>
	</section>
</div>

<style>
	/* ---------- header ---------- */
	.ov-header {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 32px;
		flex-wrap: wrap;
		padding: 4px 0 20px;
		border-bottom: 1px solid #e7e7e7;
		margin-bottom: 22px;
	}
	.ov-header-main {
		min-width: 260px;
	}
	.ov-title {
		font-size: 26px;
		font-weight: 400;
		color: #2f2f2f;
		margin: 0 0 8px;
		line-height: 1.2;
	}
	.ov-subtitle {
		font-size: 15px;
		color: #777;
		margin: 0;
	}

	.ov-header-setup {
		min-width: 300px;
		text-align: right;
		padding-top: 4px;
	}
	.ov-setup-link {
		color: var(--hp-link);
		font-weight: 700;
		font-size: 15px;
	}
	.ov-setup-link:hover {
		text-decoration: underline;
	}
	.ov-progress-row {
		display: flex;
		align-items: center;
		gap: 12px;
		margin-top: 12px;
	}
	.ov-progress-track {
		flex: 1;
		height: 14px;
		border-radius: 7px;
		background: #ededed;
		overflow: hidden;
	}
	.ov-progress-fill {
		height: 100%;
		background: #5cb85c;
		border-radius: 7px;
		transition: width 0.3s ease;
	}
	.ov-progress-pct {
		font-size: 14px;
		color: #666;
		font-weight: 400;
		min-width: 38px;
		text-align: right;
	}
	.ov-progress-sub {
		display: block;
		margin-top: 6px;
		font-size: 12px;
		color: #aaa;
	}

	/* ---------- layout ---------- */
	.ov-layout {
		display: grid;
		grid-template-columns: 232px minmax(0, 1fr);
		gap: 30px;
		align-items: start;
	}

	/* ---------- sidebar ---------- */
	.ov-search {
		position: relative;
		margin-bottom: 18px;
	}
	.ov-search-icon {
		position: absolute;
		left: 13px;
		top: 50%;
		transform: translateY(-50%);
		color: #aaa;
		font-size: 14px;
		pointer-events: none;
	}
	.ov-search-input {
		width: 100%;
		padding: 10px 12px 10px 36px;
		border: 1px solid #ccc;
		border-radius: 4px;
		font: inherit;
		font-size: 14px;
		background: #fff;
		color: #333;
	}
	.ov-search-input:focus {
		outline: none;
		border-color: #66afe9;
		box-shadow: 0 0 6px rgba(102, 175, 233, 0.4);
	}

	.ov-cats {
		display: flex;
		flex-direction: column;
	}
	.ov-cat {
		display: block;
		width: 100%;
		border: none;
		border-left: 3px solid transparent;
		background: none;
		text-align: left;
		font: inherit;
		font-size: 14px;
		color: #444;
		padding: 10px 12px;
		cursor: pointer;
	}
	.ov-cat:hover {
		background: #f6f6f6;
	}
	.ov-cat.active {
		border-left-color: #5cb85c;
		background: #eef7ee;
		color: #3a833d;
		font-weight: 600;
	}

	.ov-recent {
		margin-top: 22px;
		border-top: 1px solid #ececec;
		padding-top: 14px;
	}
	.ov-recent-hd {
		font-size: 13px;
		font-weight: 700;
		color: #666;
		text-transform: none;
		padding: 0 12px 8px;
	}
	.ov-recent-empty {
		color: #aaa;
		font-size: 12px;
		padding: 0 12px;
		margin: 0;
		line-height: 1.5;
	}
	.ov-recent-link {
		display: block;
		padding: 6px 12px;
		font-size: 13px;
		color: var(--hp-link);
	}
	.ov-recent-link:hover {
		text-decoration: underline;
	}

	/* ---------- main ---------- */
	.ov-main-hd {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding-bottom: 12px;
		margin-bottom: 20px;
		border-bottom: 1px solid #e7e7e7;
	}
	.ov-main-title {
		font-size: 22px;
		font-weight: 400;
		color: #333;
		margin: 0;
	}
	.ov-sort {
		width: auto;
		padding: 7px 10px;
		border: 1px solid #ccc;
		border-radius: 4px;
		font: inherit;
		font-size: 13px;
		background: #fff;
		color: #333;
	}
	.ov-sort:focus {
		outline: none;
		border-color: #66afe9;
		box-shadow: 0 0 6px rgba(102, 175, 233, 0.4);
	}

	.ov-grid {
		display: grid;
		grid-template-columns: repeat(3, 1fr);
		gap: 20px;
	}

	.ov-card {
		display: flex;
		flex-direction: column;
		background: #fff;
		border: 1px solid #e2e2e2;
		border-radius: 3px;
		color: inherit;
		overflow: hidden;
		transition:
			box-shadow 0.15s ease,
			border-color 0.15s ease;
	}
	.ov-card:hover {
		border-color: #cfcfcf;
		box-shadow: 0 2px 10px rgba(0, 0, 0, 0.08);
	}
	.ov-card-iconwrap {
		display: flex;
		align-items: center;
		justify-content: center;
		padding: 30px 12px;
		border-bottom: 1px solid #eee;
	}
	.ov-card-icon {
		font-size: 46px;
		color: #c6c6c6;
		line-height: 1;
	}
	.ov-card-body {
		padding: 16px 20px 20px;
		min-width: 0;
	}
	.ov-card-title-row {
		display: flex;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
	}
	.ov-card-title {
		color: #33547d;
		font-weight: 600;
		font-size: 15.5px;
	}
	.ov-card:hover .ov-card-title {
		color: var(--hp-link);
	}
	.ov-card-desc {
		color: #999;
		font-size: 13px;
		line-height: 1.55;
		margin: 8px 0 0;
	}
	.ov-badge {
		display: inline-block;
		font-size: 9.5px;
		font-weight: 700;
		letter-spacing: 0.3px;
		text-transform: uppercase;
		padding: 2px 6px;
		border-radius: 3px;
		color: #fff;
		line-height: 1.1;
	}
	.ov-badge-updated {
		background: var(--hp-primary, #337ab7);
	}

	.ov-empty {
		grid-column: 1 / -1;
		text-align: center;
		padding: 32px;
		color: #999;
	}

	@media (max-width: 980px) {
		.ov-layout {
			grid-template-columns: 200px minmax(0, 1fr);
			gap: 20px;
		}
		.ov-grid {
			grid-template-columns: repeat(2, 1fr);
		}
	}
	@media (max-width: 720px) {
		.ov-layout {
			grid-template-columns: 1fr;
		}
		.ov-header-setup {
			text-align: left;
			min-width: 0;
			width: 100%;
		}
	}
	@media (max-width: 560px) {
		.ov-grid {
			grid-template-columns: 1fr;
		}
	}
</style>
