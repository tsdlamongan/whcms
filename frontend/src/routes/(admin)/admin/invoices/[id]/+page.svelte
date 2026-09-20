<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import { DateText, MoneyText } from '$lib/components';
	import HpModal from '$lib/components/hp/HpModal.svelte';
	import HpPanel from '$lib/components/hp/HpPanel.svelte';
	import { toast } from '$lib/stores/toast.svelte';
	import type { SubmitFunction } from '@sveltejs/kit';
	import type { PageProps } from './$types';
	import { clientDisplayName } from '../types';

	let { data, form }: PageProps = $props();

	const invoice = $derived(data.invoice);
	const items = $derived(data.invoice?.items ?? []);

	type TabId = 'summary' | 'payment' | 'refund' | 'notes';
	let active = $state<TabId>('summary');

	let addItemOpen = $state(false);
	let cancelOpen = $state(false);
	let deleteItemId = $state<number | null>(null);
	let submitting = $state(false);

	let deleteItemForm: HTMLFormElement | undefined = $state();
	let cancelForm: HTMLFormElement | undefined = $state();

	const canPay = $derived(invoice?.status === 'unpaid' || invoice?.status === 'overdue');
	const canRefund = $derived(invoice?.status === 'paid');
	const canCancel = $derived(
		invoice?.status === 'draft' || invoice?.status === 'unpaid' || invoice?.status === 'overdue'
	);
	const isDraft = $derived(invoice?.status === 'draft');

	// If the current action tab's guard flips off mid-session (e.g. the
	// invoice just got paid), fall back to Summary rather than showing a
	// stale/empty tab.
	$effect(() => {
		if (active === 'payment' && !canPay) active = 'summary';
		if (active === 'refund' && !canRefund) active = 'summary';
	});

	function goPay() {
		active = 'payment';
	}
	function goRefund() {
		active = 'refund';
	}
	function goNotes() {
		active = 'notes';
	}

	function openAddItem() {
		addItemOpen = true;
	}

	const ERROR_TEXT: Record<string, string> = {
		'adminBilling.create.errDueDate': 'The due date is required.',
		'adminBilling.detail.errAmount': 'The amount must be a positive number.',
		'adminBilling.detail.errMethod': 'The payment method is required.',
		'adminBilling.detail.errItemDescription': 'The item description is required.'
	};
	const errorText = $derived(
		form?.errorKey ? (ERROR_TEXT[form.errorKey] ?? form.errorKey) : (form?.errorMessage ?? null)
	);

	const STATUS_COLOR: Record<string, string> = {
		paid: '#46a546',
		unpaid: '#f89406',
		overdue: '#c43c35',
		cancelled: '#bfbfbf',
		refunded: '#0768b8',
		draft: '#8a8a8a'
	};
	const STATUS_TEXT: Record<string, string> = {
		paid: 'This invoice has been paid.',
		unpaid: 'This invoice is unpaid.',
		overdue: 'This invoice is past its due date.',
		cancelled: 'This invoice has been cancelled.',
		refunded: 'This invoice has been refunded.',
		draft: 'This invoice is still a draft — items can still be edited.'
	};
	const bannerColor = $derived(STATUS_COLOR[invoice?.status ?? ''] ?? '#8a8a8a');
	const bannerText = $derived(STATUS_TEXT[invoice?.status ?? ''] ?? '');

	function txStatusClass(s: string): string {
		if (s === 'success') return 'green';
		if (s === 'pending') return 'orange';
		if (s === 'failed' || s === 'expired') return 'red';
		return 'gray';
	}

	/**
	 * Shared enhance factory: shows a toast, closes any open modal, and
	 * (for the Add Payment / Refund / Notes tabs) hops back to Summary so
	 * the user sees the freshly updated status banner.
	 */
	function mutate(successMsg: string, opts: { toSummary?: boolean } = {}): SubmitFunction {
		return () => {
			submitting = true;
			return async ({ result, update }) => {
				submitting = false;
				if (result.type === 'success') {
					addItemOpen = false;
					cancelOpen = false;
					deleteItemId = null;
					if (opts.toSummary) active = 'summary';
					toast.success(successMsg);
				}
				await update();
			};
		};
	}
</script>

<svelte:head>
	<title>{invoice ? invoice.invoice_number : 'Invoice'} — {appName}</title>
</svelte:head>

{#if !invoice}
	<h1 class="hp-h1">Invoice</h1>
	<div class="hp-alert-red" data-testid="invoice-not-found">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.errorMessage ??
			'Invoice not found.'}
	</div>
{:else}
	{#if errorText}
		<div class="hp-alert-red">{errorText}</div>
	{/if}

	<div
		style="display:flex;justify-content:space-between;align-items:center;flex-wrap:wrap;gap:10px"
	>
		<h1 class="hp-h1" style="margin:0">Invoice {invoice.invoice_number}</h1>
		<div style="display:flex;flex-wrap:wrap;gap:8px;margin-bottom:18px">
			{#if canPay}
				<button
					type="button"
					class="hp-btn hp-btn-green"
					data-testid="add-payment-button"
					onclick={goPay}
				>
					<i class="fas fa-dollar-sign"></i>Add Payment
				</button>
			{/if}
			{#if canRefund}
				<button type="button" class="hp-btn" data-testid="refund-button" onclick={goRefund}>
					<i class="fas fa-undo"></i>Refund
				</button>
			{/if}
			<button type="button" class="hp-btn" data-testid="edit-invoice-button" onclick={goNotes}>
				<i class="fas fa-pencil-alt"></i>Edit
			</button>
			<a class="hp-btn" href={`/admin/invoices/${invoice.id}/pdf`} data-testid="invoice-pdf-link">
				<i class="fas fa-download"></i>Download PDF
			</a>
			{#if canCancel}
				<button
					type="button"
					class="hp-btn hp-btn-danger"
					data-testid="cancel-invoice-button"
					onclick={() => (cancelOpen = true)}
				>
					<i class="fas fa-ban"></i>Mark Cancelled
				</button>
			{/if}
		</div>
	</div>

	<!-- Big status banner: color matches the badge palette (docs/DESIGN.md §2). -->
	<div class="hp-status-banner" style={`background:${bannerColor}`}>
		<div class="hp-status-word" data-testid="invoice-status">
			{(invoice.status ?? '').toUpperCase()}
		</div>
		<div class="hp-status-sub">
			{bannerText}
			{#if invoice.status === 'paid' && invoice.paid_at}
				<DateText value={invoice.paid_at} mode="datetime" />
			{:else if invoice.status === 'unpaid' || invoice.status === 'overdue'}
				Due <DateText value={invoice.due_date} />
			{/if}
		</div>
	</div>

	<div class="hp-tabs">
		<button
			type="button"
			class="hp-tab"
			class:active={active === 'summary'}
			data-testid="invoice-tab-summary"
			onclick={() => (active = 'summary')}
		>
			Summary
		</button>
		{#if canPay}
			<button
				type="button"
				class="hp-tab"
				class:active={active === 'payment'}
				data-testid="invoice-tab-payment"
				onclick={goPay}
			>
				Add Payment
			</button>
		{/if}
		{#if canRefund}
			<button
				type="button"
				class="hp-tab"
				class:active={active === 'refund'}
				data-testid="invoice-tab-refund"
				onclick={goRefund}
			>
				Refund
			</button>
		{/if}
		<button
			type="button"
			class="hp-tab"
			class:active={active === 'notes'}
			data-testid="invoice-tab-notes"
			onclick={goNotes}
		>
			Notes
		</button>
	</div>

	<div class="hp-tabpanel">
		<!-- Summary -->
		<div class:hidden={active !== 'summary'}>
			<div class="hp-2col" style="margin-bottom:16px">
				<HpPanel title="Billed To">
					{#if data.client}
						<p style="font-weight:600">
							<a class="cell-link" href={`/admin/clients/${data.client.id}`}
								>{clientDisplayName(data.client)}</a
							>
						</p>
						{#if data.client.company}<p>{data.client.company}</p>{/if}
						<p style="color:#666">
							{[data.client.address1, data.client.city, data.client.state, data.client.postcode]
								.filter(Boolean)
								.join(', ')}
						</p>
						{#if data.client.email}<p style="color:#666">{data.client.email}</p>{/if}
						{#if data.client.phone}<p style="color:#666">{data.client.phone}</p>{/if}
					{:else}
						<p style="color:#666">
							{invoice.client_name || invoice.client_email || `#${invoice.client_id}`}
						</p>
					{/if}
				</HpPanel>

				<HpPanel title="Invoice Information">
					<div style="display:flex;justify-content:space-between;padding:3px 0">
						<span style="color:#666">Issued Date</span>
						<span><DateText value={invoice.created_at} /></span>
					</div>
					<div style="display:flex;justify-content:space-between;padding:3px 0">
						<span style="color:#666">Due Date</span>
						<span><DateText value={invoice.due_date} /></span>
					</div>
					{#if invoice.paid_at}
						<div style="display:flex;justify-content:space-between;padding:3px 0">
							<span style="color:#666">Paid At</span>
							<span><DateText value={invoice.paid_at} mode="datetime" /></span>
						</div>
					{/if}
					{#if invoice.notes}
						<div style="display:flex;justify-content:space-between;padding:3px 0;gap:12px">
							<span style="color:#666">Notes</span>
							<span style="text-align:right">{invoice.notes}</span>
						</div>
					{/if}
				</HpPanel>
			</div>

			<section class="hp-panel" style="margin-bottom:16px">
				<div class="hp-panel-hd">
					<span class="title">Invoice Items</span>
					{#if isDraft}
						<button
							type="button"
							class="hp-btn hp-btn-primary"
							data-testid="add-item-button"
							onclick={openAddItem}
						>
							<i class="fas fa-plus"></i>Add Item
						</button>
					{/if}
				</div>
				{#if !isDraft}
					<div class="hp-info" style="margin:12px 12px 0">
						<i class="fas fa-lock" style="margin-right:8px"></i>Items are locked — only draft
						invoices can be edited.
					</div>
				{/if}
				<div class="hp-scroll">
					<table class="hp-table">
						<thead>
							<tr>
								<th>Description</th>
								<th class="r" style="width:160px">Amount</th>
								{#if isDraft}<th class="c" style="width:56px"></th>{/if}
							</tr>
						</thead>
						<tbody>
							{#if items.length === 0}
								<tr>
									<td colspan={isDraft ? 3 : 2} style="text-align:center;padding:24px;color:#999">
										This invoice has no items yet.
									</td>
								</tr>
							{:else}
								{#each items as item (item.id)}
									<tr data-testid={`row-invoice-item-${item.id}`}>
										<td>
											{item.description}
											{#if item.taxed}
												<span style="color:#999;font-size:11px;margin-left:6px">(Tax)</span>
											{/if}
										</td>
										<td class="r"><MoneyText amount={item.amount} /></td>
										{#if isDraft}
											<td class="c">
												<button
													type="button"
													class="hp-btn hp-btn-danger"
													style="padding:2px 8px"
													data-testid={`delete-item-${item.id}`}
													aria-label="Delete item"
													onclick={() => (deleteItemId = item.id)}
												>
													&times;
												</button>
											</td>
										{/if}
									</tr>
								{/each}
							{/if}
						</tbody>
					</table>
				</div>
				<div style="padding:10px 12px;border-top:1px solid #eee">
					<div style="max-width:320px;margin-left:auto;font-size:13px">
						<div style="display:flex;justify-content:space-between;padding:2px 0">
							<span style="color:#666">Subtotal</span>
							<span><MoneyText amount={invoice.subtotal} /></span>
						</div>
						{#if invoice.discount > 0}
							<div style="display:flex;justify-content:space-between;padding:2px 0">
								<span style="color:#666">Discount</span>
								<span>-<MoneyText amount={invoice.discount} /></span>
							</div>
						{/if}
						{#if invoice.tax_total > 0}
							<div style="display:flex;justify-content:space-between;padding:2px 0">
								<span style="color:#666">Tax ({invoice.tax_rate}%)</span>
								<span><MoneyText amount={invoice.tax_total} /></span>
							</div>
						{/if}
						{#if invoice.credit_applied > 0}
							<div style="display:flex;justify-content:space-between;padding:2px 0">
								<span style="color:#666">Credit applied</span>
								<span>-<MoneyText amount={invoice.credit_applied} /></span>
							</div>
						{/if}
					</div>
				</div>
				<div class="hp-total-bar">
					<span>Total</span>
					<span data-testid="invoice-total">
						<MoneyText amount={invoice.total} currency={invoice.currency || 'IDR'} />
					</span>
				</div>
			</section>

			<HpPanel title="Transactions" flush>
				{#if data.transactionsError}
					<div class="hp-alert-yellow" style="margin:12px">
						<i class="fas fa-exclamation-triangle" style="margin-right:8px"
						></i>{data.transactionsError}
					</div>
				{:else if data.transactions.length === 0}
					<p style="text-align:center;padding:20px;color:#999">
						No transactions for this invoice yet.
					</p>
				{:else}
					<div class="hp-scroll">
						<table class="hp-table" style="border:none">
							<thead>
								<tr>
									<th>Date</th>
									<th>Gateway</th>
									<th>Method</th>
									<th class="r">Amount</th>
									<th class="c">Status</th>
								</tr>
							</thead>
							<tbody>
								{#each data.transactions as tx (tx.id)}
									<tr data-testid={`row-transaction-${tx.id}`}>
										<td><DateText value={tx.created_at} mode="datetime" /></td>
										<td>{tx.gateway}</td>
										<td>{tx.method_code || '—'}</td>
										<td class="r"><MoneyText amount={tx.amount} /></td>
										<td class="c"
											><span class={`hp-stext ${txStatusClass(tx.status)}`}>{tx.status}</span></td
										>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{/if}
			</HpPanel>
		</div>

		<!-- Add Payment -->
		{#if canPay}
			<div class:hidden={active !== 'payment'}>
				<form
					method="POST"
					action="?/addPayment"
					data-testid="invoice-form-payment"
					use:enhance={mutate('Manual payment recorded.', { toSummary: true })}
				>
					<div style="max-width:520px">
						<div class="hp-formrow">
							<label for="field-amount">Amount</label>
							<div class="hp-field">
								<input
									id="field-amount"
									name="amount"
									type="number"
									class="hp-input"
									value={invoice.total}
									required
								/>
								<div class="hp-help" style="margin-top:3px">Whole rupiah, no decimals.</div>
							</div>
						</div>
						<div class="hp-formrow">
							<label for="field-method">Payment Method</label>
							<div class="hp-field">
								<select id="field-method" name="method" class="hp-select" required>
									<option value="bank_transfer" selected>Bank transfer</option>
									<option value="cash">Cash</option>
									<option value="other">Other</option>
								</select>
							</div>
						</div>
						<div class="hp-form-actions">
							<button
								type="submit"
								class="hp-btn hp-btn-green"
								data-testid="add-payment-submit"
								disabled={submitting}
							>
								Add Payment
							</button>
						</div>
					</div>
				</form>
			</div>
		{/if}

		<!-- Refund -->
		{#if canRefund}
			<div class:hidden={active !== 'refund'}>
				<p style="color:#666;margin-bottom:6px">
					Refunding marks this invoice as refunded. This cannot be undone.
				</p>
				<form
					method="POST"
					action="?/refund"
					data-testid="invoice-form-refund"
					use:enhance={mutate('Invoice refunded.', { toSummary: true })}
				>
					<div style="max-width:520px">
						<div class="hp-formrow">
							<label for="field-reason">Refund Reason</label>
							<div class="hp-field">
								<input
									id="field-reason"
									name="reason"
									type="text"
									class="hp-input"
									placeholder="Optional"
								/>
							</div>
						</div>
						<div class="hp-form-actions">
							<button
								type="submit"
								class="hp-btn hp-btn-danger"
								data-testid="refund-submit"
								disabled={submitting}
							>
								Refund
							</button>
						</div>
					</div>
				</form>
			</div>
		{/if}

		<!-- Notes / due date -->
		<div class:hidden={active !== 'notes'}>
			<form
				method="POST"
				action="?/update"
				data-testid="invoice-form-notes"
				use:enhance={mutate('Invoice updated.', { toSummary: true })}
			>
				<div style="max-width:520px">
					<div class="hp-formrow">
						<label for="field-due_date">Due Date</label>
						<div class="hp-field">
							<input
								id="field-due_date"
								name="due_date"
								type="date"
								class="hp-input"
								value={invoice.due_date ? invoice.due_date.slice(0, 10) : ''}
								required
							/>
						</div>
					</div>
					<div class="hp-formrow top">
						<label for="field-notes">Notes</label>
						<div class="hp-field">
							<textarea id="field-notes" name="notes" rows="3" class="hp-textarea"
								>{invoice.notes}</textarea
							>
						</div>
					</div>
					<div class="hp-form-actions">
						<button
							type="submit"
							class="hp-btn hp-btn-primary"
							data-testid="edit-invoice-submit"
							disabled={submitting}
						>
							Save Changes
						</button>
					</div>
				</div>
			</form>
		</div>
	</div>

	<!-- Add draft item -->
	<HpModal open={addItemOpen} title="Add Item" onClose={() => (addItemOpen = false)}>
		<form method="POST" action="?/addItem" use:enhance={mutate('Item added.')}>
			<label class="hp-field-label" style="display:block" for="field-item-description"
				>Description</label
			>
			<input
				id="field-item-description"
				name="description"
				type="text"
				class="hp-input"
				style="margin-bottom:10px"
				required
			/>
			<label class="hp-field-label" style="display:block" for="field-item-amount">Amount</label>
			<input
				id="field-item-amount"
				name="amount"
				type="number"
				class="hp-input"
				value={0}
				required
			/>
			<div class="hp-help" style="margin:4px 0 10px">Whole rupiah, no decimals.</div>
			<label class="hp-checkline" style="padding:0 0 10px">
				<input type="checkbox" name="taxed" checked />
				Taxed
			</label>
			<div style="display:flex;justify-content:flex-end;gap:8px">
				<button type="button" class="hp-btn" onclick={() => (addItemOpen = false)}>Cancel</button>
				<button
					type="submit"
					class="hp-btn hp-btn-primary"
					data-testid="add-item-submit"
					disabled={submitting}
				>
					Add
				</button>
			</div>
		</form>
	</HpModal>

	<!-- Cancel invoice -->
	<form
		method="POST"
		action="?/cancel"
		bind:this={cancelForm}
		use:enhance={mutate('Invoice cancelled.')}
		class="hidden"
	></form>
	<HpModal open={cancelOpen} title="Cancel Invoice" onClose={() => (cancelOpen = false)}>
		<p style="color:#666;margin-bottom:16px">A cancelled invoice can no longer be paid.</p>
		<div style="display:flex;justify-content:flex-end;gap:8px">
			<button type="button" class="hp-btn" onclick={() => (cancelOpen = false)}>Cancel</button>
			<button
				type="button"
				class="hp-btn hp-btn-danger"
				disabled={submitting}
				onclick={() => cancelForm?.requestSubmit()}
			>
				Mark Cancelled
			</button>
		</div>
	</HpModal>

	<!-- Delete draft item -->
	<form
		method="POST"
		action="?/deleteItem"
		bind:this={deleteItemForm}
		use:enhance={mutate('Item deleted.')}
		class="hidden"
	>
		<input type="hidden" name="item_id" value={deleteItemId ?? ''} />
	</form>
	<HpModal open={deleteItemId !== null} title="Delete Item" onClose={() => (deleteItemId = null)}>
		<p style="color:#666;margin-bottom:16px">
			Are you sure you want to delete this item? This cannot be undone.
		</p>
		<div style="display:flex;justify-content:flex-end;gap:8px">
			<button type="button" class="hp-btn" onclick={() => (deleteItemId = null)}>Cancel</button>
			<button
				type="button"
				class="hp-btn hp-btn-danger"
				disabled={submitting}
				onclick={() => deleteItemForm?.requestSubmit()}
			>
				Delete
			</button>
		</div>
	</HpModal>
{/if}

<style>
	.hidden {
		display: none;
	}
	.hp-2col {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 16px;
	}
	@media (max-width: 720px) {
		.hp-2col {
			grid-template-columns: 1fr;
		}
	}
	.hp-status-banner {
		border-radius: 4px;
		padding: 18px 20px;
		color: #fff;
		margin: 0 0 16px;
		box-shadow: 0 1px 2px rgba(0, 0, 0, 0.1);
	}
	.hp-status-word {
		font-size: 28px;
		font-weight: 800;
		letter-spacing: 1px;
		line-height: 1.1;
	}
	.hp-status-sub {
		margin-top: 4px;
		font-size: 13px;
		opacity: 0.95;
	}
	.hp-total-bar {
		display: flex;
		justify-content: space-between;
		align-items: center;
		background: var(--hp-navy);
		color: #fff;
		font-weight: 700;
		font-size: 15px;
		padding: 10px 14px;
		border-radius: 0 0 4px 4px;
	}
</style>
