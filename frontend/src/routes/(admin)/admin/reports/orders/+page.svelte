<script lang="ts">
	import { appName } from '$lib/appName';
	import { formatIDR } from '$lib/money';
	import type { PageProps } from './$types';
	import BarChart from '../BarChart.svelte';

	let { data }: PageProps = $props();

	const money = (v: number) => formatIDR(v);

	const plain = (v: number) => new Intl.NumberFormat('id-ID').format(v);

	const chartData = $derived(data.series.map((p) => ({ label: p.period, value: p.count })));

	function statusClass(s: string): string {
		const v = s.toLowerCase();
		if (['completed', 'active', 'paid'].includes(v)) return 'green';
		if (['pending', 'awaiting_payment'].includes(v)) return 'orange';
		if (['cancelled', 'fraud', 'failed'].includes(v)) return 'red';
		return 'gray';
	}
</script>

<svelte:head>
	<title>Orders Report — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Orders Report</h1>

<div class="report-nav">
	<a class="hp-btn" href="/admin/reports/revenue"><i class="fas fa-chart-line"></i>Revenue</a>
	<a class="hp-btn hp-btn-primary" href="/admin/reports/orders"
		><i class="fas fa-shopping-cart"></i>Orders</a
	>
	<a class="hp-btn" href="/admin/reports/services"><i class="fas fa-server"></i>Services</a>
</div>

{#if data.errorMessage}
	<div class="hp-alert-yellow">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.errorMessage}
	</div>
{/if}

<form method="GET" action="/admin/reports/orders" class="hp-filter">
	<div style="flex:0 0 150px">
		<div class="hp-field-label">Date From</div>
		<input class="hp-input" type="date" name="from" value={data.from} data-testid="report-from" />
	</div>
	<div style="flex:0 0 150px">
		<div class="hp-field-label">Date To</div>
		<input class="hp-input" type="date" name="to" value={data.to} data-testid="report-to" />
	</div>
	<button class="hp-btn hp-btn-primary" type="submit" data-testid="report-run">
		<i class="fas fa-play"></i>Run Report
	</button>
</form>

<div class="report-stats-2">
	<div class="hp-stat" style="background:#337ab7">
		<div class="hp-stat-ico"><i class="fas fa-shopping-cart" aria-hidden="true"></i></div>
		<div class="hp-stat-bd">
			<div class="hp-stat-val">{plain(data.totalOrders)}</div>
			<div class="hp-stat-lbl">Total Orders</div>
		</div>
	</div>
	<div class="hp-stat" style="background:#46a546">
		<div class="hp-stat-ico"><i class="fas fa-money-bill-wave" aria-hidden="true"></i></div>
		<div class="hp-stat-bd">
			<div class="hp-stat-val">{money(data.totalValue)}</div>
			<div class="hp-stat-lbl">Total Value</div>
		</div>
	</div>
</div>

{#if data.series.length > 0}
	<div class="hp-panel" style="margin-bottom:20px">
		<div class="hp-panel-hd"><span class="title">Orders Over Time</span></div>
		<div style="padding:16px">
			<BarChart data={chartData} formatValue={plain} formatTick={plain} testid="orders-chart" />
		</div>
	</div>
{/if}

<div class="hp-panel">
	<div class="hp-panel-hd"><span class="title">By Status</span></div>
	<div class="hp-scroll">
		<table class="hp-table" data-testid="orders-status-table">
			<thead>
				<tr>
					<th>Status</th>
					<th class="r">Count</th>
					<th class="r">Total Value</th>
				</tr>
			</thead>
			<tbody>
				{#if data.byStatus.length === 0}
					<tr>
						<td colspan="3" style="text-align:center;padding:24px;color:#999">No data available</td>
					</tr>
				{:else}
					{#each data.byStatus as row (row.status)}
						<tr class="hp-row">
							<td><span class={`hp-stext ${statusClass(row.status)}`}>{row.status}</span></td>
							<td class="r">{row.count}</td>
							<td class="r">{row.value !== undefined ? money(row.value) : '—'}</td>
						</tr>
					{/each}
				{/if}
			</tbody>
		</table>
	</div>
</div>

<style>
	.report-nav {
		display: flex;
		gap: 8px;
		margin-bottom: 16px;
		flex-wrap: wrap;
	}
	.report-stats-2 {
		display: grid;
		grid-template-columns: repeat(2, 1fr);
		gap: 16px;
		margin-bottom: 16px;
	}
	@media (max-width: 560px) {
		.report-stats-2 {
			grid-template-columns: 1fr;
		}
	}
</style>
