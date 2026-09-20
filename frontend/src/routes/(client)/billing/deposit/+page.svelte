<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import { Alert, Breadcrumb, FormField, LoadingButton, MoneyText } from '$lib/components';
	import { t } from '$lib/i18n';
	import { untrack } from 'svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	let amount = $state<number>(untrack(() => Number(form?.amount) || 50000));
	let submitting = $state(false);

	/** Field-level validation error (shown under the input). */
	const fieldError = $derived(form?.errorKey ? t(form.errorKey) : null);
	/** API-level error (shown as an alert). */
	const apiError = $derived(form?.errorMessage ?? null);
</script>

<svelte:head>
	<title>{t('billing.addFunds')} — {appName}</title>
</svelte:head>

<Breadcrumb
	items={[
		{ label: t('nav.home'), href: '/dashboard' },
		{ label: t('clientBilling.title'), href: '/billing' },
		{ label: t('billing.addFunds') }
	]}
/>

<div class="mx-auto max-w-lg">
	<h1 class="ca-h1" style="margin-bottom:4px">{t('billing.addFunds')}</h1>
	<p class="ca-muted" style="margin-bottom:20px">{t('clientBilling.deposit.description')}</p>

	{#if data.creditBalance !== null}
		<div
			class="ca-card ca-card-body"
			style="display:flex;align-items:center;justify-content:space-between;gap:12px"
		>
			<span class="ca-label" style="margin-bottom:0">{t('billing.creditBalance')}</span>
			<span class="ca-price-amount" style="font-size:22px" data-testid="deposit-current-balance">
				<MoneyText amount={data.creditBalance} />
			</span>
		</div>
	{/if}

	{#if apiError}
		<div class="mb-4">
			<Alert type="error">{apiError}</Alert>
		</div>
	{/if}

	{#if form?.success}
		<div class="mb-4">
			<Alert type="success">
				{t('clientBilling.deposit.created')}
				<a href="/billing" class="font-semibold underline"
					>{t('clientBilling.return.backToBilling')}</a
				>
			</Alert>
		</div>
	{/if}

	<form
		method="POST"
		class="ca-card ca-card-body"
		data-testid="deposit-form"
		use:enhance={() => {
			submitting = true;
			return async ({ update }) => {
				submitting = false;
				await update();
			};
		}}
	>
		<div data-testid="deposit-amount-input">
			<FormField
				label={t('clientBilling.deposit.amountLabel')}
				name="amount"
				type="number"
				bind:value={amount}
				hint={t('clientBilling.deposit.minHint')}
				error={fieldError ?? undefined}
				required
			/>
		</div>

		<div data-testid="deposit-submit">
			<LoadingButton type="submit" loading={submitting} class="w-full">
				{t('clientBilling.deposit.submit')}
			</LoadingButton>
		</div>
	</form>
</div>
