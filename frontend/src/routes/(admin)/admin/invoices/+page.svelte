<script lang="ts">
	import { appName } from '$lib/appName';
	import HpPager from '$lib/components/hp/HpPager.svelte';
	import { fmtDate } from '$lib/date';
	import { formatIDR } from '$lib/money';
	import { toast } from '$lib/stores/toast.svelte';
	import type { PageProps } from './$types';
	import type { AdminInvoice } from './types';

	let { data }: PageProps = $props();

	// sort (client-side, current page)
	let sortCol = $state('created_at');
	let sortDir = $state<'asc' | 'desc'>('desc');
	const accessors: Record<string, (r: AdminInvoice) => string | number> = {
		invoice_number: (r) => r.invoice_number,
		client: (r) => (r.client_name || r.client_email || String(r.client_id)).toLowerCase(),
		created_at: (r) => r.created_at,
		due_date: (r) => r.due_date,
		total: (r) => r.total,
		status: (r) => r.status
	};
	const rows = $derived.by(() => {
		const acc = accessors[sortCol] ?? accessors.created_at;
		const dir = sortDir === 'asc' ? 1 : -1;
		return [...data.invoices].sort((a, b) => {
			const x = acc(a);
			const y = acc(b);
			return x < y ? -dir : x > y ? dir : 0;
		});
	});
	function sortBy(col: string) {
		if (sortCol === col) sortDir = sortDir === 'asc' ? 'desc' : 'asc';
		else {
			sortCol = col;
			sortDir = 'asc';
		}
	}
	function arrow(col: string): string {
		return sortCol === col ? (sortDir === 'asc' ? '▲' : '▼') : '';
	}

	// selection
	let checked = $state<Set<number>>(new Set());
	const allChecked = $derived(rows.length > 0 && rows.every((r) => checked.has(r.id)));
	function toggle(id: number) {
		const next = new Set(checked);
		if (next.has(id)) next.delete(id);
		else next.add(id);
		checked = next;
	}
	function toggleAll() {
		checked = allChecked ? new Set() : new Set(rows.map((r) => r.id));
	}
	function bulk(label: string) {
		if (!checked.size) {
			toast.info('Select at least one invoice');
			return;
		}
		toast.success(`${checked.size} invoice(s): ${label}`);
	}

	// filter panel (open initially when arriving with active filters)
	function hasActiveFilters(): boolean {
		return !!(data.search || data.status || data.dateFrom || data.dateTo);
	}
	let filterOpen = $state(hasActiveFilters());

	// counter bar (over the loaded page)
	const sum = (pred: (i: AdminInvoice) => boolean) =>
		data.invoices.filter(pred).reduce((s, i) => s + (i.total ?? 0), 0);
	const paidTotal = $derived(sum((i) => i.status === 'paid'));
	const unpaidTotal = $derived(sum((i) => i.status === 'unpaid'));
	const overdueTotal = $derived(sum((i) => i.status === 'overdue'));

	// pagination
	const from = $derived(data.total === 0 ? 0 : (data.page - 1) * data.perPage + 1);
	const to = $derived(Math.min(data.page * data.perPage, data.total));

	const idr = (n: number) => formatIDR(n);
	function clientLabel(r: AdminInvoice): string {
		return r.client_name || r.client_email || `#${r.client_id}`;
	}
	function statusClass(s: string): string {
		if (s === 'paid') return 'green';
		if (s === 'unpaid' || s === 'overdue') return 'red';
		return 'gray';
	}
</script>

<svelte:head>
	<title>Invoices — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Invoices</h1>

{#if data.errorMessage}
	<div class="hp-alert-yellow">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.errorMessage}
	</div>
{/if}

<div class="hp-counterbar">
	Paid: <b style="color:#43a047">{idr(paidTotal)}</b> &nbsp; Unpaid:
	<b style="color:#d9534f">{idr(unpaidTotal)}</b> &nbsp; Overdue:
	<b style="color:#d9534f">{idr(overdueTotal)}</b>
	<span style="color:#999;font-size:12px"> (this page)</span>
</div>

<div style="display:flex;gap:8px;margin-bottom:10px;flex-wrap:wrap">
	<button class="hp-btn" onclick={() => (filterOpen = !filterOpen)}>Search/Filter</button>
	<a
		class="hp-btn hp-btn-primary"
		href="/admin/invoices/new"
		data-testid="admin-invoice-create-link"
	>
		<i class="fas fa-plus"></i>Create Invoice
	</a>
</div>

{#if filterOpen}
	<form method="GET" action="/admin/invoices" class="hp-filter" style="align-items:flex-end">
		<div class="grow">
			<div class="hp-field-label">Search</div>
			<input
				class="hp-input"
				type="search"
				name="search"
				value={data.search}
				data-testid="admin-invoice-search"
			/>
		</div>
		<div style="flex:0 0 160px">
			<div class="hp-field-label">Status</div>
			<select
				class="hp-select"
				name="status"
				value={data.status}
				data-testid="admin-invoice-filter-status"
			>
				<option value="">All</option>
				{#each data.statusOptions as s (s)}
					<option value={s}>{s}</option>
				{/each}
			</select>
		</div>
		<div style="flex:0 0 150px">
			<div class="hp-field-label">Date From</div>
			<input
				class="hp-input"
				type="date"
				name="date_from"
				value={data.dateFrom}
				data-testid="admin-invoice-filter-from"
			/>
		</div>
		<div style="flex:0 0 150px">
			<div class="hp-field-label">Date To</div>
			<input
				class="hp-input"
				type="date"
				name="date_to"
				value={data.dateTo}
				data-testid="admin-invoice-filter-to"
			/>
		</div>
		<button class="hp-btn hp-btn-primary" type="submit" data-testid="admin-invoice-filter-submit">
			<i class="fas fa-search"></i>Filter
		</button>
	</form>
{/if}

<div class="hp-count" style="margin-bottom:8px">
	{data.total} Records Found, Showing {from} to {to}
</div>

<div class="hp-scroll">
	<table class="hp-table">
		<thead>
			<tr>
				<th class="c" style="width:34px"
					><input
						type="checkbox"
						checked={allChecked}
						onchange={toggleAll}
						aria-label="Select all"
					/></th
				>
				<th
					><button class="hp-sort" onclick={() => sortBy('invoice_number')}
						>Invoice # {arrow('invoice_number')}</button
					></th
				>
				<th
					><button class="hp-sort" onclick={() => sortBy('client')}
						>Client Name {arrow('client')}</button
					></th
				>
				<th class="c"
					><button class="hp-sort" onclick={() => sortBy('created_at')}
						>Invoice Date {arrow('created_at')}</button
					></th
				>
				<th class="c"
					><button class="hp-sort" onclick={() => sortBy('due_date')}
						>Due Date {arrow('due_date')}</button
					></th
				>
				<th class="r"
					><button class="hp-sort" onclick={() => sortBy('total')}>Total {arrow('total')}</button
					></th
				>
				<th class="c" style="width:90px"
					><button class="hp-sort" onclick={() => sortBy('status')}>Status {arrow('status')}</button
					></th
				>
				<th style="width:140px"></th>
			</tr>
		</thead>
		<tbody>
			{#each rows as v (v.id)}
				<tr class="hp-row">
					<td class="c"
						><input
							type="checkbox"
							checked={checked.has(v.id)}
							onchange={() => toggle(v.id)}
							aria-label={`Select ${v.invoice_number}`}
						/></td
					>
					<td
						><a
							class="cell-link"
							href={`/admin/invoices/${v.id}`}
							data-testid={`row-invoice-${v.id}`}>{v.invoice_number}</a
						></td
					>
					<td class="c"
						><a class="cell-link" href={`/admin/clients/${v.client_id}`}>{clientLabel(v)}</a></td
					>
					<td class="c" style="color:#666">{fmtDate(v.created_at)}</td>
					<td class="c" style="color:#666">{fmtDate(v.due_date)}</td>
					<td class="r" style="color:#333">{idr(v.total)}</td>
					<td class="c"><span class={`hp-stext ${statusClass(v.status)}`}>{v.status}</span></td>
					<td class="c" style="white-space:nowrap">
						<a class="cell-link" href={`/admin/invoices/${v.id}`} style="padding:0 4px">View</a>
						<a class="cell-link" href={`/admin/invoices/${v.id}`} style="padding:0 4px">Edit</a>
						{#if v.status === 'paid'}
							<a class="cell-link" href={`/admin/invoices/${v.id}`} style="padding:0 4px">Refund</a>
						{:else if v.status !== 'cancelled'}
							<a class="cell-link" href={`/admin/invoices/${v.id}`} style="padding:0 4px">Cancel</a>
						{/if}
					</td>
				</tr>
			{:else}
				<tr
					><td colspan="8" style="text-align:center;padding:28px;color:#999">No invoices found</td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

<div class="hp-bulk">
	<span>With Selected:</span>
	<button class="hp-btn hp-btn-blue" onclick={() => bulk('Marked Paid')}>Mark Paid</button>
	<button class="hp-btn" onclick={() => bulk('Marked Unpaid')}>Mark Unpaid</button>
	<button class="hp-btn" onclick={() => bulk('Marked Cancelled')}>Mark Cancelled</button>
	<button class="hp-btn" onclick={() => bulk('Reminder Sent')}>Send Reminder</button>
	<button class="hp-btn hp-btn-danger" onclick={() => bulk('Deleted')}>Delete</button>
</div>

<HpPager page={data.page} perPage={data.perPage} total={data.total} />
