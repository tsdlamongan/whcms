<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import { invalidateAll } from '$app/navigation';
	import {
		Alert,
		Breadcrumb,
		DateText,
		LoadingButton,
		MoneyText,
		StatusBadge,
		toast
	} from '$lib/components';
	import { t } from '$lib/i18n';
	import { formatIDR } from '$lib/money';
	import type { PageProps } from './$types';
	import { findResumablePayment, isPayable, resumedPaymentInstructions } from '../../billing';
	import QRCode from 'qrcode';

	let { data, form }: PageProps = $props();

	const invoice = $derived(data.invoice);
	const payable = $derived(invoice !== null && isPayable(invoice.status));
	const formPayment = $derived(form?.payment ?? null);

	// A pending gateway transaction already exists for this invoice (own VA
	// number/QR/bank instructions) - reload/re-visit resumes THOSE instead of
	// silently starting a new one every time. "Ganti metode pembayaran" is the
	// explicit escape hatch; the old pending row is simply left to expire on
	// its own (existing expiry + reconciliation already handle that).
	let switchingMethod = $state(false);
	const resumedPayment = $derived.by(() => {
		const tx = findResumablePayment(data.transactions ?? [], Date.now());
		return tx ? resumedPaymentInstructions(tx) : null;
	});
	// switchingMethod must win over BOTH sources - otherwise `form.payment`
	// (which SvelteKit keeps around until the next action runs, not just
	// while its originating submission is fresh) would make the "Ganti
	// metode pembayaran" button a permanent no-op after the first payment
	// attempt. Reset it once a genuinely new payment attempt completes, so
	// the freshly-picked method's instructions show immediately.
	const payment = $derived(switchingMethod ? null : (formPayment ?? resumedPayment));
	$effect(() => {
		if (formPayment) switchingMethod = false;
	});

	// Rendered client-side (no network call) from the raw qrString Duitku
	// returns for QRIS channels, so the customer can scan it directly instead
	// of visiting Duitku's hosted page.
	let qrImageUrl = $state('');
	$effect(() => {
		const qrString = payment?.qrString;
		if (!qrString) {
			qrImageUrl = '';
			return;
		}
		let cancelled = false;
		QRCode.toDataURL(qrString, { margin: 1, width: 220 })
			.then((url) => {
				if (!cancelled) qrImageUrl = url;
			})
			.catch(() => {
				if (!cancelled) qrImageUrl = '';
			});
		return () => {
			cancelled = true;
		};
	});

	const creditSufficient = $derived(
		invoice !== null && data.creditBalance !== null && data.creditBalance >= invoice.total
	);
	const hasMethods = $derived((data.methods?.length ?? 0) > 0 || creditSufficient);

	// Duitku returns 20+ channels (every enabled VA bank, e-wallet, retail
	// outlet, paylater...) as one flat list, which reads as a very long
	// scrolling wall of buttons. Surface only the most commonly used ones by
	// default (major VA banks + QRIS) and tuck the rest behind a toggle.
	// Non-Duitku gateways (e.g. manual bank transfer) are never collapsed -
	// this only thins out Duitku's own long channel catalog.
	const POPULAR_DUITKU_CODES = ['BC', 'M2', 'BR', 'I1', 'SP', 'LQ', 'NQ'];
	const manualMethods = $derived((data.methods ?? []).filter((m) => m.gateway === 'manual'));
	const duitkuMethods = $derived((data.methods ?? []).filter((m) => m.gateway !== 'manual'));
	const popularMethods = $derived(
		[...duitkuMethods]
			.filter((m) => POPULAR_DUITKU_CODES.includes(m.code))
			.sort((a, b) => POPULAR_DUITKU_CODES.indexOf(a.code) - POPULAR_DUITKU_CODES.indexOf(b.code))
	);
	const otherMethods = $derived(
		duitkuMethods.filter((m) => !POPULAR_DUITKU_CODES.includes(m.code))
	);

	let selectedMethod = $state('');
	let showMoreMethods = $state(false);
	let paying = $state(false);
	let copiedKey = $state('');

	const payError = $derived(form?.errorKey ? t(form.errorKey) : (form?.errorMessage ?? null));

	async function copyText(text: string, key: string) {
		try {
			await navigator.clipboard.writeText(text);
			copiedKey = key;
			setTimeout(() => {
				if (copiedKey === key) copiedKey = '';
			}, 2000);
		} catch {
			// Clipboard unavailable (http/permission) - the value stays selectable.
		}
	}

	// Poll the colocated status proxy every 4s while the invoice is payable;
	// on paid -> refresh data + success toast.
	$effect(() => {
		if (!invoice || !payable) return;
		const invoiceId = invoice.id;
		const currentStatus = invoice.status;
		const timer = setInterval(async () => {
			try {
				const res = await fetch(`/billing/invoices/${invoiceId}/status`);
				if (!res.ok) return;
				const body = (await res.json()) as { status?: string | null };
				if (body.status && body.status !== currentStatus) {
					if (body.status === 'paid') {
						toast.success(t('clientBilling.detail.paymentReceived'));
					}
					await invalidateAll();
				}
			} catch {
				// Transient network error - try again on the next tick.
			}
		}, 4000);
		return () => clearInterval(timer);
	});
</script>

<svelte:head>
	<title>
		{invoice ? invoice.invoice_number : t('billing.invoice')} — {appName}
	</title>
</svelte:head>

<Breadcrumb
	items={[
		{ label: t('nav.home'), href: '/dashboard' },
		{ label: t('clientBilling.title'), href: '/billing' },
		{ label: invoice ? invoice.invoice_number : t('billing.invoice') }
	]}
/>

{#if data.errorMessage}
	<Alert type="error" title={t('toast.error')}>{data.errorMessage}</Alert>
{:else if !invoice}
	<Alert type="error">{t('clientBilling.detail.notFound')}</Alert>
{:else}
	<!-- Header -->
	<div class="mb-4 flex flex-wrap items-center justify-between gap-3">
		<div class="flex flex-wrap items-center gap-3">
			<h1 class="ca-h1" style="margin-bottom:0" data-testid="invoice-number">
				{invoice.invoice_number}
			</h1>
			<span data-testid="invoice-status">
				<StatusBadge status={invoice.status} />
			</span>
		</div>
		<a
			href={`/billing/invoices/${invoice.id}/pdf`}
			target="_blank"
			rel="noopener"
			data-testid="invoice-pdf-download"
			class="ca-btn ca-btn-default"
		>
			<i class="fas fa-file-pdf" aria-hidden="true"></i>
			{t('billing.downloadPdf')}
		</a>
	</div>

	<!-- Status banner -->
	<div class="mb-5" data-testid="invoice-status-banner">
		{#if invoice.status === 'paid'}
			<Alert type="success">
				{t('clientBilling.detail.paidBanner')}
				{#if invoice.paid_at}
					{t('billing.paidAt')}: <DateText
						value={invoice.paid_at}
						mode="datetime"
						class="font-semibold"
					/>
				{/if}
			</Alert>
		{:else if invoice.status === 'unpaid'}
			<Alert type="warning">
				{t('clientBilling.detail.unpaidBanner')}
				<DateText value={invoice.due_date} class="font-semibold" />
			</Alert>
		{:else if invoice.status === 'overdue'}
			<Alert type="error">
				{t('clientBilling.detail.overdueBanner')}
				<DateText value={invoice.due_date} class="font-semibold" />
			</Alert>
		{:else if invoice.status === 'cancelled'}
			<Alert type="info">{t('clientBilling.detail.cancelledBanner')}</Alert>
		{:else if invoice.status === 'refunded'}
			<Alert type="info">{t('clientBilling.detail.refundedBanner')}</Alert>
		{:else if invoice.status === 'draft'}
			<Alert type="info">{t('clientBilling.detail.draftBanner')}</Alert>
		{/if}
	</div>

	<div class="grid grid-cols-1 gap-5 lg:grid-cols-3">
		<!-- Invoice document -->
		<div class="lg:col-span-2">
			<div class="ca-card" style="overflow:hidden">
				<!-- Invoice head: parties + dates -->
				<div
					class="grid grid-cols-1 gap-4 sm:grid-cols-2"
					style="padding:16px 20px;background:#f7f7f7;border-bottom:1px solid rgba(0,0,0,.125)"
				>
					<div>
						<h2 class="ca-label" style="margin-bottom:6px">
							{t('clientBilling.detail.billedTo')}
						</h2>
						<p class="text-sm font-semibold text-gray-800">{data.user.name}</p>
						<p class="ca-muted text-sm">{data.user.email}</p>
					</div>
					<div class="sm:text-right">
						<h2 class="ca-label" style="margin-bottom:6px">
							{t('clientBilling.detail.invoiceInfo')}
						</h2>
						<dl class="space-y-0.5 text-sm">
							<div class="flex justify-between gap-4 sm:justify-end">
								<dt class="text-gray-500">{t('billing.issuedDate')}:</dt>
								<dd class="text-gray-700"><DateText value={invoice.created_at} /></dd>
							</div>
							<div class="flex justify-between gap-4 sm:justify-end">
								<dt class="text-gray-500">{t('billing.dueDate')}:</dt>
								<dd class="text-gray-700"><DateText value={invoice.due_date} /></dd>
							</div>
							{#if invoice.paid_at}
								<div class="flex justify-between gap-4 sm:justify-end">
									<dt class="text-gray-500">{t('billing.paidAt')}:</dt>
									<dd class="text-gray-700">
										<DateText value={invoice.paid_at} mode="datetime" />
									</dd>
								</div>
							{/if}
						</dl>
					</div>
				</div>

				<!-- Line items -->
				<table class="min-w-full divide-y divide-gray-200 text-sm" data-testid="invoice-items">
					<thead class="bg-gray-50">
						<tr>
							<th
								class="px-5 py-2.5 text-left text-xs font-semibold tracking-wide text-gray-600 uppercase"
							>
								{t('clientBilling.detail.itemDescription')}
							</th>
							<th
								class="px-5 py-2.5 text-right text-xs font-semibold tracking-wide text-gray-600 uppercase"
							>
								{t('clientBilling.detail.itemAmount')}
							</th>
						</tr>
					</thead>
					<tbody class="divide-y divide-gray-100">
						{#if data.items.length === 0}
							<tr>
								<td colspan="2" class="px-5 py-6 text-center text-gray-400">{t('table.empty')}</td>
							</tr>
						{:else}
							{#each data.items as item (item.id)}
								<tr data-testid={`row-invoice-item-${item.id}`}>
									<td class="px-5 py-2.5 text-gray-700">{item.description}</td>
									<td class="px-5 py-2.5 text-right text-gray-700">
										<MoneyText amount={item.amount} currency={invoice.currency || 'IDR'} />
									</td>
								</tr>
							{/each}
						{/if}
					</tbody>
					<tfoot class="border-t border-gray-200 bg-gray-50/60">
						<tr>
							<td class="px-5 py-2 text-right text-gray-500">{t('billing.subtotal')}</td>
							<td class="px-5 py-2 text-right text-gray-700">
								<MoneyText amount={invoice.subtotal} currency={invoice.currency || 'IDR'} />
							</td>
						</tr>
						{#if invoice.discount > 0}
							<tr>
								<td class="px-5 py-2 text-right text-gray-500">{t('billing.discount')}</td>
								<td class="px-5 py-2 text-right text-gray-700">
									&minus;<MoneyText
										amount={invoice.discount}
										currency={invoice.currency || 'IDR'}
									/>
								</td>
							</tr>
						{/if}
						{#if invoice.tax_total > 0 || invoice.tax_rate > 0}
							<tr>
								<td class="px-5 py-2 text-right text-gray-500">
									{t('clientBilling.detail.taxWithRate', { rate: invoice.tax_rate })}
								</td>
								<td class="px-5 py-2 text-right text-gray-700">
									<MoneyText amount={invoice.tax_total} currency={invoice.currency || 'IDR'} />
								</td>
							</tr>
						{/if}
						{#if invoice.credit_applied > 0}
							<tr>
								<td class="px-5 py-2 text-right text-gray-500"
									>{t('clientBilling.detail.creditApplied')}</td
								>
								<td class="px-5 py-2 text-right text-gray-700">
									&minus;<MoneyText
										amount={invoice.credit_applied}
										currency={invoice.currency || 'IDR'}
									/>
								</td>
							</tr>
						{/if}
						<tr class="border-t border-gray-200">
							<td class="px-5 py-2.5 text-right text-sm font-bold text-gray-800"
								>{t('billing.total')}</td
							>
							<td
								class="px-5 py-2.5 text-right text-sm font-bold text-gray-800"
								data-testid="invoice-total"
							>
								<MoneyText amount={invoice.total} currency={invoice.currency || 'IDR'} />
							</td>
						</tr>
					</tfoot>
				</table>

				{#if invoice.notes}
					<div class="border-t border-gray-200 px-5 py-3 text-sm text-gray-500">
						{invoice.notes}
					</div>
				{/if}
			</div>

			<!-- Previous transactions -->
			{#if data.transactions.length > 0}
				<div class="ca-card" style="overflow:hidden;margin-top:20px">
					<div class="ca-card-header">
						{t('clientBilling.detail.previousTransactions')}
					</div>
					<div class="overflow-x-auto">
						<table class="min-w-full divide-y divide-gray-200 text-sm">
							<thead class="bg-gray-50">
								<tr>
									<th
										class="px-5 py-2 text-left text-xs font-semibold tracking-wide text-gray-600 uppercase"
									>
										{t('clientBilling.transactions.colDate')}
									</th>
									<th
										class="px-5 py-2 text-left text-xs font-semibold tracking-wide text-gray-600 uppercase"
									>
										{t('clientBilling.transactions.colMethod')}
									</th>
									<th
										class="px-5 py-2 text-right text-xs font-semibold tracking-wide text-gray-600 uppercase"
									>
										{t('billing.amount')}
									</th>
									<th
										class="px-5 py-2 text-center text-xs font-semibold tracking-wide text-gray-600 uppercase"
									>
										{t('clientBilling.transactions.colStatus')}
									</th>
								</tr>
							</thead>
							<tbody class="divide-y divide-gray-100">
								{#each data.transactions as trx (trx.id)}
									<tr data-testid={`row-invoice-transaction-${trx.id}`}>
										<td class="px-5 py-2 text-gray-700"
											><DateText value={trx.created_at} mode="datetime" /></td
										>
										<td class="px-5 py-2 text-gray-700">{trx.gateway} / {trx.method_code || '—'}</td
										>
										<td class="px-5 py-2 text-right text-gray-700"
											><MoneyText amount={trx.amount} /></td
										>
										<td class="px-5 py-2 text-center"><StatusBadge status={trx.status} /></td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				</div>
			{/if}
		</div>

		<!-- Payment panel -->
		<div>
			{#if payable}
				<div class="ca-card" style="overflow:hidden" data-testid="payment-panel">
					<div class="ca-card-header">
						{t('clientBilling.detail.paymentTitle')}
					</div>
					<div class="ca-card-body space-y-4">
						{#if payError}
							<Alert type="error">{payError}</Alert>
						{/if}

						{#if payment}
							<!-- Instructions returned by ?/pay (VA / QRIS) -->
							<div class="space-y-3" data-testid="payment-instructions">
								<h3 class="text-sm font-semibold text-gray-800">
									{t('clientBilling.detail.instructionsTitle')}
								</h3>
								<div class="rounded-md border border-gray-200 bg-gray-50 p-3 text-sm">
									<p class="text-gray-500">{t('clientBilling.detail.payAmount')}</p>
									<p class="text-lg font-bold text-gray-800">
										<MoneyText amount={payment.amount ?? invoice.total} />
									</p>
									{#if payment.fee > 0}
										<p class="mt-1 text-xs text-gray-500" data-testid="payment-fee-note">
											{t('clientBilling.detail.feeIncluded', { fee: formatIDR(payment.fee) })}
										</p>
									{/if}
								</div>
								{#if payment.vaNumber}
									<div class="rounded-md border border-gray-200 bg-gray-50 p-3 text-sm">
										<p class="text-gray-500">{t('clientBilling.detail.vaNumber')}</p>
										<div class="flex items-center justify-between gap-2">
											<code
												class="text-base font-bold tracking-wider text-gray-800"
												data-testid="payment-va-number"
											>
												{payment.vaNumber}
											</code>
											<button
												type="button"
												class="ca-btn ca-btn-default shrink-0"
												style="padding:4px 10px;font-size:12px"
												data-testid="payment-copy-va"
												onclick={() => copyText(payment.vaNumber ?? '', 'va')}
											>
												{copiedKey === 'va'
													? t('clientBilling.detail.copied')
													: t('clientBilling.detail.copy')}
											</button>
										</div>
									</div>
								{/if}
								{#if payment.bankAccounts.length > 0}
									<div class="space-y-2">
										<p class="text-gray-500">{t('clientBilling.detail.bankAccounts')}</p>
										{#each payment.bankAccounts as account, i (i)}
											<div
												class="rounded-md border border-gray-200 bg-gray-50 p-3 text-sm"
												data-testid={`payment-bank-account-${i}`}
											>
												<p class="font-semibold text-gray-800">{account.bank_name}</p>
												<div class="flex items-center justify-between gap-2">
													<code
														class="text-base font-bold tracking-wider text-gray-800"
														data-testid={`payment-bank-account-number-${i}`}
													>
														{account.account_number}
													</code>
													<button
														type="button"
														class="ca-btn ca-btn-default shrink-0"
														style="padding:4px 10px;font-size:12px"
														data-testid={`payment-copy-bank-account-${i}`}
														onclick={() => copyText(account.account_number, `bank-${i}`)}
													>
														{copiedKey === `bank-${i}`
															? t('clientBilling.detail.copied')
															: t('clientBilling.detail.copy')}
													</button>
												</div>
												<p class="mt-1 text-xs text-gray-500">{account.account_holder}</p>
											</div>
										{/each}
									</div>
								{/if}
								{#if payment.note}
									<div
										class="rounded-md border border-gray-200 bg-gray-50 p-3 text-sm text-gray-700"
										data-testid="payment-note"
									>
										{payment.note}
									</div>
								{/if}
								{#if payment.qrString}
									<div class="rounded-md border border-gray-200 bg-gray-50 p-3 text-sm">
										<p class="mb-2 text-gray-500">{t('clientBilling.detail.qrString')}</p>
										{#if qrImageUrl}
											<div class="flex justify-center">
												<img
													src={qrImageUrl}
													alt={t('clientBilling.detail.qrString')}
													width="220"
													height="220"
													data-testid="payment-qr-image"
												/>
											</div>
										{/if}
										<details class="mt-2">
											<summary class="cursor-pointer text-xs text-gray-500">
												{t('clientBilling.detail.qrStringRaw')}
											</summary>
											<div class="mt-1 flex items-start justify-between gap-2">
												<code
													class="max-h-24 overflow-y-auto text-xs break-all text-gray-800"
													data-testid="payment-qr-string"
												>
													{payment.qrString}
												</code>
												<button
													type="button"
													class="ca-btn ca-btn-default shrink-0"
													style="padding:4px 10px;font-size:12px"
													data-testid="payment-copy-qr"
													onclick={() => copyText(payment.qrString ?? '', 'qr')}
												>
													{copiedKey === 'qr'
														? t('clientBilling.detail.copied')
														: t('clientBilling.detail.copy')}
												</button>
											</div>
										</details>
									</div>
								{/if}
								<dl class="space-y-1 text-sm text-gray-600">
									{#if payment.reference}
										<div class="flex justify-between gap-2">
											<dt class="text-gray-500">{t('clientBilling.detail.reference')}</dt>
											<dd class="font-medium" data-testid="payment-reference">
												{payment.reference}
											</dd>
										</div>
									{/if}
									{#if payment.expiresAt}
										<div class="flex justify-between gap-2">
											<dt class="text-gray-500">{t('clientBilling.detail.expiresAt')}</dt>
											<dd class="font-medium">
												<DateText value={payment.expiresAt} mode="datetime" />
											</dd>
										</div>
									{:else if payment.expiryMinutes}
										<div class="flex justify-between gap-2">
											<dt class="text-gray-500">{t('clientBilling.detail.expiresAt')}</dt>
											<dd class="font-medium">
												{t('clientBilling.detail.expiryMinutes', {
													minutes: payment.expiryMinutes
												})}
											</dd>
										</div>
									{/if}
								</dl>
								<Alert type="info">{t('clientBilling.detail.awaitingPayment')}</Alert>
								<button
									type="button"
									class="text-xs font-medium text-primary hover:underline"
									data-testid="payment-switch-method"
									onclick={() => (switchingMethod = true)}
								>
									{t('clientBilling.detail.switchMethod')}
								</button>
							</div>
						{:else}
							<!-- Method picker -->
							{#if data.methodsError}
								<Alert type="warning">{t('clientBilling.detail.methodsError')}</Alert>
							{/if}

							{#if hasMethods}
								<div class="flex items-center justify-between gap-2">
									<p class="text-sm font-medium text-gray-700">
										{t('clientBilling.detail.chooseMethod')}
									</p>
									{#if resumedPayment && switchingMethod}
										<button
											type="button"
											class="text-xs font-medium text-primary hover:underline"
											data-testid="payment-cancel-switch-method"
											onclick={() => (switchingMethod = false)}
										>
											{t('clientBilling.detail.cancelSwitchMethod')}
										</button>
									{/if}
								</div>
								{#snippet methodButton(method: {
									code: string;
									name: string;
									image?: string;
									fee: number;
								})}
									<button
										type="button"
										class={`flex items-center justify-between gap-3 rounded-md border p-3 text-left text-sm transition ${
											selectedMethod === method.code
												? 'border-primary bg-primary/5 ring-1 ring-primary'
												: 'border-gray-200 hover:border-gray-300 hover:bg-gray-50'
										}`}
										aria-pressed={selectedMethod === method.code}
										data-testid={`payment-method-${method.code}`}
										onclick={() => (selectedMethod = method.code)}
									>
										<span class="flex items-center gap-2">
											{#if method.image}
												<img src={method.image} alt={method.name} class="h-6 w-10 object-contain" />
											{/if}
											<span class="font-medium text-gray-800">{method.name}</span>
										</span>
										{#if method.fee > 0}
											<span class="text-xs whitespace-nowrap text-gray-500">
												{t('clientBilling.detail.fee')}: <MoneyText amount={method.fee} />
											</span>
										{/if}
									</button>
								{/snippet}
								<div class="grid grid-cols-1 gap-2" data-testid="payment-methods">
									{#if creditSufficient && data.creditBalance !== null}
										<button
											type="button"
											class={`flex items-center justify-between gap-3 rounded-md border p-3 text-left text-sm transition ${
												selectedMethod === 'credit'
													? 'border-primary bg-primary/5 ring-1 ring-primary'
													: 'border-gray-200 hover:border-gray-300 hover:bg-gray-50'
											}`}
											aria-pressed={selectedMethod === 'credit'}
											data-testid="payment-method-credit"
											onclick={() => (selectedMethod = 'credit')}
										>
											<span>
												<span class="block font-semibold text-gray-800">
													{t('clientBilling.detail.creditOption')}
												</span>
												<span class="block text-xs text-gray-500">
													{t('clientBilling.detail.creditOptionDesc')}
												</span>
											</span>
											<span class="text-xs text-gray-500">
												{t('billing.creditBalance')}:
												<MoneyText
													amount={data.creditBalance}
													class="font-semibold text-gray-700"
												/>
											</span>
										</button>
									{/if}
									{#each manualMethods as method (method.code)}
										{@render methodButton(method)}
									{/each}
									{#each popularMethods as method (method.code)}
										{@render methodButton(method)}
									{/each}
									{#if otherMethods.length > 0}
										<button
											type="button"
											class="rounded-md border border-dashed border-gray-300 p-3 text-center text-sm font-medium text-primary hover:bg-gray-50"
											aria-expanded={showMoreMethods}
											data-testid="payment-methods-toggle"
											onclick={() => (showMoreMethods = !showMoreMethods)}
										>
											{showMoreMethods
												? t('clientBilling.detail.showFewerMethods')
												: t('clientBilling.detail.showMoreMethods', {
														count: otherMethods.length
													})}
										</button>
									{/if}
									{#if showMoreMethods}
										{#each otherMethods as method (method.code)}
											{@render methodButton(method)}
										{/each}
									{/if}
								</div>

								<form
									method="POST"
									action="?/pay"
									use:enhance={() => {
										paying = true;
										return async ({ update, result }) => {
											paying = false;
											await update();
											// Credit payments settle immediately (no gateway instructions).
											if (
												result.type === 'success' &&
												!(result.data as { payment?: unknown } | undefined)?.payment
											) {
												toast.success(t('clientBilling.detail.paymentReceived'));
											}
										};
									}}
								>
									<input type="hidden" name="method" value={selectedMethod} />
									<div data-testid="pay-button">
										<LoadingButton
											type="submit"
											loading={paying}
											disabled={!selectedMethod}
											class="w-full"
										>
											{t('billing.payNow')}
										</LoadingButton>
									</div>
								</form>
							{:else if !data.methodsError}
								<Alert type="warning">{t('clientBilling.detail.methodsError')}</Alert>
							{/if}
						{/if}
					</div>
				</div>
			{/if}
		</div>
	</div>
{/if}
