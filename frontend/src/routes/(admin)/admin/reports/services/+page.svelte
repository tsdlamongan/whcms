<script lang="ts">
	import { appName } from '$lib/appName';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	const plain = (v: number) => new Intl.NumberFormat('id-ID').format(v);

	const maxProductCount = $derived(data.byProduct.reduce((m, r) => Math.max(m, r.count), 0));
	const maxStatusCount = $derived(data.byStatus.reduce((m, r) => Math.max(m, r.count), 0));

	function statusClass(s: string): string {
		const v = s.toLowerCase();
		if (v === 'active') return 'green';
		if (v === 'pending') return 'orange';
		if (['suspended', 'terminated', 'cancelled'].includes(v)) return 'red';
		return 'gray';
	}
</script>

<svelte:head>
	<title>Services Report — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Services Report</h1>

<div class="report-nav">
	<a class="hp-btn" href="/admin/reports/revenue"><i class="fas fa-chart-line"></i>Revenue</a>
	<a class="hp-btn" href="/admin/reports/orders"><i class="fas fa-shopping-cart"></i>Orders</a>
	<a class="hp-btn hp-btn-primary" href="/admin/reports/services"
		><i class="fas fa-server"></i>Services</a
	>
</div>

{#if data.errorMessage}
	<div class="hp-alert-yellow">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.errorMessage}
	</div>
{/if}

<div style="max-width:260px;margin-bottom:16px">
	<div class="hp-stat" style="background:#337ab7">
		<div class="hp-stat-ico"><i class="fas fa-server" aria-hidden="true"></i></div>
		<div class="hp-stat-bd">
			<div class="hp-stat-val">{plain(data.total)}</div>
			<div class="hp-stat-lbl">Total Services</div>
		</div>
	</div>
</div>

<div class="report-2col">
	<div class="hp-panel">
		<div class="hp-panel-hd"><span class="title">By Status</span></div>
		<div class="hp-scroll">
			<table class="hp-table" data-testid="services-status-table">
				<thead>
					<tr>
						<th>Status</th>
						<th class="r">Count</th>
						<th style="width:140px"></th>
					</tr>
				</thead>
				<tbody>
					{#if data.byStatus.length === 0}
						<tr>
							<td colspan="3" style="text-align:center;padding:24px;color:#999"
								>No data available</td
							>
						</tr>
					{:else}
						{#each data.byStatus as row (row.status)}
							{@const pct = maxStatusCount > 0 ? Math.round((row.count / maxStatusCount) * 100) : 0}
							<tr class="hp-row">
								<td><span class={`hp-stext ${statusClass(row.status)}`}>{row.status}</span></td>
								<td class="r">{row.count}</td>
								<td>
									<span
										style="display:block;height:8px;background:#e8f0f8;border-radius:4px;overflow:hidden"
									>
										<span style={`display:block;height:100%;background:#337ab7;width:${pct}%`}
										></span>
									</span>
								</td>
							</tr>
						{/each}
					{/if}
				</tbody>
			</table>
		</div>
	</div>

	<div class="hp-panel">
		<div class="hp-panel-hd"><span class="title">By Product</span></div>
		<div class="hp-scroll">
			<table class="hp-table" data-testid="services-product-table">
				<thead>
					<tr>
						<th>Product</th>
						<th class="r">Count</th>
						<th style="width:140px"></th>
					</tr>
				</thead>
				<tbody>
					{#if data.byProduct.length === 0}
						<tr>
							<td colspan="3" style="text-align:center;padding:24px;color:#999"
								>No data available</td
							>
						</tr>
					{:else}
						{#each data.byProduct as row (row.name)}
							{@const pct =
								maxProductCount > 0 ? Math.round((row.count / maxProductCount) * 100) : 0}
							<tr class="hp-row">
								<td>{row.name}</td>
								<td class="r">{row.count}</td>
								<td>
									<span
										style="display:block;height:8px;background:#e8f0f8;border-radius:4px;overflow:hidden"
									>
										<span style={`display:block;height:100%;background:#337ab7;width:${pct}%`}
										></span>
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
	.report-2col {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 20px;
	}
	@media (max-width: 860px) {
		.report-2col {
			grid-template-columns: 1fr;
		}
	}
</style>
