<script lang="ts">
	import { appName } from '$lib/appName';
	import { goto, invalidateAll } from '$app/navigation';
	import { Alert } from '$lib/components';
	import { t } from '$lib/i18n';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	let paid = $state(false);
	let invoiceId = $state<number | null>(null);

	const invoiceHref = $derived.by(() => {
		const id = invoiceId ?? data.invoiceId;
		return id ? `/billing/invoices/${id}` : null;
	});

	// Poll the colocated proxy every 4s until the gateway callback settles.
	$effect(() => {
		if (data.missing || paid || !data.merchantOrderId) return;
		const merchantOrderId = data.merchantOrderId;
		const timer = setInterval(async () => {
			try {
				const res = await fetch(
					`/payments/return/status?merchantOrderId=${encodeURIComponent(merchantOrderId)}`
				);
				if (!res.ok) return;
				const body = (await res.json()) as { status?: string | null; invoice_id?: number | null };
				if (body.invoice_id) invoiceId = body.invoice_id;
				if (body.status === 'paid') {
					paid = true;
					clearInterval(timer);
					if (body.invoice_id) {
						await goto(`/billing/invoices/${body.invoice_id}`);
					} else {
						await invalidateAll();
					}
				}
			} catch {
				// Transient network error - try again on the next tick.
			}
		}, 4000);
		return () => clearInterval(timer);
	});
</script>

<svelte:head>
	<title>{t('clientBilling.return.title')} — {appName}</title>
</svelte:head>

<div class="mx-auto max-w-md">
	<h1 class="ca-h1" style="text-align:center">{t('clientBilling.return.title')}</h1>

	<div class="ca-card ca-card-body">
		{#if data.missing}
			<div data-testid="payment-return-missing">
				<Alert type="error">{t('clientBilling.return.missingOrder')}</Alert>
			</div>
			<a href="/billing" class="ca-btn ca-btn-primary ca-btn-block" style="margin-top:20px">
				{t('clientBilling.return.backToBilling')}
			</a>
		{:else if paid}
			<div data-testid="payment-return-paid">
				<Alert type="success">{t('clientBilling.return.paid')}</Alert>
			</div>
			{#if invoiceHref}
				<a
					href={invoiceHref}
					data-testid="payment-return-invoice-link"
					class="ca-btn ca-btn-primary ca-btn-block"
					style="margin-top:20px"
				>
					{t('clientBilling.return.viewInvoice')}
				</a>
			{/if}
		{:else}
			<div
				class="flex flex-col items-center gap-3 py-4 text-center"
				data-testid="payment-return-status"
			>
				<svg
					class="h-10 w-10 animate-spin text-primary"
					viewBox="0 0 24 24"
					fill="none"
					aria-hidden="true"
				>
					<circle
						class="opacity-25"
						cx="12"
						cy="12"
						r="10"
						stroke="currentColor"
						stroke-width="4"
					/>
					<path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 0 1 8-8v4a4 4 0 0 0-4 4H4z" />
				</svg>
				<p class="text-sm font-semibold text-gray-800">{t('clientBilling.return.processing')}</p>
				<p class="ca-muted text-sm">{t('clientBilling.return.processingHint')}</p>
			</div>

			{#if data.errorMessage}
				<div class="mt-2">
					<Alert type="warning">{t('clientBilling.return.failedInfo')}</Alert>
				</div>
			{/if}

			<div class="mt-5 flex flex-col gap-2">
				{#if invoiceHref}
					<a
						href={invoiceHref}
						data-testid="payment-return-invoice-link"
						class="ca-btn ca-btn-primary ca-btn-block"
					>
						{t('clientBilling.return.viewInvoice')}
					</a>
				{/if}
				<a href="/billing" class="ca-btn ca-btn-default ca-btn-block">
					{t('clientBilling.return.backToBilling')}
				</a>
			</div>
		{/if}
	</div>
</div>
