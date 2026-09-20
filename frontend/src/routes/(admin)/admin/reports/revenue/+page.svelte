<script lang="ts">
	import { appName } from '$lib/appName';
	import { formatIDR } from '$lib/money';
	import type { PageProps } from './$types';
	import BarChart from '../BarChart.svelte';

	let { data }: PageProps = $props();

	const money = (v: number) => formatIDR(v);

	// Compact form for the chart y-axis ticks only (e.g. "1,5 jt"); the ",-"
	// nominal is used everywhere a full amount is shown.
	const moneyCompact = (v: number) =>
		new Intl.NumberFormat('id-ID', {
			notation: 'compact',
			maximumFractionDigits: 1
		}).format(v);

	const chartData = $derived(data.series.map((p) => ({ label: p.period, value: p.amount })));
	const average = $derived(
		data.series.length > 0 ? Math.round(data.total / data.series.length) : 0
	);

	const csvHref = $derived(
		`/admin/reports/revenue/csv?from=${encodeURIComponent(data.from)}&to=${encodeURIComponent(data.to)}&group_by=${data.groupBy}`
	);
</script>

<svelte:head>
	<title>Revenue Report — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Revenue Report</h1>

<div class="report-nav">
	<a class="hp-btn hp-btn-primary" href="/admin/reports/revenue"
		><i class="fas fa-chart-line"></i>Revenue</a
	>
	<a class="hp-btn" href="/admin/reports/orders"><i class="fas fa-shopping-cart"></i>Orders</a>
	<a class="hp-btn" href="/admin/reports/services"><i class="fas fa-server"></i>Services</a>
</div>

{#if data.errorMessage}
	<div class="hp-alert-yellow">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.errorMessage}
	</div>
{/if}

<form method="GET" action="/admin/reports/revenue" class="hp-filter">
	<div style="flex:0 0 150px">
		<div class="hp-field-label">Date From</div>
		<input class="hp-input" type="date" name="from" value={data.from} data-testid="report-from" />
	</div>
	<div style="flex:0 0 150px">
		<div class="hp-field-label">Date To</div>
		<input class="hp-input" type="date" name="to" value={data.to} data-testid="report-to" />
	</div>
	<div style="flex:0 0 140px">
		<div class="hp-field-label">Group By</div>
		<select class="hp-select" name="group_by" value={data.groupBy} data-testid="report-groupby">
			<option value="day">Day</option>
			<option value="month">Month</option>
		</select>
	</div>
	<button class="hp-btn hp-btn-primary" type="submit" data-testid="report-run">
		<i class="fas fa-play"></i>Run Report
	</button>
	<a class="hp-btn" style="margin-left:auto" href={csvHref} data-testid="revenue-csv-link">
		<i class="fas fa-download"></i>Export CSV
	</a>
</form>

<div class="report-stats-3">
	<div class="hp-stat" style="background:#46a546">
		<div class="hp-stat-ico"><i class="fas fa-money-bill-wave" aria-hidden="true"></i></div>
		<div class="hp-stat-bd">
			<div class="hp-stat-val">{money(data.total)}</div>
			<div class="hp-stat-lbl">Total Revenue</div>
		</div>
	</div>
	<div class="hp-stat" style="background:#337ab7">
		<div class="hp-stat-ico"><i class="fas fa-receipt" aria-hidden="true"></i></div>
		<div class="hp-stat-bd">
			<div class="hp-stat-val">{data.txCount}</div>
			<div class="hp-stat-lbl">Transactions</div>
		</div>
	</div>
	<div class="hp-stat" style="background:#8a8a8a">
		<div class="hp-stat-ico"><i class="fas fa-chart-bar" aria-hidden="true"></i></div>
		<div class="hp-stat-bd">
			<div class="hp-stat-val">{money(average)}</div>
			<div class="hp-stat-lbl">{data.groupBy === 'month' ? 'Avg / Month' : 'Avg / Day'}</div>
		</div>
	</div>
</div>

<div class="hp-panel" style="margin-bottom:20px">
	<div class="hp-panel-hd"><span class="title">Revenue Over Time</span></div>
	<div style="padding:16px">
		<BarChart
			data={chartData}
			formatValue={money}
			formatTick={moneyCompact}
			testid="revenue-chart"
		/>
	</div>
</div>

<div class="report-2col">
	<div class="hp-panel">
		<div class="hp-panel-hd"><span class="title">Totals</span></div>
		<div class="hp-scroll">
			<table class="hp-table" data-testid="revenue-totals-table">
				<thead>
					<tr>
						<th>Period</th>
						<th class="r">Amount</th>
						<th class="r">Count</th>
					</tr>
				</thead>
				<tbody>
					{#if data.series.length === 0}
						<tr>
							<td colspan="3" style="text-align:center;padding:24px;color:#999"
								>No data available</td
							>
						</tr>
					{:else}
						{#each data.series as point (point.period)}
							<tr class="hp-row">
								<td>{point.period}</td>
								<td class="r">{money(point.amount)}</td>
								<td class="r">{point.count ?? '—'}</td>
							</tr>
						{/each}
						<tr class="hp-group-row">
							<td>Total</td>
							<td class="r" data-testid="revenue-total">{money(data.total)}</td>
							<td class="r">{data.txCount}</td>
						</tr>
					{/if}
				</tbody>
			</table>
		</div>
	</div>

	<div class="hp-panel">
		<div class="hp-panel-hd"><span class="title">Gateway Split</span></div>
		<div class="hp-scroll">
			<table class="hp-table" data-testid="revenue-gateway-table">
				<thead>
					<tr>
						<th>Gateway</th>
						<th class="r">Amount</th>
						<th class="r">Share</th>
					</tr>
				</thead>
				<tbody>
					{#if data.byGateway.length === 0}
						<tr>
							<td colspan="3" style="text-align:center;padding:24px;color:#999"
								>No data available</td
							>
						</tr>
					{:else}
						{#each data.byGateway as g (g.gateway)}
							{@const share = data.total > 0 ? Math.round((g.amount / data.total) * 100) : 0}
							<tr class="hp-row">
								<td>{g.gateway}</td>
								<td class="r">{money(g.amount)}</td>
								<td class="r">
									<span
										style="display:inline-flex;align-items:center;justify-content:flex-end;gap:8px"
									>
										<span style="font-variant-numeric:tabular-nums">{share}%</span>
										<span
											style="display:inline-block;width:70px;height:8px;background:#e8f0f8;border-radius:4px;overflow:hidden"
										>
											<span style={`display:block;height:100%;background:#337ab7;width:${share}%`}
											></span>
										</span>
									</span>
								</td>
							</tr>
						{/each}
					{/if}
				</tbody>
			</table>
		</div>
	</div>
</div>

<style>
	.report-nav {
		display: flex;
		gap: 8px;
		margin-bottom: 16px;
		flex-wrap: wrap;
	}
	.report-stats-3 {
		display: grid;
		grid-template-columns: repeat(3, 1fr);
		gap: 16px;
		margin-bottom: 16px;
	}
	.report-2col {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 20px;
	}
	@media (max-width: 860px) {
		.report-stats-3 {
			grid-template-columns: repeat(2, 1fr);
		}
		.report-2col {
			grid-template-columns: 1fr;
		}
	}
	@media (max-width: 560px) {
		.report-stats-3 {
			grid-template-columns: 1fr;
		}
	}
</style>
