<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import Alert from '$lib/components/Alert.svelte';
	import Breadcrumb from '$lib/components/Breadcrumb.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import MoneyText from '$lib/components/MoneyText.svelte';
	import { t } from '$lib/i18n';
	import { formatIDR } from '$lib/money';
	import { untrack } from 'svelte';
	import type { PageProps } from './$types';
	import type { SnipeQuote } from '../shared';

	let { data, form }: PageProps = $props();

	let domain = $state(untrack(() => form?.domain ?? ''));
	let planId = $state<number>(untrack(() => form?.planId || data.plans[0]?.id || 0));
	let years = $state<number>(untrack(() => form?.years || 1));
	let useWindow = $state(untrack(() => form?.useWindow ?? false));
	let huntFrom = $state(untrack(() => form?.huntFrom ?? ''));
	let huntUntil = $state(untrack(() => form?.huntUntil ?? ''));
	let showNs = $state(untrack(() => (form?.nameservers?.length ?? 0) > 0));
	let ns = $state<string[]>(
		untrack(() => [0, 1, 2, 3].map((i) => (form?.nameservers as string[] | undefined)?.[i] ?? ''))
	);
	let submitting = $state(false);

	let quote = $state<SnipeQuote | null>(null);
	let quoteLoading = $state(false);
	let quoteError = $state('');

	// Live price preview, debounced: the BFF endpoint forwards to /snipe/quote.
	$effect(() => {
		const d = domain.trim();
		const p = planId;
		const y = years;
		if (!d || !p) {
			quote = null;
			quoteError = '';
			quoteLoading = false;
			return;
		}
		quoteLoading = true;
		const ctrl = new AbortController();
		const timer = setTimeout(async () => {
			try {
				const res = await fetch('/snipes/quote', {
					method: 'POST',
					headers: { 'content-type': 'application/json' },
					body: JSON.stringify({ domain: d, plan_id: p, years: y }),
					signal: ctrl.signal
				});
				const body = (await res.json()) as { quote?: SnipeQuote; error?: string };
				if (res.ok && body.quote) {
					quote = body.quote;
					quoteError = '';
				} else {
					quote = null;
					quoteError = body.error ?? '';
				}
			} catch {
				if (!ctrl.signal.aborted) {
					quote = null;
					quoteError = '';
				}
			} finally {
				if (!ctrl.signal.aborted) quoteLoading = false;
			}
		}, 400);
		return () => {
			clearTimeout(timer);
			ctrl.abort();
		};
	});

	const submitError = $derived.by(() => {
		if (!form?.errorMessage) return '';
		if (form.errorMessage === 'domain') return t('portal.snipe.new.domainLabel');
		if (form.errorMessage === 'plan') return t('portal.snipe.new.planLabel');
		if (form.errorMessage === 'window') return t('portal.snipe.new.windowHint');
		return form.errorMessage;
	});
</script>

<svelte:head>
	<title>{t('portal.snipe.new.title')} — {appName}</title>
</svelte:head>

<Breadcrumb
	items={[
		{ label: t('nav.home'), href: '/dashboard' },
		{ label: t('portal.snipe.list.title'), href: '/snipes' },
		{ label: t('portal.snipe.new.title') }
	]}
/>

<div class="mx-auto max-w-3xl">
	<h1 class="ca-h1" style="margin-bottom:4px">{t('portal.snipe.new.title')}</h1>
	<p class="ca-muted" style="margin-bottom:20px">{t('portal.snipe.new.subtitle')}</p>

	{#if data.plansError}
		<div class="mb-4"><Alert type="error">{data.plansError}</Alert></div>
	{/if}
	{#if form?.errorMessage}
		<div class="mb-4" data-testid="snipe-create-error">
			<Alert type="error">
				{#if form.serverError}{submitError}{:else}{t('portal.snipe.new.createFailed')}: {submitError}{/if}
			</Alert>
		</div>
	{/if}

	{#if data.plans.length === 0}
		<EmptyState title={t('portal.snipe.new.noPlans')} description="" />
	{:else}
		<form
			method="POST"
			class="ca-card ca-card-body"
			data-testid="snipe-form"
			use:enhance={() => {
				submitting = true;
				return async ({ update }) => {
					submitting = false;
					await update({ reset: false });
				};
			}}
		>
			<div style="margin-bottom:16px">
				<label class="ca-label" for="snipe-domain">{t('portal.snipe.new.domainLabel')}</label>
				<input
					id="snipe-domain"
					name="domain"
					class="ca-input"
					bind:value={domain}
					placeholder={t('portal.snipe.new.domainPlaceholder')}
					autocomplete="off"
					autocapitalize="off"
					data-testid="snipe-domain-input"
					required
				/>
				<p class="ca-muted" style="margin-top:4px;font-size:12px">
					{t('portal.snipe.new.domainHint')}
				</p>
			</div>

			<fieldset style="margin-bottom:16px;border:0;padding:0">
				<legend class="ca-label">{t('portal.snipe.new.planLabel')}</legend>
				{#each data.plans as plan (plan.id)}
					<label
						class="ca-card"
						style="display:flex;gap:10px;align-items:flex-start;padding:10px 12px;margin-bottom:8px;cursor:pointer"
					>
						<input
							type="radio"
							name="plan_id"
							value={plan.id}
							checked={planId === plan.id}
							onchange={() => (planId = plan.id)}
							data-testid={`snipe-plan-${plan.id}`}
						/>
						<span>
							<strong>{plan.name}</strong><br />
							<span class="ca-muted" style="font-size:13px">
								{t('portal.snipe.new.planDetail', {
									days: plan.window_days,
									fee: plan.fee > 0 ? formatIDR(plan.fee) : t('portal.snipe.new.planFree')
								})}{plan.success_fee > 0
									? t('portal.snipe.new.planSuccess', { fee: formatIDR(plan.success_fee) })
									: ''}
							</span>
						</span>
					</label>
				{/each}
			</fieldset>

			<div style="margin-bottom:16px;max-width:260px">
				<label class="ca-label" for="snipe-years">{t('portal.snipe.new.yearsLabel')}</label>
				<select
					id="snipe-years"
					name="years"
					class="ca-select"
					bind:value={years}
					data-testid="snipe-years-select"
				>
					{#each [1, 2, 3, 5, 10] as n (n)}
						<option value={n}>{t('portal.snipe.new.years', { n })}</option>
					{/each}
				</select>
			</div>

			<div style="margin-bottom:16px">
				<label style="display:flex;gap:8px;align-items:center">
					<input
						type="checkbox"
						name="use_window"
						bind:checked={useWindow}
						data-testid="snipe-window-toggle"
					/>
					{t('portal.snipe.new.windowToggle')}
				</label>
				{#if useWindow}
					<p class="ca-muted" style="margin:6px 0;font-size:12px">
						{t('portal.snipe.new.windowHint')}
					</p>
					<div style="display:flex;flex-wrap:wrap;gap:12px">
						<div style="flex:1 1 200px">
							<label class="ca-label" for="snipe-from">{t('portal.snipe.new.windowFrom')}</label>
							<input
								id="snipe-from"
								name="hunt_from"
								type="datetime-local"
								class="ca-input"
								required
								bind:value={huntFrom}
								data-testid="snipe-from-input"
							/>
						</div>
						<div style="flex:1 1 200px">
							<label class="ca-label" for="snipe-until">{t('portal.snipe.new.windowUntil')}</label>
							<input
								id="snipe-until"
								name="hunt_until"
								type="datetime-local"
								class="ca-input"
								required
								bind:value={huntUntil}
								data-testid="snipe-until-input"
							/>
						</div>
					</div>
				{/if}
			</div>

			<div style="margin-bottom:16px">
				<label style="display:flex;gap:8px;align-items:center">
					<input type="checkbox" bind:checked={showNs} data-testid="snipe-ns-toggle" />
					{t('portal.snipe.new.nsToggle')}
				</label>
				{#if showNs}
					<p class="ca-muted" style="margin:6px 0;font-size:12px">{t('portal.snipe.new.nsHint')}</p>
					<div
						style="display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:12px"
					>
						{#each [0, 1, 2, 3] as i (i)}
							<div>
								<label class="ca-label" for={`snipe-ns${i + 1}`}
									>{t('portal.snipe.new.nsLabel', { n: i + 1 })}</label
								>
								<input
									id={`snipe-ns${i + 1}`}
									name={`ns${i + 1}`}
									class="ca-input"
									bind:value={ns[i]}
									data-testid={`snipe-ns${i + 1}-input`}
								/>
							</div>
						{/each}
					</div>
				{/if}
			</div>

			<div
				class="ca-card"
				style="padding:14px 16px;margin-bottom:16px;background:#f8f9fa"
				data-testid="snipe-quote"
			>
				<h2 class="ca-h2" style="margin:0 0 8px;font-size:16px">
					{t('portal.snipe.new.quoteTitle')}
				</h2>
				{#if quote}
					<table style="width:100%;font-size:14px">
						<tbody>
							{#if quote.fee > 0}
								<tr>
									<td>{t('portal.snipe.new.quoteFee')}</td>
									<td style="text-align:right"><MoneyText amount={quote.fee} /></td>
								</tr>
							{/if}
							<tr>
								<td>{t('portal.snipe.new.quoteDomain', { n: quote.years })}</td>
								<td style="text-align:right"><MoneyText amount={quote.domain_price} /></td>
							</tr>
							{#if quote.success_fee > 0}
								<tr>
									<td>{t('portal.snipe.new.quoteSuccess')}</td>
									<td style="text-align:right"><MoneyText amount={quote.success_fee} /></td>
								</tr>
							{/if}
							{#if quote.tax > 0}
								<tr>
									<td>
										{t(
											quote.tax_inclusive
												? 'portal.snipe.new.quoteTaxIncluded'
												: 'portal.snipe.new.quoteTax',
											{
												rate: quote.tax_rate
											}
										)}
									</td>
									<td style="text-align:right"><MoneyText amount={quote.tax} /></td>
								</tr>
							{/if}
							<tr style="font-weight:700;border-top:1px solid #ddd">
								<td>{t('portal.snipe.new.quoteTotal')}</td>
								<td style="text-align:right" data-testid="snipe-quote-total"
									><MoneyText amount={quote.total} /></td
								>
							</tr>
							<tr style="color:#218739">
								<td>{t('portal.snipe.new.quoteRefundable')}</td>
								<td style="text-align:right" data-testid="snipe-quote-refundable">
									<MoneyText amount={quote.refundable_if_lost} />
								</td>
							</tr>
						</tbody>
					</table>
				{:else if quoteLoading}
					<p class="ca-muted" style="margin:0">
						<span class="ca-spinner" aria-hidden="true"></span>
						{t('portal.snipe.new.quoteLoading')}
					</p>
				{:else if quoteError}
					<p style="margin:0;color:#c0392b" data-testid="snipe-quote-error">
						{t('portal.snipe.new.quoteFailed', { message: quoteError })}
					</p>
				{:else}
					<p class="ca-muted" style="margin:0">{t('portal.snipe.new.quoteNeedInput')}</p>
				{/if}
			</div>

			<p class="ca-muted" style="font-size:13px;margin-bottom:16px">
				{t('portal.snipe.new.terms')}
			</p>

			<button
				type="submit"
				class="ca-btn ca-btn-primary"
				disabled={submitting || !domain.trim() || !planId}
				aria-busy={submitting}
				data-testid="snipe-submit"
			>
				{#if submitting}<span class="ca-spinner" aria-hidden="true"></span>{t(
						'portal.snipe.new.submitting'
					)}{:else}{t('portal.snipe.new.submit')}{/if}
			</button>
		</form>
	{/if}
</div>
