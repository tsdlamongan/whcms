<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import MoneyText from '$lib/components/MoneyText.svelte';
	import { untrack } from 'svelte';
	import type { PageProps } from './$types';
	import { clientDisplayName, type AdminClientLite } from '../types';

	let { data, form }: PageProps = $props();

	interface DraftRow {
		description: string;
		amount: number;
		taxed: boolean;
	}

	let clientId = $state(untrack(() => String(form?.clientId || '')));
	let dueDate = $state(untrack(() => form?.dueDate ?? defaultDueDate()));
	let notes = $state(untrack(() => form?.notes ?? ''));
	let rows = $state<DraftRow[]>(untrack(() => restoreRows()));
	let submitting = $state(false);

	function defaultDueDate(): string {
		const d = new Date();
		d.setDate(d.getDate() + 3);
		return d.toISOString().slice(0, 10);
	}

	function restoreRows(): DraftRow[] {
		if (form?.itemsRaw) {
			try {
				const parsed = JSON.parse(form.itemsRaw) as DraftRow[];
				if (Array.isArray(parsed) && parsed.length > 0) return parsed;
			} catch {
				// fall through to the default row
			}
		}
		return [{ description: '', amount: 0, taxed: true }];
	}

	function addRow() {
		rows = [...rows, { description: '', amount: 0, taxed: true }];
	}

	function removeRow(index: number) {
		if (rows.length <= 1) return;
		rows = rows.filter((_, i) => i !== index);
	}

	const validRows = $derived(rows.filter((r) => r.description.trim() !== ''));
	const itemsJson = $derived(
		JSON.stringify(
			validRows.map((r) => ({
				description: r.description.trim(),
				amount: Math.trunc(Number(r.amount) || 0),
				taxed: r.taxed
			}))
		)
	);
	const subtotal = $derived(
		validRows.reduce((sum, r) => sum + (Math.trunc(Number(r.amount)) || 0), 0)
	);

	const clientOptions = $derived(
		data.clients.map((c: AdminClientLite) => ({
			value: String(c.id),
			label: `${clientDisplayName(c)}${c.email ? ` (${c.email})` : ''}`
		}))
	);

	const ERROR_MESSAGES: Record<string, string> = {
		'adminBilling.create.errClient': 'Please select a client first.',
		'adminBilling.create.errDueDate': 'The due date is required.',
		'adminBilling.create.errItems': 'Add at least one item with a description.'
	};
	const errorText = $derived(
		form?.errorKey
			? (ERROR_MESSAGES[form.errorKey] ?? 'Something went wrong.')
			: (form?.errorMessage ?? null)
	);
</script>

<svelte:head>
	<title>Create Manual Invoice — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Create Manual Invoice</h1>
<p class="hp-lead">Issue a manual invoice for a client.</p>

{#if errorText}
	<div class="hp-alert-red">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{errorText}
	</div>
{/if}

<div class="hp-invoice-layout">
	<div class="hp-panel" style="padding:16px 20px">
		<h2
			style="font-size:14px;font-weight:700;color:#555;margin:0 0 10px;text-transform:uppercase;letter-spacing:.03em"
		>
			Client
		</h2>

		<!-- Querystring-driven client search (separate GET form; forms must not nest). -->
		<form
			method="GET"
			action="/admin/invoices/new"
			style="display:flex;flex-wrap:wrap;gap:8px;margin-bottom:14px"
		>
			<input
				type="search"
				name="client_search"
				value={data.clientSearch}
				placeholder="Search name, email, company…"
				data-testid="invoice-client-search"
				class="hp-input"
				style="flex:1 1 260px"
			/>
			<button type="submit" data-testid="invoice-client-search-submit" class="hp-btn">
				<i class="fas fa-search"></i>Search
			</button>
		</form>

		{#if data.clientsError}
			<div class="hp-alert-yellow">{data.clientsError}</div>
		{/if}

		<form
			method="POST"
			use:enhance={() => {
				submitting = true;
				return async ({ update }) => {
					submitting = false;
					await update();
				};
			}}
		>
			<div class="hp-formrow" data-testid="invoice-client-select">
				<label for="field-client_id">Select Client<span style="color:#d9534f"> *</span></label>
				<div class="hp-field" style="flex:1 1 auto">
					<select
						id="field-client_id"
						name="client_id"
						bind:value={clientId}
						class="hp-select"
						required
					>
						<option value="" disabled>Choose a client…</option>
						{#each clientOptions as opt (opt.value)}
							<option value={opt.value}>{opt.label}</option>
						{/each}
					</select>
					{#if data.clients.length === 0}
						<div class="hp-help" style="margin-top:3px">
							No matching clients — use the search above.
						</div>
					{/if}
				</div>
			</div>

			<h2
				style="font-size:14px;font-weight:700;color:#555;margin:18px 0 10px;text-transform:uppercase;letter-spacing:.03em"
			>
				Invoice Items
			</h2>

			<div class="hp-scroll">
				<table class="hp-table">
					<thead>
						<tr>
							<th>Description</th>
							<th class="r" style="width:160px">Amount</th>
							<th class="c" style="width:80px">Taxed</th>
							<th style="width:44px"></th>
						</tr>
					</thead>
					<tbody>
						{#each rows as row, i (i)}
							<tr>
								<td>
									<input
										type="text"
										bind:value={row.description}
										placeholder="Item description…"
										data-testid={`invoice-item-description-${i}`}
										class="hp-input"
									/>
								</td>
								<td>
									<input
										type="number"
										min="0"
										step="1"
										bind:value={row.amount}
										data-testid={`invoice-item-amount-${i}`}
										class="hp-input"
										style="text-align:right"
									/>
								</td>
								<td class="c">
									<input type="checkbox" bind:checked={row.taxed} aria-label="Taxed" />
								</td>
								<td class="c">
									<button
										type="button"
										onclick={() => removeRow(i)}
										disabled={rows.length <= 1}
										aria-label="Remove"
										class="hp-btn"
										style="padding:3px 8px"
									>
										&times;
									</button>
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>

			<div style="margin-top:10px">
				<button type="button" onclick={addRow} class="hp-btn" data-testid="invoice-add-item-row">
					<i class="fas fa-plus"></i>Add Row
				</button>
			</div>

			<div class="hp-formrow" style="margin-top:8px">
				<label for="field-due_date">Due Date<span style="color:#d9534f"> *</span></label>
				<div class="hp-field">
					<input
						id="field-due_date"
						name="due_date"
						type="date"
						bind:value={dueDate}
						required
						class="hp-input"
					/>
				</div>
			</div>
			<div class="hp-formrow">
				<label for="field-notes">Notes</label>
				<div class="hp-field">
					<input
						id="field-notes"
						name="notes"
						type="text"
						bind:value={notes}
						placeholder="Optional"
						class="hp-input"
					/>
				</div>
			</div>

			<input type="hidden" name="items_json" value={itemsJson} />

			<div
				style="display:flex;align-items:center;justify-content:flex-end;gap:14px;border-top:1px solid #eee;margin-top:16px;padding-top:14px"
			>
				<p style="margin:0;font-size:13px;color:#555">
					Subtotal:
					<span style="font-weight:700;color:#333"><MoneyText amount={subtotal} /></span>
				</p>
				<button
					type="submit"
					class="hp-btn hp-btn-primary"
					disabled={submitting}
					data-testid="invoice-create-submit"
				>
					<i class="fas fa-file-invoice"></i>Create Invoice
				</button>
			</div>
		</form>
	</div>

	<aside class="hp-panel" style="padding:16px 20px">
		<h2
			style="font-size:14px;font-weight:700;color:#555;margin:0 0 8px;text-transform:uppercase;letter-spacing:.03em"
		>
			Notes
		</h2>
		<p style="color:#666;font-size:13px;line-height:1.6;margin:0">
			Amounts are whole rupiah without decimals. Subtotal, tax, and total are recalculated by the
			server when the invoice is created.
		</p>
	</aside>
</div>

<style>
	.hp-invoice-layout {
		display: grid;
		grid-template-columns: minmax(0, 2fr) minmax(0, 1fr);
		gap: 16px;
		align-items: start;
	}
	@media (max-width: 860px) {
		.hp-invoice-layout {
			grid-template-columns: 1fr;
		}
	}
</style>
