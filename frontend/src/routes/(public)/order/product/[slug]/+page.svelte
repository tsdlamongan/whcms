<script lang="ts">
	import { appName } from '$lib/appName';
	import { goto } from '$app/navigation';
	import { Alert, Breadcrumb, EmptyState, MoneyText } from '$lib/components';
	import CaPanel from '$lib/components/ca/CaPanel.svelte';
	import CaPrice from '$lib/components/ca/CaPrice.svelte';
	import SpecConfigurator from '$lib/components/ca/SpecConfigurator.svelte';
	import { t } from '$lib/i18n';
	import { formatIDR } from '$lib/money';
	import {
		CYCLE_ORDER,
		cart,
		isValidDomain,
		resolveSpecSelections,
		type BillingCycle,
		type CartOptionLabel,
		type SpecChoice
	} from '$lib/stores/cart.svelte';
	import { toast } from '$lib/stores/toast.svelte';
	import type { OptionRow, OptionValueRow } from './+page.server';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	interface CheckResult {
		name: string;
		available: boolean;
		premium: boolean;
		price: number;
		/** Full year-1..10 register price matrix - present only when `price`
		 *  came from an active TLDPricing row (not a flat annual rate; a
		 *  multi-year estimate must look this up instead of assuming
		 *  price * years). */
		register_prices?: Record<string, number>;
		min_years: number;
		max_years: number;
	}

	const product = $derived(data.product);
	const pricing = $derived(
		(data.product?.pricing ?? [])
			.slice()
			.sort((a, b) => CYCLE_ORDER.indexOf(a.cycle) - CYCLE_ORDER.indexOf(b.cycle))
	);
	const optionGroups = $derived(data.product?.option_groups ?? []);
	const specs = $derived(data.product?.specs ?? []);
	const showDomain = $derived(product !== null && product.type !== 'other');

	// Billing cycle
	let chosenCycle = $state<BillingCycle | null>(null);
	const selectedCycle = $derived(
		chosenCycle && pricing.some((p) => p.cycle === chosenCycle)
			? chosenCycle
			: (pricing[0]?.cycle ?? null)
	);
	const selectedPricing = $derived(pricing.find((p) => p.cycle === selectedCycle) ?? null);

	// Configurable options
	let optionChoices = $state<Record<number, number>>({});

	function selectedValue(option: OptionRow): OptionValueRow | null {
		return option.values.find((v) => v.id === optionChoices[option.id]) ?? option.values[0] ?? null;
	}

	function deltaFor(value: OptionValueRow | null): number {
		if (!value || !selectedCycle) return 0;
		return value.price_deltas?.[selectedCycle] ?? 0;
	}

	const allOptions = $derived(optionGroups.flatMap((g) => g.options ?? []));
	const optionLabels = $derived(
		allOptions
			.map((opt): CartOptionLabel | null => {
				const value = selectedValue(opt);
				return value ? { name: opt.name, value: value.name, delta: deltaFor(value) } : null;
			})
			.filter((l): l is CartOptionLabel => l !== null)
	);
	const optionsDeltaTotal = $derived(optionLabels.reduce((sum, l) => sum + l.delta, 0));

	// Dynamic specs (custom products)
	// choices is the single source of truth (shared with SpecConfigurator via
	// bind:choices); specSelections is a pure derivation so the estimate and the
	// add-to-cart payload never diverge.
	let specChoices = $state<Record<string, SpecChoice>>({});
	const specSelections = $derived(resolveSpecSelections(specs, specChoices, selectedCycle));
	const specsTotal = $derived(specSelections.reduce((sum, s) => sum + (s.amount || 0), 0));

	// Domain
	type DomainMode = 'register' | 'transfer' | 'own';
	let domainMode = $state<DomainMode>('register');
	let domainName = $state('');
	let eppCode = $state('');

	let checkState = $state<'idle' | 'checking' | 'done' | 'error'>('idle');
	let checkResult = $state<CheckResult | null>(null);
	let checkTimer: ReturnType<typeof setTimeout> | undefined;
	let checkSeq = 0;

	const cleanDomain = $derived(domainName.trim().toLowerCase());

	// Domain years + addons
	let domainYears = $state(1);
	let selectedDomainAddons = $state<string[]>([]);
	$effect(() => {
		// Re-clamp whenever a fresh check result changes the TLD's bounds.
		if (checkResult) {
			domainYears = Math.min(Math.max(domainYears, checkResult.min_years), checkResult.max_years);
		}
	});
	function toggleDomainAddon(key: string) {
		selectedDomainAddons = selectedDomainAddons.includes(key)
			? selectedDomainAddons.filter((k) => k !== key)
			: [...selectedDomainAddons, key];
	}
	const domainAddonsTotal = $derived(
		(data.addons ?? [])
			.filter((a) => selectedDomainAddons.includes(a.key))
			.reduce((sum, a) => sum + a.price, 0)
	);
	const domainAddonLabels = $derived(
		(data.addons ?? [])
			.filter((a) => selectedDomainAddons.includes(a.key))
			.map((a) => ({ name: a.name, price: a.price }))
	);

	function scheduleCheck() {
		if (checkTimer) clearTimeout(checkTimer);
		checkResult = null;
		checkSeq++;
		if (domainMode === 'own' || !isValidDomain(cleanDomain)) {
			checkState = 'idle';
			return;
		}
		checkState = 'checking';
		const name = cleanDomain;
		checkTimer = setTimeout(() => void runCheck(name), 500);
	}

	async function runCheck(name: string) {
		const seq = ++checkSeq;
		try {
			const res = await fetch('/order/domain-check', {
				method: 'POST',
				headers: { 'content-type': 'application/json' },
				body: JSON.stringify({ names: [name] })
			});
			const body = (await res.json()) as { results: CheckResult[] | null };
			if (seq !== checkSeq) return; // stale response
			if (!res.ok || !Array.isArray(body.results)) {
				checkState = 'error';
				return;
			}
			checkResult = body.results.find((r) => r.name === name) ?? body.results[0] ?? null;
			checkState = 'done';
		} catch {
			if (seq === checkSeq) checkState = 'error';
		}
	}

	$effect(() => () => clearTimeout(checkTimer));

	// Summary + add to cart
	/** Register price for a given term length: the real per-year matrix when
	 *  the TLD has one (never assume linear - e.g. a free-first-year promo
	 *  makes year 1 != year N / N), else the flat-rate price*years estimate
	 *  (premium/length-tier overrides and the live-registrar-quote fallback
	 *  ARE flat annual rates, so that's accurate for those). Transfers always
	 *  use the flat estimate - the registrar's transfer price isn't part of
	 *  this check response at all. */
	function registerBasePrice(result: CheckResult, yrs: number): number {
		const perYear = result.register_prices?.[String(yrs)];
		return perYear !== undefined ? perYear : result.price * yrs;
	}
	const domainPrice = $derived(
		showDomain && domainMode !== 'own' && checkState === 'done' && checkResult
			? (domainMode === 'register'
					? registerBasePrice(checkResult, domainYears)
					: checkResult.price * domainYears) +
					domainAddonsTotal * domainYears
			: 0
	);
	const estimatedTotal = $derived(
		(selectedPricing?.price ?? 0) +
			optionsDeltaTotal +
			specsTotal +
			(selectedPricing?.setup_fee ?? 0) +
			(domainMode === 'register' || domainMode === 'transfer' ? domainPrice : 0)
	);

	let addError = $state<string | null>(null);

	function optionsPayload(): Record<string, number> {
		const out: Record<string, number> = {};
		for (const opt of allOptions) {
			const value = selectedValue(opt);
			if (value) out[String(opt.id)] = value.id;
		}
		return out;
	}

	function addToCart() {
		addError = null;
		if (!product || !selectedCycle || !selectedPricing) return;

		if (showDomain) {
			if (!isValidDomain(cleanDomain)) {
				addError = t('orderfe.product.domainRequired');
				return;
			}
			if (domainMode === 'register' && !(checkState === 'done' && checkResult?.available)) {
				addError = t('orderfe.product.registerNotAvailable');
				return;
			}
		}

		cart.add({
			item_type: 'product',
			product_id: product.id,
			product_name: product.name,
			product_slug: product.slug,
			domain: showDomain ? cleanDomain : '',
			cycle: selectedCycle,
			unit_price: selectedPricing.price,
			setup_fee: selectedPricing.setup_fee,
			options: optionsPayload(),
			option_labels: optionLabels,
			specs: specSelections.length > 0 ? specSelections : undefined
		});

		if (showDomain && domainMode === 'register') {
			cart.add({
				item_type: 'domain_register',
				domain: cleanDomain,
				cycle: 'annually',
				unit_price: domainPrice,
				setup_fee: 0,
				domain_years: domainYears,
				domain_addons: selectedDomainAddons,
				domain_addon_labels: domainAddonLabels
			});
		} else if (showDomain && domainMode === 'transfer') {
			cart.add({
				item_type: 'domain_transfer',
				domain: cleanDomain,
				cycle: 'annually',
				unit_price: domainPrice,
				setup_fee: 0,
				epp_code: eppCode.trim() || undefined,
				domain_years: domainYears,
				domain_addons: selectedDomainAddons,
				domain_addon_labels: domainAddonLabels
			});
		}

		toast.success(t('orderfe.product.added'));
		void goto('/order/cart');
	}

	// <option> elements can only contain text, so MoneyText cannot be rendered
	// inside them - use the shared formatter for option labels (same "Rp…,-").
</script>

<svelte:head>
	<title>{product ? product.name : t('orderfe.product.configure')} — {appName}</title>
</svelte:head>

<Breadcrumb
	items={[
		{ label: t('orderfe.catalog.title'), href: '/order' },
		{ label: product?.name ?? t('orderfe.product.breadcrumb') }
	]}
/>

{#if data.notFound}
	<section class="ca-card">
		<div class="ca-card-body">
			<EmptyState
				title={t('orderfe.product.notFound')}
				description={t('orderfe.product.notFoundDesc')}
			>
				{#snippet action()}
					<a class="ca-btn ca-btn-primary" href="/order">
						{t('orderfe.product.backToCatalog')}
					</a>
				{/snippet}
			</EmptyState>
		</div>
	</section>
{:else if data.loadError || !product}
	<Alert type="error" title={t('orderfe.product.loadError')}>{data.loadError}</Alert>
{:else}
	<h1 class="ca-h1" data-testid="product-title">{product.name}</h1>
	{#if product.description}
		<p class="ca-lead" style="max-width:48rem;white-space:pre-line;">{product.description}</p>
	{/if}

	<div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
		<div class="space-y-6 lg:col-span-2">
			<CaPanel title={t('orderfe.product.billingCycle')}>
				{#if pricing.length === 0}
					<Alert type="warning">{t('orderfe.product.noPricing')}</Alert>
				{:else}
					<div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
						{#each pricing as row (row.id)}
							<label
								class="ca-check"
								style="justify-content:space-between;width:100%;gap:12px;padding:10px 14px;border:1px solid {selectedCycle ===
								row.cycle
									? 'var(--ca-primary)'
									: '#ced4da'};border-radius:var(--ca-radius);background:{selectedCycle ===
								row.cycle
									? '#eef4fa'
									: '#fff'};"
							>
								<span style="display:inline-flex;align-items:center;gap:9px;">
									<input
										type="radio"
										name="cycle"
										value={row.cycle}
										checked={selectedCycle === row.cycle}
										onchange={() => (chosenCycle = row.cycle)}
										data-testid={`product-cycle-${row.cycle}`}
									/>
									<span style="font-weight:600;color:#333;">
										{t(`orderfe.cycle.${row.cycle}`)}
									</span>
								</span>
								<span style="text-align:right;">
									<MoneyText amount={row.price} class="font-bold" />
									{#if row.setup_fee > 0}
										<span class="ca-muted" style="display:block;font-size:0.75rem;">
											+ <MoneyText amount={row.setup_fee} />
											{t('orderfe.product.setupFee').toLowerCase()}
										</span>
									{/if}
								</span>
							</label>
						{/each}
					</div>
				{/if}
			</CaPanel>

			{#if allOptions.length > 0}
				<CaPanel title={t('orderfe.product.options')}>
					<div class="space-y-4">
						{#each optionGroups as group (group.id)}
							{#if (group.options ?? []).length > 0}
								<div>
									{#if optionGroups.length > 1}
										<p class="ca-label">{group.name}</p>
									{/if}
									<div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
										{#each group.options as option (option.id)}
											{@const current = selectedValue(option)}
											<div class="ca-formrow" style="margin-bottom:0;">
												<label class="ca-label" for={`option-${option.id}`}>
													{option.name}
												</label>
												<select
													id={`option-${option.id}`}
													class="ca-select"
													value={current ? String(current.id) : ''}
													onchange={(e) =>
														(optionChoices = {
															...optionChoices,
															[option.id]: Number(e.currentTarget.value)
														})}
													data-testid={`option-select-${option.id}`}
												>
													{#each option.values as value (value.id)}
														{@const delta = deltaFor(value)}
														<option value={String(value.id)}>
															{value.name}{delta !== 0
																? ` (${delta > 0 ? '+' : '−'}${formatIDR(Math.abs(delta))})`
																: ''}
														</option>
													{/each}
												</select>
											</div>
										{/each}
									</div>
								</div>
							{/if}
						{/each}
					</div>
				</CaPanel>
			{/if}

			<!-- Dynamic specs (custom products) -->
			<SpecConfigurator {specs} cycle={selectedCycle} bind:choices={specChoices} />

			{#if showDomain}
				<CaPanel title={t('orderfe.product.domainSection')}>
					<div class="ca-formrow" style="display:flex;flex-direction:column;gap:8px;">
						{#each [{ mode: 'register' as const, label: t('orderfe.product.domainModeRegister') }, { mode: 'transfer' as const, label: t('orderfe.product.domainModeTransfer') }, { mode: 'own' as const, label: t('orderfe.product.domainModeOwn') }] as entry (entry.mode)}
							<label class="ca-check">
								<input
									type="radio"
									name="domain_mode"
									value={entry.mode}
									checked={domainMode === entry.mode}
									onchange={() => {
										domainMode = entry.mode;
										scheduleCheck();
									}}
									data-testid={`domain-mode-${entry.mode}`}
								/>
								<span>{entry.label}</span>
							</label>
						{/each}
					</div>

					<label class="ca-label" for="domain-input">
						{t('orderfe.product.domainLabel')}
					</label>
					<input
						id="domain-input"
						type="text"
						class="ca-input"
						placeholder={t('orderfe.product.domainPlaceholder')}
						bind:value={domainName}
						oninput={scheduleCheck}
						autocomplete="off"
						spellcheck="false"
						data-testid="domain-search-input"
					/>

					{#if domainMode !== 'own'}
						<div class="mt-2 min-h-5 text-sm" data-testid="domain-check-status">
							{#if checkState === 'checking'}
								<span class="ca-muted">{t('orderfe.product.checking')}</span>
							{:else if checkState === 'error'}
								<span style="color:var(--ca-danger,#c43c35);"
									>{t('orderfe.product.checkFailed')}</span
								>
							{:else if checkState === 'done' && checkResult}
								{#if checkResult.available}
									<span style="color:var(--ca-success);font-weight:600;">
										{t('orderfe.product.available', { domain: checkResult.name })}
										{#if checkResult.premium}
											<span class="ca-badge ca-badge--warning" style="margin-left:4px;">
												{t('orderfe.product.premium')}
											</span>
										{/if}
										— <MoneyText amount={checkResult.price} />{t('orderfe.product.perYear')}
									</span>
								{:else}
									<span
										style="font-weight:600;color:{domainMode === 'register'
											? '#c43c35'
											: '#6c757d'};"
									>
										{t('orderfe.product.unavailable', { domain: checkResult.name })}
									</span>
								{/if}
							{/if}
						</div>
					{/if}

					{#if domainMode !== 'own' && checkState === 'done' && checkResult}
						{@const cr = checkResult}
						<div style="display:flex;flex-wrap:wrap;align-items:center;gap:14px;margin-top:10px;">
							<label style="font-size:0.85rem;color:#555;display:flex;align-items:center;gap:6px;">
								{t('orderfe.domain.years')}
								<select
									class="ca-select"
									style="padding:2px 6px;"
									bind:value={domainYears}
									data-testid="domain-years-select"
								>
									{#each Array.from({ length: cr.max_years - cr.min_years + 1 }, (_, n) => cr.min_years + n) as y (y)}
										<option value={y}>{y}{t('orderfe.domain.yearSuffix')}</option>
									{/each}
								</select>
							</label>

							{#if (data.addons ?? []).length > 0}
								<span style="font-size:0.85rem;color:#555;">{t('orderfe.domain.addonsLabel')}:</span
								>
								{#each data.addons as addon (addon.key)}
									<label
										style="font-size:0.85rem;color:#555;display:flex;align-items:center;gap:4px;"
									>
										<input
											type="checkbox"
											checked={selectedDomainAddons.includes(addon.key)}
											onchange={() => toggleDomainAddon(addon.key)}
											data-testid={`domain-addon-${addon.key}`}
										/>
										{addon.name} (<MoneyText amount={addon.price} />)
									</label>
								{/each}
							{/if}
						</div>
					{/if}

					{#if domainMode === 'transfer'}
						<div class="ca-formrow" style="margin-top:12px;margin-bottom:0;">
							<label class="ca-label" for="epp-input">
								{t('orderfe.product.eppLabel')}
							</label>
							<input
								id="epp-input"
								type="text"
								class="ca-input"
								bind:value={eppCode}
								autocomplete="off"
								data-testid="epp-input"
							/>
							<p class="ca-muted" style="margin-top:5px;font-size:0.8rem;">
								{t('orderfe.product.eppHint')}
							</p>
						</div>
					{/if}
				</CaPanel>
			{/if}
		</div>

		<div>
			<div class="ca-order-summary" style="position:sticky;top:24px;">
				<div class="ca-order-summary-head">{t('orderfe.product.summary')}</div>
				<div class="ca-order-summary-body">
					<div class="ca-summary-row">
						<span>
							{t('orderfe.product.priceLabel')}
							{#if selectedCycle}
								<span class="ca-muted" style="display:block;font-size:0.78rem;">
									{t(`orderfe.cycle.${selectedCycle}`)}
								</span>
							{/if}
						</span>
						<MoneyText amount={selectedPricing?.price ?? 0} class="font-medium" />
					</div>

					{#each optionLabels.filter((l) => l.delta !== 0) as label (label.name)}
						<div class="ca-summary-row">
							<span>
								{label.name}
								<span class="ca-muted" style="display:block;font-size:0.78rem;">{label.value}</span>
							</span>
							<MoneyText amount={label.delta} class="font-medium" />
						</div>
					{/each}

					{#each specSelections.filter((s) => s.amount !== 0 || s.unlimited) as spec (spec.key)}
						<div class="ca-summary-row">
							<span>
								{spec.label || spec.key}
								<span class="ca-muted" style="display:block;font-size:0.78rem;">
									{spec.unlimited
										? t('orderfe.product.specUnlimited')
										: `${spec.qty}${spec.unit === 'gb' ? 'GB' : spec.unit === 'mb' ? 'MB' : ''}`}
								</span>
							</span>
							<MoneyText amount={spec.amount} class="font-medium" />
						</div>
					{/each}

					{#if (selectedPricing?.setup_fee ?? 0) > 0}
						<div class="ca-summary-row">
							<span>{t('orderfe.product.setupFee')}</span>
							<MoneyText amount={selectedPricing?.setup_fee ?? 0} class="font-medium" />
						</div>
					{/if}

					{#if showDomain && domainMode === 'register' && checkResult?.available}
						<div class="ca-summary-row">
							<span>
								{t('orderfe.product.domainRegistration')}
								<span class="ca-muted" style="display:block;font-size:0.78rem;">{cleanDomain}</span>
							</span>
							<MoneyText amount={domainPrice} class="font-medium" />
						</div>
					{:else if showDomain && domainMode === 'transfer' && cleanDomain}
						<div class="ca-summary-row">
							<span>
								{t('orderfe.product.domainTransfer')}
								<span class="ca-muted" style="display:block;font-size:0.78rem;">{cleanDomain}</span>
							</span>
							<MoneyText amount={domainPrice} class="font-medium" />
						</div>
					{/if}

					<div class="ca-summary-total">
						<div class="amount" data-testid="spec-estimate-total">
							<CaPrice amount={estimatedTotal} />
						</div>
						<div class="label">{t('orderfe.product.estimatedTotal')}</div>
					</div>
					<p class="ca-muted" style="text-align:center;font-size:0.78rem;margin:0 0 6px;">
						{t('orderfe.product.taxNote')}
					</p>

					{#if addError}
						<div class="ca-alert-danger" role="alert" data-testid="add-to-cart-error">
							{addError}
						</div>
					{/if}

					<button
						type="button"
						class="ca-btn ca-btn-success ca-btn-block ca-btn-lg ca-checkout"
						disabled={pricing.length === 0}
						onclick={addToCart}
						data-testid="add-to-cart"
					>
						<i class="fas fa-shopping-cart" aria-hidden="true"></i>
						{t('orderfe.product.addToCart')}
					</button>
				</div>
			</div>
		</div>
	</div>
{/if}
