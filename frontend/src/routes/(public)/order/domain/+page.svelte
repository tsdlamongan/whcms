<script lang="ts">
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
	style="display:flex;flex-wrap:wrap;gap:8px;margin-bottom:20px;"
	data-sveltekit-keepfocus
	data-testid="domain-search-form"
>
	<input
		type="text"
		name="q"
		class="ca-input"
		style="flex:1 1 220px;max-width:640px;"
		placeholder={t('orderfe.domain.searchPlaceholder')}
		bind:value={query}
		autocomplete="off"
		spellcheck="false"
		data-testid="domain-search-input"
	/>
	<button type="submit" class="ca-btn ca-btn-primary" data-testid="domain-search-submit">
		<i class="fas fa-search" aria-hidden="true"></i>
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
	<section class="ca-card" data-testid="domain-results">
		<div class="ca-card-header">{t('orderfe.domain.resultsFor', { q: data.q })}</div>
		{#each data.results as result, i (result.name)}
			<div
				style="padding:12px 16px;{i > 0 ? 'border-top:1px solid #eee;' : ''}"
				data-testid={`domain-result-${result.name}`}
			>
				<div style="display:flex;flex-wrap:wrap;align-items:center;gap:12px;">
					<span style="min-width:12rem;flex:1;font-weight:600;color:#333;">{result.name}</span>

					{#if result.available}
						<span class="ca-badge ca-badge--success">{t('orderfe.domain.available')}</span>
						{#if result.premium}
							<span class="ca-badge ca-badge--warning">{t('orderfe.domain.premium')}</span>
						{/if}
					{:else}
						<span class="ca-badge ca-badge--danger">{t('orderfe.domain.taken')}</span>
					{/if}

					<span style="font-size:0.9rem;color:#333;">
						<MoneyText amount={estimatedTotal(result)} class="font-bold" /><span class="ca-muted"
							>{t('orderfe.domain.perYear')}</span
						>
					</span>

					{#if cart.hasDomain(result.name)}
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

				{#if !cart.hasDomain(result.name)}
					<div
						style="display:flex;flex-wrap:wrap;align-items:center;gap:14px;margin-top:8px;padding-left:0.25rem;"
					>
						<label style="font-size:0.85rem;color:#555;display:flex;align-items:center;gap:6px;">
							{t('orderfe.domain.years')}
							<select
								class="ca-select"
								style="padding:2px 6px;"
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
							<span style="font-size:0.85rem;color:#555;">{t('orderfe.domain.addonsLabel')}:</span>
							{#each data.addons as addon (addon.key)}
								<label
									style="font-size:0.85rem;color:#555;display:flex;align-items:center;gap:4px;"
								>
									<input
										type="checkbox"
										checked={addonsFor(result.name).includes(addon.key)}
										onchange={() => toggleAddon(result.name, addon.key)}
										data-testid={`domain-addon-${result.name}-${addon.key}`}
									/>
									{addon.name} (<MoneyText amount={addon.price} />)
								</label>
							{/each}
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
