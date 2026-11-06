<script lang="ts">
	import { navigating } from '$app/state';
	import { appName } from '$lib/appName';
	import { Alert, EmptyState, MoneyText } from '$lib/components';
	import CaPanel from '$lib/components/ca/CaPanel.svelte';
	import { t } from '$lib/i18n';
	import { cart, isValidDomain } from '$lib/stores/cart.svelte';
	import { toast } from '$lib/stores/toast.svelte';
	import { untrack } from 'svelte';
	import type { DomainCheckResult } from './+page.server';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	let query = $state(untrack(() => data.q));
	$effect(() => {
		query = data.q;
	});

	// The search is a GET form, so "in flight" means the router is navigating to
	// /order/domain?q=… while this page's load() asks the registrar - which can
	// take seconds. Without feedback the button gets clicked again and again,
	// and every extra click is another registrar lookup.
	const searching = $derived(
		navigating.to?.url.pathname === '/order/domain' && navigating.to.url.searchParams.has('q')
	);

	// Per-result years/addon selection (register + transfer)
	let years = $state<Record<string, number>>({});
	let selectedAddons = $state<Record<string, string[]>>({});

	function yearsFor(result: DomainCheckResult): number {
		return years[result.name] ?? 1;
	}
	function addonsFor(name: string): string[] {
		return selectedAddons[name] ?? [];
	}
	function toggleAddon(name: string, key: string) {
		const cur = new Set(addonsFor(name));
		if (cur.has(key)) cur.delete(key);
		else cur.add(key);
		selectedAddons = { ...selectedAddons, [name]: [...cur] };
	}
	function addonsTotal(name: string): number {
		const keys = new Set(addonsFor(name));
		return data.addons.filter((a) => keys.has(a.key)).reduce((sum, a) => sum + a.price, 0);
	}
	/** Register price for a given term length: the real per-year matrix when
	 *  the TLD has one (never assume linear - e.g. a free-first-year promo
	 *  makes year 1 != year N / N), else the flat-rate price*years estimate
	 *  (premium/length-tier overrides and the live-registrar-quote fallback
	 *  ARE flat annual rates, so that's accurate for those). */
	function registerBasePrice(result: DomainCheckResult, yrs: number): number {
		const perYear = result.register_prices?.[String(yrs)];
		return perYear !== undefined ? perYear : result.price * yrs;
	}
	function estimatedTotal(result: DomainCheckResult): number {
		const years = yearsFor(result);
		return registerBasePrice(result, years) + addonsTotal(result.name) * years;
	}
	function addonLabels(name: string): { name: string; price: number }[] {
		const keys = new Set(addonsFor(name));
		return data.addons
			.filter((a) => keys.has(a.key))
			.map((a) => ({ name: a.name, price: a.price }));
	}

	// Transfer-your-own-domain form
	let transferDomain = $state('');
	let transferEpp = $state('');
	let transferError = $state<string | null>(null);

	/** Everything after the first label: "toko.co.id" -> "co.id". */
	function extensionOf(name: string): string {
		return name.slice(name.indexOf('.') + 1);
	}

	function addRegister(result: DomainCheckResult) {
		cart.add({
			item_type: 'domain_register',
			domain: result.name,
			cycle: 'annually',
			unit_price: estimatedTotal(result),
			setup_fee: 0,
			domain_years: yearsFor(result),
			domain_addons: addonsFor(result.name),
			domain_addon_labels: addonLabels(result.name)
		});
		toast.success(t('orderfe.domain.addedRegister', { domain: result.name }));
	}

	function addTransfer(name: string, price: number, epp?: string, yrs = 1, addons: string[] = []) {
		const addonSum = data.addons
			.filter((a) => addons.includes(a.key))
			.reduce((sum, a) => sum + a.price, 0);
		cart.add({
			item_type: 'domain_transfer',
			domain: name,
			cycle: 'annually',
			unit_price: (price + addonSum) * yrs,
			setup_fee: 0,
			epp_code: epp || undefined,
			domain_years: yrs,
			domain_addons: addons,
			domain_addon_labels: addonLabels(name)
		});
		toast.success(t('orderfe.domain.addedTransfer', { domain: name }));
	}

	function addTransferOwn() {
		transferError = null;
		const name = transferDomain.trim().toLowerCase();
		if (!isValidDomain(name)) {
			transferError = t('orderfe.domain.transferOwnInvalid');
			return;
		}
		const ext = name.slice(name.indexOf('.') + 1);
		if (data.sellableTlds && !data.sellableTlds.includes(ext)) {
			transferError = t('orderfe.domain.notOfferedHint', { ext });
			return;
		}
		addTransfer(name, 0, transferEpp.trim());
		transferDomain = '';
		transferEpp = '';
	}
</script>

<svelte:head>
	<title>{t('orderfe.domain.title')} — {appName}</title>
</svelte:head>

<h1 class="ca-h1">{t('orderfe.domain.title')}</h1>
<p class="ca-lead">{t('orderfe.domain.subtitle')}</p>

<!-- Search form (GET, querystring-driven) -->
<form
	method="GET"
	class="ca-domain-search"
	data-sveltekit-keepfocus
	data-testid="domain-search-form"
	aria-busy={searching}
	onsubmit={(e) => {
		// Enter in the input would otherwise slip past the disabled button.
		if (searching) e.preventDefault();
	}}
>
	<input
		type="text"
		name="q"
		class="ca-input"
		placeholder={t('orderfe.domain.searchPlaceholder')}
		bind:value={query}
		autocomplete="off"
		spellcheck="false"
		data-testid="domain-search-input"
	/>
	<button
		type="submit"
		class="ca-btn ca-btn-primary"
		disabled={searching}
		aria-busy={searching}
		data-testid="domain-search-submit"
	>
		{#if searching}
			<span class="ca-spinner" aria-hidden="true"></span>
		{:else}
			<i class="fas fa-search" aria-hidden="true"></i>
		{/if}
		{t('orderfe.domain.search')}
	</button>
</form>

{#if data.invalidQuery}
	<Alert type="warning">{t('orderfe.domain.invalidQuery')}</Alert>
{:else if data.searchError}
	<Alert type="error" title={t('orderfe.domain.error')}>{data.searchError}</Alert>
{:else if data.results === null}
	<section class="ca-card">
		<div class="ca-card-body">
			<EmptyState
				title={t('orderfe.domain.emptyTitle')}
				description={t('orderfe.domain.emptyDesc')}
			>
				{#snippet icon()}
					<i class="fas fa-search" style="font-size:34px;color:#c7ccd1;" aria-hidden="true"></i>
				{/snippet}
			</EmptyState>
		</div>
	</section>
{:else}
	<section
		class="ca-card ca-dom-list"
		class:ca-loading={searching}
		aria-busy={searching}
		data-testid="domain-results"
	>
		<div class="ca-card-header">{t('orderfe.domain.resultsFor', { q: data.q })}</div>
		{#each data.results as result (result.name)}
			<div class="ca-dom-row" data-testid={`domain-result-${result.name}`}>
				<div class="ca-dom-name">
					<span class="ca-dom-domain">{result.name}</span>
					{#if result.sellable === false}
						<span
							class="ca-badge ca-badge--gray"
							title={t('orderfe.domain.notOfferedHint', { ext: extensionOf(result.name) })}
							data-testid={`domain-not-offered-${result.name}`}
							>{t('orderfe.domain.notOffered')}</span
						>
					{:else if result.available}
						<span class="ca-badge ca-badge--success">{t('orderfe.domain.available')}</span>
						{#if result.premium}
							<span class="ca-badge ca-badge--warning">{t('orderfe.domain.premium')}</span>
						{/if}
						{#if result.document_required}
							<span
								class="ca-badge ca-badge--info"
								title={t('orderfe.domain.docsRequiredHint')}
								data-testid={`domain-docs-badge-${result.name}`}
								>{t('orderfe.domain.docsRequired')}</span
							>
						{/if}
					{:else}
						<span class="ca-badge ca-badge--danger">{t('orderfe.domain.taken')}</span>
					{/if}
				</div>

				<div class="ca-dom-price">
					{#if result.sellable === false}
						<span class="ca-muted">—</span>
					{:else}
						<MoneyText amount={estimatedTotal(result)} class="font-bold" /><span class="ca-muted"
							>{t('orderfe.domain.perYear')}</span
						>
					{/if}
				</div>

				<div class="ca-dom-action">
					{#if result.sellable === false}
						<span class="ca-muted" style="font-size:0.85rem;"
							>{t('orderfe.domain.notOfferedHint', { ext: extensionOf(result.name) })}</span
						>
					{:else if cart.hasDomain(result.name)}
						<a
							href="/order/cart"
							class="ca-btn ca-btn-outline-success"
							data-testid={`domain-in-cart-${result.name}`}
						>
							<i class="fas fa-check" aria-hidden="true"></i>
							{t('orderfe.domain.inCart')}
						</a>
					{:else if result.available}
						<button
							type="button"
							class="ca-btn ca-btn-primary"
							onclick={() => addRegister(result)}
							data-testid={`domain-register-${result.name}`}
						>
							{t('orderfe.domain.register')}
						</button>
					{:else}
						<button
							type="button"
							class="ca-btn ca-btn-default"
							onclick={() =>
								addTransfer(
									result.name,
									result.price,
									undefined,
									yearsFor(result),
									addonsFor(result.name)
								)}
							data-testid={`domain-transfer-${result.name}`}
						>
							{t('orderfe.domain.transfer')}
						</button>
					{/if}
				</div>

				{#if result.sellable !== false && !cart.hasDomain(result.name)}
					<div class="ca-dom-opts">
						<label class="ca-dom-opt">
							<span class="ca-dom-cap">{t('orderfe.domain.years')}</span>
							<select
								class="ca-select"
								value={yearsFor(result)}
								onchange={(e) =>
									(years = {
										...years,
										[result.name]: Number((e.target as HTMLSelectElement).value)
									})}
								data-testid={`domain-years-${result.name}`}
							>
								{#each Array.from({ length: result.max_years - result.min_years + 1 }, (_, n) => result.min_years + n) as y (y)}
									<option value={y}>{y}{t('orderfe.domain.yearSuffix')}</option>
								{/each}
							</select>
						</label>

						{#if data.addons.length > 0}
							<div class="ca-dom-addons">
								<span class="ca-dom-cap">{t('orderfe.domain.addonsLabel')}:</span>
								{#each data.addons as addon (addon.key)}
									<label class="ca-dom-addon">
										<input
											type="checkbox"
											checked={addonsFor(result.name).includes(addon.key)}
											onchange={() => toggleAddon(result.name, addon.key)}
											data-testid={`domain-addon-${result.name}-${addon.key}`}
										/>
										<span>{addon.name} (<MoneyText amount={addon.price} />)</span>
									</label>
								{/each}
							</div>
						{/if}
					</div>
				{/if}
			</div>
		{:else}
			<div class="ca-card-body">
				<EmptyState
					title={t('orderfe.domain.emptyTitle')}
					description={t('orderfe.domain.emptyDesc')}
				/>
			</div>
		{/each}
	</section>
{/if}

<!-- Transfer a domain you already own -->
<div style="margin-top:24px;">
	<CaPanel title={t('orderfe.domain.transferOwnTitle')}>
		<p class="ca-muted" style="margin:0 0 16px;">{t('orderfe.domain.transferOwnDesc')}</p>

		<div class="grid grid-cols-1 gap-3 sm:grid-cols-2" style="max-width:42rem;">
			<div class="ca-formrow" style="margin-bottom:0;">
				<label class="ca-label" for="transfer-own-domain">
					{t('orderfe.domain.transferOwnDomain')}
				</label>
				<input
					id="transfer-own-domain"
					type="text"
					class="ca-input"
					placeholder={t('orderfe.domain.searchPlaceholder')}
					bind:value={transferDomain}
					autocomplete="off"
					spellcheck="false"
					data-testid="transfer-own-domain-input"
				/>
			</div>
			<div class="ca-formrow" style="margin-bottom:0;">
				<label class="ca-label" for="transfer-own-epp">
					{t('orderfe.domain.transferOwnEpp')}
				</label>
				<input
					id="transfer-own-epp"
					type="text"
					class="ca-input"
					bind:value={transferEpp}
					autocomplete="off"
					data-testid="transfer-own-epp-input"
				/>
			</div>
		</div>

		{#if transferError}
			<p style="margin-top:10px;color:#c43c35;font-size:0.9rem;" role="alert">{transferError}</p>
		{/if}

		<button
			type="button"
			class="ca-btn ca-btn-primary"
			style="margin-top:16px;"
			onclick={addTransferOwn}
			data-testid="transfer-own-add"
		>
			<i class="fas fa-exchange-alt" aria-hidden="true"></i>
			{t('orderfe.domain.transferOwnAdd')}
		</button>
	</CaPanel>
</div>

{#if cart.count > 0}
	<div style="margin-top:24px;text-align:right;">
		<a
			href="/order/cart"
			class="ca-btn ca-btn-success ca-btn-lg"
			data-testid="domain-continue-to-cart"
		>
			{t('orderfe.domain.continueToCart')}
			<i class="fas fa-arrow-right" aria-hidden="true"></i>
		</a>
	</div>
{/if}
