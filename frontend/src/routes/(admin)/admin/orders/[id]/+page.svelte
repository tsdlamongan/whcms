<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import DateText from '$lib/components/DateText.svelte';
	import HpModal from '$lib/components/hp/HpModal.svelte';
	import MoneyText from '$lib/components/MoneyText.svelte';
	import { toast } from '$lib/stores/toast.svelte';
	import type { SubmitFunction } from '@sveltejs/kit';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	const order = $derived(data.order);
	const clientLabel = $derived(
		data.client
			? [data.client.first_name, data.client.last_name].filter(Boolean).join(' ') ||
					data.client.email ||
					`#${data.client.id}`
			: order
				? `#${order.client_id}`
				: ''
	);

	type OrderAction = 'accept' | 'cancel' | 'fraud';
	let confirmAction = $state<OrderAction | null>(null);
	let submitting = $state(false);
	let acceptForm: HTMLFormElement | undefined = $state();
	let cancelForm: HTMLFormElement | undefined = $state();
	let fraudForm: HTMLFormElement | undefined = $state();

	const confirmTitles: Record<OrderAction, string> = {
		accept: 'Accept Order',
		cancel: 'Cancel Order',
		fraud: 'Mark As Fraud'
	};
	const confirmMessages: Record<OrderAction, string> = {
		accept: 'Accept this order and activate the related services/domains?',
		cancel: 'Cancel this order? This cannot be undone.',
		fraud: 'Mark this order as fraud? It will be closed and flagged.'
	};

	function submitConfirmed() {
		if (confirmAction === 'accept') acceptForm?.requestSubmit();
		else if (confirmAction === 'cancel') cancelForm?.requestSubmit();
		else if (confirmAction === 'fraud') fraudForm?.requestSubmit();
	}

	const actionEnhance: SubmitFunction = () => {
		submitting = true;
		return async ({ result, update }) => {
			submitting = false;
			confirmAction = null;
			if (result.type === 'success') {
				toast.success('Action completed successfully');
			}
			await update();
		};
	};

	const actionError = $derived(form?.errorMessage ?? null);

	const CYCLE_LABELS: Record<string, string> = {
		one_time: 'One-Time',
		monthly: 'Monthly',
		quarterly: 'Quarterly',
		semiannually: 'Semi-Annually',
		annually: 'Annually',
		biennially: 'Biennially'
	};
	function cycleLabel(cycle: string): string {
		return CYCLE_LABELS[cycle] ?? cycle;
	}

	const ITEM_TYPE_LABELS: Record<string, string> = {
		product: 'Product',
		domain_register: 'Domain Registration',
		domain_transfer: 'Domain Transfer'
	};
	function itemTypeLabel(type: string): string {
		return ITEM_TYPE_LABELS[type] ?? type;
	}

	function invoiceBadgeClass(status: string): string {
		if (status === 'paid') return 'active';
		if (status === 'unpaid') return 'pending';
		if (status === 'overdue') return 'terminated';
		return 'cancelled';
	}
</script>

<svelte:head>
	<title>{order?.order_number ?? 'Order'} — {appName} Admin</title>
</svelte:head>

{#if !order}
	<h1 class="hp-h1">Order</h1>
	<div class="hp-alert-red" data-testid="order-detail-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.loadError ??
			'Unable to load this order.'}
	</div>
	<a class="hp-btn" href="/admin/orders"><i class="fas fa-arrow-left"></i>Back to Orders</a>
{:else}
	<div
		style="display:flex;flex-wrap:wrap;align-items:flex-start;justify-content:space-between;gap:12px;margin-bottom:14px"
	>
		<div>
			<h1 class="hp-h1" style="margin-bottom:2px">
				<span data-testid="order-number">{order.order_number}</span>
				<span
					data-testid="order-status"
					class={`hp-badge ${order.status}`}
					style="margin-left:8px;vertical-align:middle">{order.status}</span
				>
			</h1>
			<p style="color:#888;font-size:12px;margin:0">
				<DateText value={order.created_at} mode="datetime" />
			</p>
		</div>

		{#if order.status === 'pending'}
			<div style="display:flex;flex-wrap:wrap;gap:8px">
				<span data-testid="order-accept">
					<button
						type="button"
						class="hp-btn hp-btn-green"
						disabled={submitting && confirmAction === 'accept'}
						onclick={() => (confirmAction = 'accept')}
					>
						<i class="fas fa-check"></i>Accept Order
					</button>
				</span>
				<span data-testid="order-cancel">
					<button
						type="button"
						class="hp-btn"
						disabled={submitting && confirmAction === 'cancel'}
						onclick={() => (confirmAction = 'cancel')}
					>
						<i class="fas fa-ban"></i>Cancel Order
					</button>
				</span>
				<span data-testid="order-fraud">
					<button
						type="button"
						class="hp-btn hp-btn-danger"
						disabled={submitting && confirmAction === 'fraud'}
						onclick={() => (confirmAction = 'fraud')}
					>
						<i class="fas fa-exclamation-triangle"></i>Mark Fraud
					</button>
				</span>
			</div>
		{/if}
	</div>

	{#if actionError}
		<div class="hp-alert-red" data-testid="order-action-error">
			<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{actionError}
		</div>
	{/if}

	<!-- Hidden action forms, submitted from the confirm modal below. -->
	<form
		method="POST"
		action="?/accept"
		class="hidden"
		bind:this={acceptForm}
		use:enhance={actionEnhance}
	></form>
	<form
		method="POST"
		action="?/cancel"
		class="hidden"
		bind:this={cancelForm}
		use:enhance={actionEnhance}
	></form>
	<form
		method="POST"
		action="?/fraud"
		class="hidden"
		bind:this={fraudForm}
		use:enhance={actionEnhance}
	></form>

	<HpModal
		open={confirmAction !== null}
		title={confirmAction ? confirmTitles[confirmAction] : ''}
		onClose={() => (confirmAction = null)}
	>
		<p style="color:#555;margin-bottom:16px">
			{confirmAction ? confirmMessages[confirmAction] : ''}
		</p>
		<div style="display:flex;justify-content:flex-end;gap:8px">
			<button
				type="button"
				class="hp-btn"
				disabled={submitting}
				onclick={() => (confirmAction = null)}
			>
				Cancel
			</button>
			<button
				type="button"
				class={`hp-btn ${confirmAction === 'accept' ? 'hp-btn-primary' : 'hp-btn-danger'}`}
				disabled={submitting}
				onclick={submitConfirmed}
			>
				{#if submitting}<i class="fas fa-spinner fa-spin"></i>{/if}Confirm
			</button>
		</div>
	</HpModal>

	<div class="hp-grid-order">
		<!-- Items + totals -->
		<section class="hp-panel" data-testid="order-items">
			<div class="hp-panel-hd"><span class="title">Order Items</span></div>
			{#if data.items.length === 0}
				<div style="padding:24px;text-align:center;color:#999">No items on this order.</div>
			{:else}
				<div class="hp-scroll">
					<table class="hp-table" style="border:none">
						<thead>
							<tr>
								<th>Description</th>
								<th>Type</th>
								<th>Cycle</th>
								<th class="r">Unit Price</th>
								<th class="r">Setup Fee</th>
							</tr>
						</thead>
						<tbody>
							{#each data.items as item (item.id)}
								<tr class="hp-row" data-testid={`row-order-item-${item.id}`}>
									<td>
										{item.description || '—'}
										{#if item.domain}
											<div style="color:#999;font-size:12px">{item.domain}</div>
										{/if}
									</td>
									<td>{itemTypeLabel(item.item_type)}</td>
									<td>{cycleLabel(item.cycle)}</td>
									<td class="r"><MoneyText amount={item.unit_price ?? 0} /></td>
									<td class="r"><MoneyText amount={item.setup_fee ?? 0} /></td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
			<div style="border-top:1px solid #eee;padding:12px 16px">
				<div class="hp-totals">
					<div><span>Subtotal</span><b><MoneyText amount={order.subtotal ?? 0} /></b></div>
					<div><span>Discount</span><b><MoneyText amount={order.discount ?? 0} /></b></div>
					<div><span>Tax</span><b><MoneyText amount={order.tax_total ?? 0} /></b></div>
					<div class="total">
						<span>Total</span>
						<b data-testid="order-total"><MoneyText amount={order.total ?? 0} /></b>
					</div>
				</div>
			</div>
		</section>

		<div style="display:flex;flex-direction:column;gap:14px">
			<section class="hp-panel">
				<div class="hp-panel-hd"><span class="title">Order Information</span></div>
				<table class="hp-table" style="border:none">
					<tbody>
						<tr class="hp-row">
							<td style="color:#888;width:100px">Client</td>
							<td>
								<a
									class="cell-link"
									href={`/admin/clients/${order.client_id}`}
									data-testid="order-client-link">{clientLabel}</a
								>
							</td>
						</tr>
						<tr class="hp-row">
							<td style="color:#888">Date</td>
							<td><DateText value={order.created_at} mode="datetime" /></td>
						</tr>
						<tr class="hp-row">
							<td style="color:#888">IP Address</td>
							<td>{order.ip || '—'}</td>
						</tr>
						{#if order.notes}
							<tr class="hp-row">
								<td style="color:#888;vertical-align:top">Notes</td>
								<td style="white-space:pre-wrap">{order.notes}</td>
							</tr>
						{/if}
					</tbody>
				</table>
			</section>

			<section class="hp-panel">
				<div class="hp-panel-hd"><span class="title">Linked Invoice</span></div>
				<div style="padding:14px 16px">
					{#if data.invoice || data.invoiceId}
						<div style="display:flex;align-items:center;justify-content:space-between;gap:8px">
							<a
								class="cell-link"
								href={`/admin/invoices/${data.invoice?.id ?? data.invoiceId}`}
								data-testid="order-invoice-link"
							>
								{data.invoice?.invoice_number ?? `Invoice #${data.invoiceId}`}
							</a>
							{#if data.invoice?.status}
								<span class={`hp-badge ${invoiceBadgeClass(data.invoice.status)}`}
									>{data.invoice.status}</span
								>
							{/if}
						</div>
						{#if data.invoice?.total !== undefined}
							<p style="margin:8px 0 0;color:#555">
								Total: <MoneyText amount={data.invoice.total ?? 0} />
							</p>
						{/if}
					{:else}
						<p style="color:#999;margin:0" data-testid="order-no-invoice">
							No invoice is linked to this order.
						</p>
					{/if}
				</div>
			</section>
		</div>
	</div>
{/if}

<style>
	.hidden {
		display: none;
	}
	.hp-grid-order {
		display: grid;
		grid-template-columns: minmax(0, 2fr) minmax(0, 1fr);
		gap: 14px;
	}
	@media (max-width: 900px) {
		.hp-grid-order {
			grid-template-columns: 1fr;
		}
	}
	.hp-totals {
		margin-left: auto;
		max-width: 260px;
		font-size: 13px;
	}
	.hp-totals > div {
		display: flex;
		justify-content: space-between;
		padding: 3px 0;
		color: #888;
	}
	.hp-totals b {
		font-weight: 600;
		color: #444;
	}
	.hp-totals .total {
		border-top: 1px solid #eee;
		margin-top: 4px;
		padding-top: 6px;
		color: #333;
	}
	.hp-totals .total b {
		font-weight: 700;
		color: #333;
	}
</style>
