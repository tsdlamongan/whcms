<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import DateText from '$lib/components/DateText.svelte';
	import MoneyText from '$lib/components/MoneyText.svelte';
	import HpModal from '$lib/components/hp/HpModal.svelte';
	import HpPager from '$lib/components/hp/HpPager.svelte';
	import HpPanel from '$lib/components/hp/HpPanel.svelte';
	import HpStatCard from '$lib/components/hp/HpStatCard.svelte';
	import { toast } from '$lib/stores/toast.svelte';
	import { formatIDR } from '$lib/money';
	import type { PageProps } from './$types';
	import type { AdminTransaction } from '../invoices/types';

	let { data }: PageProps = $props();

	// Manual bank-transfer transactions awaiting admin confirmation
	// (POST /admin/transactions/:id/confirm) - only ever true for a
	// gateway=manual, status=pending row (backend 409s otherwise).
	function isPendingManual(tx: AdminTransaction): boolean {
		return tx.gateway === 'manual' && tx.status === 'pending';
	}
	let confirmId: number | null = $state(null);
	let confirmForm: HTMLFormElement | undefined = $state();
	let confirming = $state(false);

	function hasActiveFilters(): boolean {
		return !!(data.search || data.status || data.gateway || data.dateFrom || data.dateTo);
	}
	let filterOpen = $state(hasActiveFilters());

	// pagination
	const from = $derived(data.total === 0 ? 0 : (data.page - 1) * data.perPage + 1);
	const to = $derived(Math.min(data.page * data.perPage, data.total));

	function statusClass(s: string): string {
		if (s === 'success') return 'green';
		if (s === 'failed' || s === 'expired') return 'red';
		if (s === 'pending') return 'orange';
		return 'gray';
	}
	function cap(s: string): string {
		return s ? s[0].toUpperCase() + s.slice(1) : s;
	}
	const idr = (n: number) => formatIDR(n);

	// Stat totals + chart series, computed client-side from the loaded page.
	// The API surface (docs/CONTRACTS.md) has no admin/transactions summary or
	// series endpoint, so these are honest sums over the rows already fetched
	// for the current page/filter, the same "(this page)" scoping the Invoices
	// list's counter bar uses.
	const isRefund = (t: AdminTransaction) => t.status === 'refunded';
	const totalIncome = $derived(
		data.transactions.filter((t) => !isRefund(t)).reduce((s, t) => s + (t.amount ?? 0), 0)
	);
	const totalFees = $derived(data.transactions.reduce((s, t) => s + (t.fee ?? 0), 0));
	const totalExpenditure = $derived(
		data.transactions.filter(isRefund).reduce((s, t) => s + (t.amount ?? 0), 0)
	);

	const chartSeries = $derived.by(() => {
		const byDay = new Map<string, number>();
		for (const t of data.transactions) {
			if (isRefund(t)) continue;
			const day = (t.created_at ?? '').slice(0, 10);
			if (!day) continue;
			byDay.set(day, (byDay.get(day) ?? 0) + (t.amount ?? 0));
		}
		return [...byDay.entries()].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
	});
	const chart = $derived.by(() => {
		const n = chartSeries.length;
		if (n === 0)
			return { area: '', line: '', points: [] as { x: number; y: number; label: string }[] };
		const max = Math.max(1, ...chartSeries.map(([, v]) => v));
		const x0 = 40;
		const x1 = 860;
		const y0 = 240;
		const y1 = 20;
		const step = n > 1 ? (x1 - x0) / (n - 1) : 0;
		const points = chartSeries.map(([day, v], i) => ({
			x: n > 1 ? x0 + i * step : (x0 + x1) / 2,
			y: y0 - (v / max) * (y0 - y1),
			label: day
		}));
		const line = points.map((p) => `${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(' ');
		const lastX = points[points.length - 1]?.x ?? x1;
		const area = `${x0},${y0} ${line} ${lastX.toFixed(1)},${y0}`;
		return { area, line, points };
	});
</script>

<svelte:head>
	<title>Transactions — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Transactions</h1>

{#if data.errorMessage}
	<div class="hp-alert-yellow">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.errorMessage}
	</div>
{/if}

<!-- 3 stat cards: Total Income / Fees / Expenditure (this page) -->
<div
	style="display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:16px;margin-bottom:16px"
>
	<div data-testid="stat-total-income">
		<HpStatCard
			value={idr(totalIncome)}
			label="Total Income"
			icon="fas fa-arrow-up"
			color="#46a546"
		/>
	</div>
	<div data-testid="stat-total-fees">
		<HpStatCard value={idr(totalFees)} label="Total Fees" icon="fas fa-coins" color="#9e9e9e" />
	</div>
	<div data-testid="stat-total-expenditure">
		<HpStatCard
			value={idr(totalExpenditure)}
			label="Total Expenditure"
			icon="fas fa-arrow-down"
			color="#c43c35"
		/>
	</div>
</div>

<!-- green area chart: daily income over the loaded page -->
<div style="margin-bottom:16px">
	<HpPanel title="Income Overview">
		<div
			style="display:flex;justify-content:center;gap:22px;margin-bottom:8px;font-size:12px;color:#666"
		>
			<span>
				<span
					style="display:inline-block;width:11px;height:11px;background:#46a546;margin-right:5px;vertical-align:-1px"
				></span>Income
			</span>
		</div>
		{#if chart.points.length === 0}
			<div style="padding:40px;text-align:center;color:#999">
				No transaction activity to chart (this page)
			</div>
		{:else}
			<svg viewBox="0 0 900 260" width="100%" style="display:block" aria-hidden="true">
				{#each [0, 1, 2, 3, 4, 5] as i (i)}
					<line x1="40" y1={20 + i * 44} x2="860" y2={20 + i * 44} stroke="#eee" stroke-width="1" />
				{/each}
				<polygon points={chart.area} fill="rgba(70,165,70,.2)" />
				<polyline points={chart.line} fill="none" stroke="#46a546" stroke-width="2" />
				{#each chart.points as p (p.label)}
					<circle cx={p.x} cy={p.y} r="3" fill="#46a546" />
				{/each}
				<line x1="40" y1="240" x2="860" y2="240" stroke="#ccc" />
			</svg>
			<div style="text-align:center;font-size:11px;color:#999;margin-top:4px">
				Daily income, {chart.points[0].label} – {chart.points[chart.points.length - 1].label} (this page)
			</div>
		{/if}
	</HpPanel>
</div>

<div style="margin-bottom:10px">
	<button class="hp-btn" onclick={() => (filterOpen = !filterOpen)}>Search/Filter</button>
</div>

{#if filterOpen}
	<form
		method="GET"
		action="/admin/transactions"
		class="hp-filter"
		data-testid="transaction-filter-form"
	>
		<div class="grow">
			<div class="hp-field-label">Search</div>
			<input
				class="hp-input"
				type="search"
				name="search"
				value={data.search}
				placeholder="Reference / invoice #…"
				data-testid="transaction-search"
			/>
		</div>
		<div style="flex:0 0 140px">
			<div class="hp-field-label">Status</div>
			<select
				class="hp-select"
				name="status"
				value={data.status}
				data-testid="transaction-filter-status"
			>
				<option value="">Any</option>
				{#each data.statusOptions as s (s)}
					<option value={s}>{cap(s)}</option>
				{/each}
			</select>
		</div>
		<div style="flex:0 0 140px">
			<div class="hp-field-label">Gateway</div>
			<select
				class="hp-select"
				name="gateway"
				value={data.gateway}
				data-testid="transaction-filter-gateway"
			>
				<option value="">Any</option>
				{#each data.gatewayOptions as g (g)}
					<option value={g}>{cap(g)}</option>
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
				data-testid="transaction-filter-from"
			/>
		</div>
		<div style="flex:0 0 150px">
			<div class="hp-field-label">Date To</div>
			<input
				class="hp-input"
				type="date"
				name="date_to"
				value={data.dateTo}
				data-testid="transaction-filter-to"
			/>
		</div>
		<button class="hp-btn hp-btn-primary" type="submit" data-testid="transaction-filter-submit">
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
				<th>Client</th>
				<th class="c" style="width:140px">Date</th>
				<th>Payment Method</th>
				<th>Invoice</th>
				<th class="r" style="width:130px">Amount In</th>
				<th class="r" style="width:110px">Fees</th>
				<th class="r" style="width:130px">Amount Out</th>
				<th class="c" style="width:90px">Status</th>
				<th class="c" style="width:110px">Actions</th>
			</tr>
		</thead>
		<tbody>
			{#each data.transactions as tx (tx.id)}
				<tr class="hp-row">
					<td>{tx.client_name ?? '—'}</td>
					<td class="c" style="color:#666"><DateText value={tx.created_at} mode="datetime" /></td>
					<td>
						<div>{cap(tx.method_code || tx.gateway)}</div>
						<div style="font-size:11px;color:#999">
							{cap(tx.gateway)}{#if tx.merchant_order_id || tx.gateway_reference}
								·
								<span style="font-family:monospace"
									>{tx.merchant_order_id || tx.gateway_reference}</span
								>
							{/if}
						</div>
					</td>
					<td>
						<a
							class="cell-link"
							href={`/admin/invoices/${tx.invoice_id}`}
							data-testid={`row-transaction-${tx.id}`}
						>
							{tx.invoice_number ?? `#${tx.invoice_id}`}
						</a>
					</td>
					<td class="r">
						{#if tx.status !== 'refunded'}
							<MoneyText amount={tx.amount} />
						{:else}
							—
						{/if}
					</td>
					<td class="r"><MoneyText amount={tx.fee ?? 0} /></td>
					<td class="r">
						{#if tx.status === 'refunded'}
							<MoneyText amount={tx.amount} />
						{:else}
							—
						{/if}
					</td>
					<td class="c"><span class={`hp-stext ${statusClass(tx.status)}`}>{tx.status}</span></td>
					<td class="c">
						{#if isPendingManual(tx)}
							<button
								type="button"
								class="hp-btn hp-btn-green"
								style="padding:5px 10px"
								onclick={() => (confirmId = tx.id)}
								data-testid={`confirm-payment-${tx.id}`}
							>
								Confirm
							</button>
						{/if}
					</td>
				</tr>
			{:else}
				<tr
					><td colspan="9" style="text-align:center;padding:28px;color:#999"
						>No transactions found</td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

<HpPager page={data.page} perPage={data.perPage} total={data.total} />

<form
	method="POST"
	action="?/confirm"
	bind:this={confirmForm}
	use:enhance={() => {
		confirming = true;
		return async ({ result, update }) => {
			confirming = false;
			if (result.type === 'success') {
				confirmId = null;
				toast.success('Payment confirmed.');
			}
			await update();
		};
	}}
	class="hidden"
>
	<input type="hidden" name="id" value={confirmId ?? ''} />
</form>
<HpModal
	open={confirmId !== null}
	title="Confirm Bank Transfer Payment"
	onClose={() => (confirmId = null)}
>
	<p style="color:#666;margin-bottom:16px">
		Confirm that the funds for this transaction have arrived. This marks the invoice as paid and
		cannot be undone.
	</p>
	<div style="display:flex;justify-content:flex-end;gap:8px">
		<button type="button" class="hp-btn" onclick={() => (confirmId = null)}>Cancel</button>
		<button
			type="button"
			class="hp-btn hp-btn-green"
			disabled={confirming}
			onclick={() => confirmForm?.requestSubmit()}
			data-testid="confirm-payment-submit"
		>
			Confirm Payment
		</button>
	</div>
</HpModal>
