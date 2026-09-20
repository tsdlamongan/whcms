<script lang="ts">
	import { appName } from '$lib/appName';
	import { Alert, EmptyState, MoneyText } from '$lib/components';
	import CaPrice from '$lib/components/ca/CaPrice.svelte';
	import { t } from '$lib/i18n';
	import { CYCLE_ORDER } from '$lib/stores/cart.svelte';
	import type { CatalogPricing, CatalogProduct } from './+page.server';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	const visibleGroups = $derived(
		data.groups.filter((g) => !g.hidden).sort((a, b) => a.sort - b.sort || a.id - b.id)
	);
	// WHMCS-style: there's no "all products" view - landing on the store with
	// no ?group= picks the first category, same as clicking it in the sidebar.
	const activeGroup = $derived(
		visibleGroups.find((g) => g.slug === data.activeGroupSlug) ?? visibleGroups[0] ?? null
	);
	// data.products is already the visible catalog, pre-sorted by the backend
	// (sort, id) within each group; only narrow it to the active group.
	const products = $derived(
		data.products.filter((p) => !activeGroup || p.group_id === activeGroup.id)
	);

	/** Cheapest priced cycle for the "starting from" line. */
	function startingPrice(product: CatalogProduct): CatalogPricing | null {
		const rows = (product.pricing ?? []).slice().sort((a, b) => a.price - b.price);
		return rows.find((r) => r.price > 0) ?? rows[0] ?? null;
	}

	function cycleLabel(cycle: string): string {
		return t(`orderfe.cycle.${cycle}`);
	}

	/** Number of billing cycles offered (for the "N cycles" hint). */
	function cycleCount(product: CatalogProduct): number {
		const cycles = new Set((product.pricing ?? []).map((r) => r.cycle));
		return CYCLE_ORDER.filter((c) => cycles.has(c)).length;
	}
</script>

<svelte:head>
	<title>{activeGroup?.name ?? t('orderfe.catalog.title')} — {appName}</title>
</svelte:head>

<h1 class="ca-h1">{activeGroup?.name ?? t('orderfe.catalog.title')}</h1>
{#if !activeGroup}
	<p class="ca-lead">{t('orderfe.catalog.subtitle')}</p>
{/if}

{#if data.loadError}
	<Alert type="error" title={t('orderfe.catalog.loadError')}>{data.loadError}</Alert>
{/if}

<!-- Domain search banner -->
<section class="ca-hero">
	<div class="ca-hero-box">
		<h2 class="ca-h3" style="margin-bottom:4px;">{t('orderfe.catalog.domainBanner')}</h2>
		<p class="ca-muted" style="margin:0;">{t('orderfe.catalog.domainBannerDesc')}</p>
		<div class="ca-hero-actions">
			<a class="ca-btn ca-btn-primary" href="/order/domain" data-testid="domain-search-link">
				<i class="fas fa-search" aria-hidden="true"></i>
				{t('orderfe.catalog.searchDomain')}
			</a>
		</div>
	</div>
</section>

{#if products.length === 0}
	{#if !data.loadError}
		<section class="ca-card">
			<div class="ca-card-body">
				<EmptyState
					title={t('orderfe.catalog.empty')}
					description={t('orderfe.catalog.emptyDesc')}
				/>
			</div>
		</section>
	{/if}
{:else}
	<div class="ca-product-grid" data-testid="product-list">
		{#each products as product (product.id)}
			{@const price = startingPrice(product)}
			<div class="ca-product-card" data-testid={`product-card-${product.slug}`}>
				<div class="ca-product-header">{product.name}</div>
				<div class="ca-product-body">
					{#if product.description}
						<p class="ca-muted line-clamp-3" style="margin:0;white-space:pre-line;">
							{product.description}
						</p>
					{/if}

					<div style="flex:1 1 auto;"></div>

					<div>
						<div class="ca-price-block">
							{#if price}
								<div class="ca-muted" style="font-size:0.78rem;margin-bottom:2px;">
									{t('orderfe.catalog.startingFrom')}
								</div>
								<CaPrice amount={price.price} cycle={cycleLabel(price.cycle)} />
								{#if price.setup_fee > 0}
									<div class="ca-muted" style="font-size:0.78rem;margin-top:4px;">
										+ <MoneyText amount={price.setup_fee} />
										{t('orderfe.product.setupFee').toLowerCase()}
									</div>
								{/if}
								{#if cycleCount(product) > 1}
									<div class="ca-muted" style="font-size:0.78rem;margin-top:2px;">
										{cycleCount(product)}&times; {t('orderfe.product.billingCycle').toLowerCase()}
									</div>
								{/if}
							{:else}
								<div class="ca-muted" style="font-size:0.9rem;font-weight:600;">
									{t('orderfe.catalog.noPricing')}
								</div>
							{/if}
						</div>

						<a
							class="ca-btn ca-btn-success ca-btn-block ca-order-now"
							href={`/order/product/${encodeURIComponent(product.slug)}`}
							data-testid={`product-order-${product.slug}`}
						>
							<i class="fas fa-shopping-cart" aria-hidden="true"></i>
							{t('orderfe.catalog.orderNow')}
						</a>
					</div>
				</div>
			</div>
		{/each}
	</div>
{/if}
