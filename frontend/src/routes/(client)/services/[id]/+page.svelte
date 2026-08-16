<script lang="ts">
	import { enhance } from '$app/forms';
	import Alert from '$lib/components/Alert.svelte';
	import Breadcrumb from '$lib/components/Breadcrumb.svelte';
	import DateText from '$lib/components/DateText.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import FormField from '$lib/components/FormField.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import MoneyText from '$lib/components/MoneyText.svelte';
	import SpecConfigurator from '$lib/components/ca/SpecConfigurator.svelte';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import type { BreadcrumbItem, SelectOption } from '$lib/components/types';
	import { t } from '$lib/i18n';
	import { specAmount, type SpecChoice } from '$lib/stores/cart.svelte';
	import { toast } from '$lib/stores/toast.svelte';
	import type { ActionResult } from '@sveltejs/kit';
	import type { PageProps } from './$types';
	import {
		BILLING_CYCLES,
		formatIDR,
		parsePendingUpgrade,
		type BillingCycle,
		type CatalogProduct,
		type Service
	} from '../types';

	let { data, form }: PageProps = $props();

	const service = $derived(data.service);
	const pendingUpgrade = $derived(parsePendingUpgrade(service?.pending_upgrade));

	// action visibility
	const canChangePassword = $derived(service?.status === 'active');
	const canSso = $derived(service?.status === 'active');
	const canUpgrade = $derived(service?.status === 'active' && !pendingUpgrade);
	const canCancel = $derived(
		service !== null && ['pending', 'active', 'suspended'].includes(service.status)
	);
	const hasActions = $derived(canChangePassword || canSso || canUpgrade || canCancel);

	// modal / submit state
	// Initial open state derives from `form` so a no-JS (non-enhanced) POST that
	// failed re-opens the modal and shows the error after the full-page render.
	function failedAction(action: string): boolean {
		return form?.action === action && form.success === false;
	}

	let pwOpen = $state(failedAction('changePassword'));
	let pwSubmitting = $state(false);
	let newPassword = $state('');
	let confirmPassword = $state('');

	let ssoLoading = $state(false);

	let cancelOpen = $state(failedAction('cancel'));
	let cancelSubmitting = $state(false);
	let cancelMode = $state<'immediate' | 'end_of_term'>('end_of_term');

	let upgradeOpen = $state(failedAction('upgrade'));
	let upgradeSubmitting = $state(false);
	let upgradeProductId = $state('');
	let upgradeCycle = $state('');

	// upgrade picker derivations
	const productOptions: SelectOption[] = $derived(
		data.products.map((p) => ({ value: String(p.id), label: p.name }))
	);
	const selectedProduct = $derived(
		data.products.find((p) => String(p.id) === upgradeProductId) ?? null
	);
	const availableCycles: readonly BillingCycle[] = $derived.by(() => {
		if (!selectedProduct) return [];
		const pricing = selectedProduct.pricing;
		if (pricing && pricing.length > 0) {
			return BILLING_CYCLES.filter((c) => pricing.some((pr) => pr.cycle === c));
		}
		return BILLING_CYCLES;
	});
	const cycleOptions: SelectOption[] = $derived(
		availableCycles.map((c) => {
			const pr = selectedProduct?.pricing?.find((x) => x.cycle === c);
			return {
				value: c,
				label: pr
					? `${t(`clientsvc.cycle.${c}`)} — ${formatIDR(pr.price)}`
					: t(`clientsvc.cycle.${c}`)
			};
		})
	);
	const upgradePricing = $derived(
		selectedProduct?.pricing?.find((x) => x.cycle === upgradeCycle) ?? null
	);

	$effect(() => {
		const desired = (availableCycles as readonly string[]).includes(upgradeCycle)
			? upgradeCycle
			: (availableCycles[0] ?? '');
		if (desired !== upgradeCycle) upgradeCycle = desired;
	});

	// Dynamic specs (custom-spec / configurable upgrade targets)
	let specChoices = $state<Record<string, SpecChoice>>({});

	/** chosen_specs snapshot from services.panel_meta, keyed for prefill. */
	function currentChosenSpecs(): Record<string, { qty: number; unlimited: boolean }> {
		const meta = service?.panel_meta;
		if (!meta || typeof meta !== 'object') return {};
		const list = (meta as { chosen_specs?: unknown }).chosen_specs;
		if (!Array.isArray(list)) return {};
		const out: Record<string, { qty: number; unlimited: boolean }> = {};
		for (const s of list) {
			if (s && typeof s === 'object' && typeof (s as { key?: unknown }).key === 'string') {
				const row = s as { key: string; qty?: number; unlimited?: boolean };
				out[row.key] = { qty: Number(row.qty ?? 0), unlimited: !!row.unlimited };
			}
		}
		return out;
	}

	/** Prefill: current knobs when resizing the same product, defaults otherwise. */
	function initialSpecChoices(p: CatalogProduct | null): Record<string, SpecChoice> {
		if (!p?.configurable) return {};
		const current = service && p.id === service.product_id ? currentChosenSpecs() : {};
		const out: Record<string, SpecChoice> = {};
		for (const spec of p.specs ?? []) {
			const cur = current[spec.key];
			out[spec.key] = cur
				? { qty: cur.unlimited || cur.qty < 0 ? spec.default_qty : cur.qty, unlimited: cur.unlimited }
				: { qty: spec.default_qty, unlimited: false };
		}
		return out;
	}

	// Reset the knobs whenever the picked product changes.
	let lastSpecProductId = $state('');
	$effect(() => {
		if (upgradeProductId === lastSpecProductId) return;
		lastSpecProductId = upgradeProductId;
		specChoices = initialSpecChoices(selectedProduct);
	});

	const selectedSpecs = $derived(
		selectedProduct?.configurable ? (selectedProduct.specs ?? []) : []
	);
	const specsTotal = $derived(
		selectedSpecs.reduce(
			(sum, s) =>
				sum + specAmount(s, specChoices[s.key], (upgradeCycle || null) as BillingCycle | null),
			0
		)
	);
	/** Estimated new recurring price: base + spec charges (specs are 0 for flat products). */
	const upgradeTotalPrice = $derived((upgradePricing?.price ?? 0) + specsTotal);
	/** [{key, qty, unlimited}] for the hidden form field; server re-validates. */
	const specsPayload = $derived(
		JSON.stringify(
			selectedSpecs.map((s) => {
				const c = specChoices[s.key] ?? { qty: s.default_qty, unlimited: false };
				return { key: s.key, qty: c.unlimited ? 0 : c.qty, unlimited: !!c.unlimited };
			})
		)
	);

	// helpers
	function productLabel(s: Service): string {
		return s.product_name ?? t('clientsvc.list.productFallback', { id: s.product_id });
	}

	function errorFor(action: string): string | null {
		if (!form || form.action !== action || form.success) return null;
		return form.errorKey
			? t(form.errorKey)
			: (form.errorMessage ?? t('clientsvc.errors.actionFailed'));
	}

	function failureMessage(result: ActionResult): string {
		if (result.type === 'failure') {
			const d = result.data as Record<string, unknown> | undefined;
			if (typeof d?.errorKey === 'string') return t(d.errorKey);
			if (typeof d?.errorMessage === 'string') return d.errorMessage;
		}
		return t('clientsvc.errors.actionFailed');
	}

	const breadcrumbs: BreadcrumbItem[] = $derived([
		{ label: t('nav.home'), href: '/dashboard' },
		{ label: t('nav.services'), href: '/services' },
		{ label: service ? service.domain || productLabel(service) : t('clientsvc.detail.title') }
	]);

	// button styles (native buttons so data-testid can be attached)
	const btnPrimary = 'ca-btn ca-btn-primary';
	const btnSecondary = 'ca-btn ca-btn-default';
	const btnDanger = 'ca-btn ca-btn-danger';
</script>

{#snippet spinner()}
	<svg class="h-4 w-4 animate-spin" viewBox="0 0 24 24" fill="none" aria-hidden="true">
		<circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
		<path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 0 1 8-8v4a4 4 0 0 0-4 4H4z" />
	</svg>
{/snippet}

<svelte:head>
	<title>{service ? productLabel(service) : t('clientsvc.detail.title')} — WHCMS</title>
</svelte:head>

<Breadcrumb items={breadcrumbs} />

{#if data.notFound}
	<div class="ca-card">
		<EmptyState
			title={t('clientsvc.detail.notFound')}
			description={t('clientsvc.detail.notFoundDescription')}
		>
			{#snippet action()}
				<a href="/services" class={btnPrimary} data-testid="service-back-to-list">
					{t('clientsvc.detail.backToList')}
				</a>
			{/snippet}
		</EmptyState>
	</div>
{:else if !service}
	<div class="mb-4">
		<Alert type="error" title={t('toast.error')}>
			{data.loadError ?? t('clientsvc.errors.actionFailed')}
		</Alert>
	</div>
	<a href="/services" class={btnSecondary} data-testid="service-back-to-list">
		{t('clientsvc.detail.backToList')}
	</a>
{:else}
	<div class="mb-5 flex flex-wrap items-center justify-between gap-3">
		<div>
			<h1 class="ca-h1" style="margin-bottom:2px" data-testid="service-product">
				{productLabel(service)}
			</h1>
			{#if service.domain}
				<p class="ca-muted font-mono" data-testid="service-domain">
					{service.domain}
				</p>
			{/if}
		</div>
		<span data-testid="service-status"><StatusBadge status={service.status} /></span>
	</div>

	{#if service.status === 'suspended'}
		<div class="mb-4">
			<Alert type="warning" title={t('clientsvc.detail.suspendedTitle')}>
				{#if service.suspend_reason}
					{t('clientsvc.detail.suspendedReason', { reason: service.suspend_reason })}
				{:else}
					{t('clientsvc.detail.renewalUnpaidBody')}
				{/if}
			</Alert>
		</div>
	{/if}

	{#if pendingUpgrade}
		<div class="mb-4">
			<Alert type="info" title={t('clientsvc.detail.pendingUpgradeTitle')}>
				{t('clientsvc.detail.pendingUpgradeBody')}
				<a
					href={`/billing/invoices/${pendingUpgrade.invoice_id}`}
					class="ml-1 font-semibold underline"
					data-testid="service-upgrade-invoice-link"
				>
					{t('clientsvc.detail.payUpgradeInvoice')}
				</a>
			</Alert>
		</div>
	{/if}

	{#if service.renewal_invoice_id}
		<div class="mb-4">
			<Alert type="warning" title={t('clientsvc.detail.renewalUnpaidTitle')}>
				{t('clientsvc.detail.renewalUnpaidBody')}
				<a
					href={`/billing/invoices/${service.renewal_invoice_id}`}
					class="ml-1 font-semibold underline"
					data-testid="service-renewal-invoice-link"
				>
					{t('clientsvc.detail.payRenewalInvoice')}
				</a>
			</Alert>
		</div>
	{/if}

	{#if form?.action === 'sso' && form.success && form.ssoUrl}
		<div class="mb-4">
			<Alert type="info" title={t('clientsvc.actions.ssoReadyTitle')}>
				{t('clientsvc.actions.ssoReadyBody')}
				<a
					href={form.ssoUrl}
					target="_blank"
					rel="noopener noreferrer"
					class="ml-1 font-semibold underline"
					data-testid="service-sso-link"
				>
					{t('clientsvc.actions.ssoOpen')}
				</a>
			</Alert>
		</div>
	{/if}

	{#if errorFor('sso')}
		<div class="mb-4">
			<Alert type="error" title={t('toast.error')}>{errorFor('sso')}</Alert>
		</div>
	{/if}

	<!-- Overview card -->
	<section class="ca-card" data-testid="service-overview">
		<h2 class="ca-card-header">
			{t('clientsvc.detail.overview')}
		</h2>
		<dl class="ca-card-body grid grid-cols-1 gap-x-8 gap-y-4 text-sm sm:grid-cols-2">
			<div>
				<dt class="text-xs font-semibold tracking-wide text-gray-500 uppercase">
					{t('clientsvc.detail.product')}
				</dt>
				<dd class="mt-0.5 text-gray-800">{productLabel(service)}</dd>
			</div>
			<div>
				<dt class="text-xs font-semibold tracking-wide text-gray-500 uppercase">
					{t('clientsvc.detail.domain')}
				</dt>
				<dd class="mt-0.5 font-mono text-gray-800">{service.domain || '—'}</dd>
			</div>
			<div>
				<dt class="text-xs font-semibold tracking-wide text-gray-500 uppercase">
					{t('clientsvc.detail.username')}
				</dt>
				<dd class="mt-0.5 font-mono text-gray-800" data-testid="service-username">
					{service.username || '—'}
				</dd>
			</div>
			<div>
				<dt class="text-xs font-semibold tracking-wide text-gray-500 uppercase">
					{t('clientsvc.detail.server')}
				</dt>
				<dd class="mt-0.5 text-gray-800" data-testid="service-server">
					{service.server_hostname ?? (service.server_id ? `#${service.server_id}` : '—')}
				</dd>
			</div>
			<div>
				<dt class="text-xs font-semibold tracking-wide text-gray-500 uppercase">
					{t('clientsvc.detail.registrationDate')}
				</dt>
				<dd class="mt-0.5 text-gray-800">
					<DateText value={service.registration_date} />
				</dd>
			</div>
			<div>
				<dt class="text-xs font-semibold tracking-wide text-gray-500 uppercase">
					{t('clientsvc.detail.nextDueDate')}
				</dt>
				<dd class="mt-0.5 text-gray-800" data-testid="service-next-due">
					<DateText value={service.next_due_date} />
				</dd>
			</div>
			<div>
				<dt class="text-xs font-semibold tracking-wide text-gray-500 uppercase">
					{t('clientsvc.detail.billingCycle')}
				</dt>
				<dd class="mt-0.5 text-gray-800">{t(`clientsvc.cycle.${service.billing_cycle}`)}</dd>
			</div>
			<div>
				<dt class="text-xs font-semibold tracking-wide text-gray-500 uppercase">
					{t('clientsvc.detail.recurringAmount')}
				</dt>
				<dd class="mt-0.5 text-gray-800" data-testid="service-recurring-amount">
					<MoneyText amount={service.recurring_amount} />
					<span class="text-xs text-gray-400"
						>/ {t(`clientsvc.cycle.${service.billing_cycle}`)}</span
					>
				</dd>
			</div>
			{#if service.setup_fee > 0}
				<div>
					<dt class="text-xs font-semibold tracking-wide text-gray-500 uppercase">
						{t('clientsvc.detail.setupFee')}
					</dt>
					<dd class="mt-0.5 text-gray-800"><MoneyText amount={service.setup_fee} /></dd>
				</div>
			{/if}
			{#if service.notes}
				<div class="sm:col-span-2">
					<dt class="text-xs font-semibold tracking-wide text-gray-500 uppercase">
						{t('clientsvc.detail.notes')}
					</dt>
					<dd class="mt-0.5 whitespace-pre-line text-gray-800">{service.notes}</dd>
				</div>
			{/if}
		</dl>
	</section>

	<!-- Actions -->
	<section class="ca-card" data-testid="service-actions">
		<h2 class="ca-card-header">
			{t('clientsvc.detail.actionsTitle')}
		</h2>
		<div class="ca-card-body flex flex-wrap items-center gap-2">
			{#if canSso}
				<form
					method="POST"
					action="?/sso"
					class="inline"
					use:enhance={() => {
						ssoLoading = true;
						return async ({ result, update }) => {
							ssoLoading = false;
							if (result.type === 'success') {
								const url = (result.data as Record<string, unknown> | undefined)?.ssoUrl;
								if (typeof url === 'string' && url) {
									window.open(url, '_blank', 'noopener');
								}
							} else if (result.type === 'failure') {
								toast.error(failureMessage(result));
							}
							await update({ reset: false });
						};
					}}
				>
					<button
						type="submit"
						class={btnPrimary}
						disabled={ssoLoading}
						data-testid="service-sso-button"
					>
						{#if ssoLoading}{@render spinner()}{/if}
						{t('clientsvc.actions.sso')}
					</button>
				</form>
			{/if}

			{#if canChangePassword}
				<button
					type="button"
					class={btnSecondary}
					onclick={() => (pwOpen = true)}
					data-testid="service-change-password-button"
				>
					{t('clientsvc.actions.changePassword')}
				</button>
			{/if}

			{#if canUpgrade}
				<button
					type="button"
					class={btnSecondary}
					onclick={() => (upgradeOpen = true)}
					data-testid="service-upgrade-button"
				>
					{t('clientsvc.actions.upgrade')}
				</button>
			{/if}

			{#if canCancel}
				<button
					type="button"
					class={btnDanger}
					onclick={() => (cancelOpen = true)}
					data-testid="service-cancel-button"
				>
					{t('clientsvc.actions.cancel')}
				</button>
			{/if}

			{#if !hasActions}
				<p class="ca-muted" style="margin:0">{t('clientsvc.detail.noActions')}</p>
			{/if}
		</div>
	</section>

	<!-- Change password modal -->
	<Modal bind:open={pwOpen} title={t('clientsvc.actions.changePasswordTitle')}>
		<form
			method="POST"
			action="?/changePassword"
			data-testid="service-change-password-form"
			use:enhance={() => {
				pwSubmitting = true;
				return async ({ result, update }) => {
					pwSubmitting = false;
					if (result.type === 'success') {
						pwOpen = false;
						newPassword = '';
						confirmPassword = '';
						toast.success(t('clientsvc.actions.passwordChanged'));
					}
					await update({ reset: false });
				};
			}}
		>
			{#if errorFor('changePassword')}
				<div class="mb-4">
					<Alert type="error">{errorFor('changePassword')}</Alert>
				</div>
			{/if}
			<FormField
				label={t('clientsvc.actions.newPassword')}
				name="password"
				type="password"
				bind:value={newPassword}
				hint={t('clientsvc.actions.passwordHint')}
				autocomplete="new-password"
				required
			/>
			<FormField
				label={t('clientsvc.actions.confirmPassword')}
				name="password_confirm"
				type="password"
				bind:value={confirmPassword}
				autocomplete="new-password"
				required
			/>
			<div class="mt-5 flex justify-end gap-2">
				<button
					type="button"
					class={btnSecondary}
					onclick={() => (pwOpen = false)}
					disabled={pwSubmitting}
				>
					{t('action.cancel')}
				</button>
				<button
					type="submit"
					class={btnPrimary}
					disabled={pwSubmitting}
					data-testid="service-change-password-submit"
				>
					{#if pwSubmitting}{@render spinner()}{/if}
					{t('action.save')}
				</button>
			</div>
		</form>
	</Modal>

	<!-- Upgrade modal -->
	<Modal bind:open={upgradeOpen} title={t('clientsvc.actions.upgradeTitle')}>
		<form
			method="POST"
			action="?/upgrade"
			data-testid="service-upgrade-form"
			use:enhance={() => {
				upgradeSubmitting = true;
				return async ({ result, update }) => {
					upgradeSubmitting = false;
					if (result.type === 'redirect') {
						upgradeOpen = false;
					} else if (result.type === 'success') {
						upgradeOpen = false;
						toast.success(t('clientsvc.actions.upgradeRequested'));
					}
					await update({ reset: false });
				};
			}}
		>
			{#if errorFor('upgrade')}
				<div class="mb-4">
					<Alert type="error">{errorFor('upgrade')}</Alert>
				</div>
			{/if}

			{#if data.products.length === 0}
				<div class="mb-4">
					<Alert type="warning">{t('clientsvc.actions.upgradeNoProducts')}</Alert>
				</div>
			{:else}
				<FormField
					label={t('clientsvc.actions.upgradeProduct')}
					name="product_id"
					type="select"
					bind:value={upgradeProductId}
					options={productOptions}
					placeholder={t('clientsvc.actions.upgradeProductPlaceholder')}
					required
				/>
				<FormField
					label={t('clientsvc.actions.upgradeCycle')}
					name="cycle"
					type="select"
					bind:value={upgradeCycle}
					options={cycleOptions}
					disabled={!selectedProduct}
					required
				/>

				{#if selectedSpecs.length > 0}
					<div class="mb-4" data-testid="service-upgrade-specs">
						<SpecConfigurator
							specs={selectedSpecs}
							cycle={(upgradeCycle || null) as BillingCycle | null}
							bind:choices={specChoices}
						/>
					</div>
				{/if}
				<input
					type="hidden"
					name="specs"
					value={selectedProduct?.configurable ? specsPayload : ''}
				/>

				{#if upgradePricing}
					<div class="mb-4 rounded-md border border-gray-200 bg-gray-50 px-4 py-3 text-sm">
						<div class="flex items-center justify-between">
							<span class="text-gray-500">{t('clientsvc.actions.upgradePrice')}</span>
							<span class="font-semibold text-gray-800" data-testid="service-upgrade-price">
								<MoneyText amount={upgradeTotalPrice} />
								<span class="text-xs font-normal text-gray-400">
									/ {t(`clientsvc.cycle.${upgradePricing.cycle}`)}
								</span>
							</span>
						</div>
						{#if upgradePricing.setup_fee > 0}
							<div class="mt-1 flex items-center justify-between">
								<span class="text-gray-500">{t('clientsvc.actions.upgradeSetupFee')}</span>
								<span class="font-semibold text-gray-800">
									<MoneyText amount={upgradePricing.setup_fee} />
								</span>
							</div>
						{/if}
					</div>
				{/if}

				<p class="mb-4 text-xs text-gray-500">{t('clientsvc.actions.upgradeHint')}</p>
			{/if}

			<div class="mt-5 flex justify-end gap-2">
				<button
					type="button"
					class={btnSecondary}
					onclick={() => (upgradeOpen = false)}
					disabled={upgradeSubmitting}
				>
					{t('action.cancel')}
				</button>
				<button
					type="submit"
					class={btnPrimary}
					disabled={upgradeSubmitting || !selectedProduct || !upgradeCycle}
					data-testid="service-upgrade-submit"
				>
					{#if upgradeSubmitting}{@render spinner()}{/if}
					{t('clientsvc.actions.upgradeSubmit')}
				</button>
			</div>
		</form>
	</Modal>

	<!-- Cancel modal -->
	<Modal bind:open={cancelOpen} title={t('clientsvc.actions.cancelTitle')}>
		<form
			method="POST"
			action="?/cancel"
			data-testid="service-cancel-form"
			use:enhance={() => {
				cancelSubmitting = true;
				return async ({ result, update }) => {
					cancelSubmitting = false;
					if (result.type === 'success') {
						cancelOpen = false;
						toast.success(t('clientsvc.actions.cancelSuccess'));
					}
					await update({ reset: false });
				};
			}}
		>
			{#if errorFor('cancel')}
				<div class="mb-4">
					<Alert type="error">{errorFor('cancel')}</Alert>
				</div>
			{/if}

			<fieldset class="mb-4">
				<legend class="mb-2 block text-sm font-medium text-gray-700">
					{t('clientsvc.actions.cancelMode')}
				</legend>
				<div class="space-y-3">
					<label class="flex cursor-pointer items-start gap-2 text-sm text-gray-700">
						<input
							type="radio"
							name="mode"
							value="end_of_term"
							bind:group={cancelMode}
							class="mt-0.5 h-4 w-4 accent-primary"
							data-testid="service-cancel-mode-end-of-term"
						/>
						<span>
							<span class="font-medium">{t('clientsvc.actions.cancelEndOfTerm')}</span>
							<br />
							<span class="text-xs text-gray-500">{t('clientsvc.actions.cancelEndOfTermHint')}</span
							>
						</span>
					</label>
					<label class="flex cursor-pointer items-start gap-2 text-sm text-gray-700">
						<input
							type="radio"
							name="mode"
							value="immediate"
							bind:group={cancelMode}
							class="mt-0.5 h-4 w-4 accent-primary"
							data-testid="service-cancel-mode-immediate"
						/>
						<span>
							<span class="font-medium">{t('clientsvc.actions.cancelImmediate')}</span>
							<br />
							<span class="text-xs text-gray-500">{t('clientsvc.actions.cancelImmediateHint')}</span
							>
						</span>
					</label>
				</div>
			</fieldset>

			<div class="mt-5 flex justify-end gap-2">
				<button
					type="button"
					class={btnSecondary}
					onclick={() => (cancelOpen = false)}
					disabled={cancelSubmitting}
				>
					{t('action.close')}
				</button>
				<button
					type="submit"
					class={btnDanger}
					disabled={cancelSubmitting}
					data-testid="service-cancel-submit"
				>
					{#if cancelSubmitting}{@render spinner()}{/if}
					{t('clientsvc.actions.cancelSubmit')}
				</button>
			</div>
		</form>
	</Modal>
{/if}
