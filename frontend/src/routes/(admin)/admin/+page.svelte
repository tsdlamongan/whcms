<script lang="ts">
	import { appName } from '$lib/appName';
	import { invalidateAll } from '$app/navigation';
	import HpPanel from '$lib/components/hp/HpPanel.svelte';
	import HpStatCard from '$lib/components/hp/HpStatCard.svelte';
	import { fmtDate, fmtDateTime, fmtShortDate } from '$lib/date';
	import { formatIDR } from '$lib/money';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	// The KPIs/recent-activity half of this page's data is cached server-side
	// for 60s (Redis) - a plain invalidateAll() can still replay a stale hit
	// within that window. Every panel's Refresh button uses this instead: it
	// busts the cache via a one-shot proxy call, then reloads, so "Refresh"
	// always means "recompute from the database right now."
	async function hardRefresh() {
		await fetch('/admin/dashboard-refresh', { method: 'POST' });
		await invalidateAll();
	}

	const d = $derived(data.dashboard);
	const stats = $derived(d?.stats);
	const extra = $derived(d?.extra);
	const recentOrders = $derived(d?.recent_orders ?? []);
	const recentTickets = $derived(d?.recent_tickets ?? []);
	const onlineStaff = $derived(data.onlineStaff ?? []);
	const moduleActionsPending = $derived(d?.module_actions_pending ?? 0);

	const idr = (amount: number | undefined) => formatIDR(amount);
	function num(v: number | undefined): number {
		return v ?? 0;
	}
	function statusClass(status: string): string {
		const s = status.toLowerCase();
		if (['active', 'paid', 'completed', 'success'].includes(s)) return 'green';
		if (['unpaid', 'pending', 'overdue', 'suspended', 'terminated', 'failed'].includes(s))
			return 'red';
		if (['answered', 'in_progress', 'on_hold'].includes(s)) return 'orange';
		return 'gray';
	}

	// Real per-day series (last N days) for the System Overview chart, sourced
	// from the same /admin/reports/revenue and /admin/reports/orders endpoints
	// the Reports pages use. Both queries only return rows for days that had
	// at least one order/payment, so we "densify" against the full calendar
	// window here (missing day -> 0), otherwise the line would skip gaps.
	const chartDates = $derived.by(() => {
		const days = data.chartWindowDays ?? 14;
		const dates: string[] = [];
		const end = new Date();
		for (let i = days - 1; i >= 0; i--) {
			dates.push(new Date(end.getTime() - i * 86400000).toISOString().slice(0, 10));
		}
		return dates;
	});
	function seriesByDate(
		points: { period: string; amount?: number; count?: number }[],
		dates: string[],
		key: 'amount' | 'count'
	): number[] {
		const byPeriod = new Map(points.map((p) => [p.period, p[key] ?? 0]));
		return dates.map((day) => byPeriod.get(day) ?? 0);
	}
	const ordersValues = $derived(seriesByDate(data.ordersSeries ?? [], chartDates, 'count'));
	const incomeValues = $derived(seriesByDate(data.revenueSeries ?? [], chartDates, 'amount'));

	// Plot area: x in [120,840], y in [80,240] (240 = baseline). Each series is
	// scaled independently to its own max - this is a trend sparkline, not a
	// labeled dual-axis chart, so Orders (small counts) and Income (rupiah,
	// much larger) are shown on comparable visual scale rather than one
	// series being flattened by the other's magnitude.
	function scalePoints(values: number[]): { x: number; y: number }[] {
		const n = values.length;
		const max = Math.max(...values, 1);
		const xStep = n > 1 ? 720 / (n - 1) : 0;
		return values.map((v, i) => ({ x: 120 + i * xStep, y: 240 - (v / max) * 160 }));
	}
	function toPolyline(pts: { x: number; y: number }[]): string {
		return pts.map((p) => `${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(' ');
	}
	const ordersPoints = $derived(scalePoints(ordersValues));
	const incomePoints = $derived(scalePoints(incomeValues));
	const ordersLine = $derived(toPolyline(ordersPoints));
	const incomeLine = $derived(toPolyline(incomePoints));
	const ordersArea = $derived(
		ordersPoints.length
			? `${ordersLine} ${ordersPoints[ordersPoints.length - 1].x.toFixed(1)},240 ${ordersPoints[0].x.toFixed(1)},240`
			: ''
	);

	// Aggregate totals for the period, shown as plain text next to the legend
	// so the chart states concrete numbers even though the two lines
	// themselves are independently-scaled trend shapes, not a labeled axis.
	const totalOrders = $derived(ordersValues.reduce((sum, v) => sum + v, 0));
	const totalIncome = $derived(incomeValues.reduce((sum, v) => sum + v, 0));

	// A handful of evenly-spaced x-axis date labels (always incl. first/last)
	// rather than one per day, which would be unreadably cramped at 14+ points.
	const axisLabelIndices = $derived.by(() => {
		const n = chartDates.length;
		if (n <= 1) return [0];
		const want = Math.min(5, n);
		const step = (n - 1) / (want - 1);
		const idxs = new Set<number>();
		for (let i = 0; i < want; i++) idxs.add(Math.round(i * step));
		return Array.from(idxs).sort((a, b) => a - b);
	});

	// Hover/tap crosshair + tooltip: the chart shows two independently-scaled
	// trend lines with no value axis, so without this there is no way at all
	// to read an exact number off it - the tooltip is what makes each point
	// informative rather than purely decorative.
	let hoverIndex = $state<number | null>(null);
	let chartSvg: SVGSVGElement | undefined = $state();

	function updateHoverFromClientX(clientX: number) {
		if (!chartSvg || ordersPoints.length === 0) return;
		const rect = chartSvg.getBoundingClientRect();
		if (rect.width === 0) return;
		const svgX = ((clientX - rect.left) / rect.width) * 900;
		const n = ordersPoints.length;
		const xStep = n > 1 ? 720 / (n - 1) : 0;
		let idx = xStep > 0 ? Math.round((svgX - 120) / xStep) : 0;
		idx = Math.max(0, Math.min(n - 1, idx));
		hoverIndex = idx;
	}
	function onChartPointerMove(e: PointerEvent) {
		updateHoverFromClientX(e.clientX);
	}
	function onChartPointerLeave() {
		hoverIndex = null;
	}
	const hoverPct = $derived(
		hoverIndex !== null && ordersPoints[hoverIndex]
			? (ordersPoints[hoverIndex].x / 900) * 100
			: null
	);

	// Automation Overview: WHMCS's real widget reports counts from the last
	// cron run specifically (invoices created / cards captured / suspensions /
	// etc this backend doesn't track historically), which this backend has no
	// equivalent for without persisting per-cron-run history. Rather than
	// showing fabricated numbers under those labels, this shows six REAL,
	// currently-uncovered-elsewhere operational KPIs from the same dashboard
	// payload instead.
	const automation = $derived([
		{ label: 'Orders Today', value: num(extra?.orders_today), color: '#5b9bd5' },
		{ label: 'Services Suspended', value: num(stats?.services_suspended), color: '#e6a23c' },
		{ label: 'Invoices Overdue', value: num(stats?.invoices_overdue), color: '#dc3c7d' },
		{ label: 'Open Tickets', value: num(stats?.tickets_open), color: '#9e9e9e' },
		{ label: 'Services Pending', value: num(extra?.services_pending), color: '#9b59b6' },
		{ label: 'Active Domains', value: num(stats?.domains_active), color: '#8bc34a' }
	]);
</script>

<svelte:head>
	<title>Dashboard — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Dashboard</h1>

{#if data.dashboardError}
	<div class="hp-alert-yellow" data-testid="dashboard-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.dashboardError}
	</div>
{/if}

<div class="hp-grid4">
	<div data-testid="stat-pending-provisioning">
		<HpStatCard
			value={num(extra?.services_pending)}
			label="Pending Provisioning"
			icon="fas fa-shopping-cart"
			color="#64b843"
			href="/admin/services?status=pending"
		/>
	</div>
	<div data-testid="stat-open-tickets">
		<HpStatCard
			value={num(stats?.tickets_open)}
			label="Tickets Waiting"
			icon="fas fa-comment"
			color="#dc3c7d"
			href="/admin/tickets?status=open"
		/>
	</div>
	<div data-testid="stat-overdue-invoices">
		<HpStatCard
			value={num(stats?.invoices_overdue)}
			label="Overdue Invoices"
			icon="fas fa-exclamation-triangle"
			color="#56bfbd"
			href="/admin/invoices?status=overdue"
		/>
	</div>
	<div data-testid="stat-module-actions-pending">
		<HpStatCard
			value={moduleActionsPending}
			label="Pending Module Actions"
			icon="fas fa-redo"
			color="#e6a23c"
			href="/admin/logs/queue"
		/>
	</div>
</div>

<div class="hp-main2">
	<div class="hp-col">
		<HpPanel title="System Overview" onRefresh={hardRefresh}>
			<div class="chart-legend">
				<span
					><span class="swatch" style="background:#7ea6d8"></span>Orders — {totalOrders} total</span
				>
				<span
					><span class="swatch" style="background:#8bc34a"></span>Income — {idr(totalIncome)} total</span
				>
			</div>
			<div class="chart-subtitle">
				Last {data.chartWindowDays ?? 14} days · hover or tap to see a day's numbers
			</div>
			<div class="chart-wrap">
				<svg
					viewBox="0 0 900 280"
					width="100%"
					style="display:block"
					role="img"
					aria-label="Orders and income for the last {data.chartWindowDays ?? 14} days"
					bind:this={chartSvg}
					onpointermove={onChartPointerMove}
					onpointerleave={onChartPointerLeave}
					onpointerdown={onChartPointerMove}
				>
					{#each [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10] as i (i)}
						<line
							x1="40"
							y1={20 + i * 22}
							x2="860"
							y2={20 + i * 22}
							stroke="#eee"
							stroke-width="1"
						/>
					{/each}
					<polygon points={ordersArea} fill="rgba(126,166,216,.25)" />
					<polyline points={ordersLine} fill="none" stroke="#7ea6d8" stroke-width="2" />
					<polyline points={incomeLine} fill="none" stroke="#8bc34a" stroke-width="2" />
					<line x1="40" y1="240" x2="860" y2="240" stroke="#ccc" />

					{#each axisLabelIndices as i (i)}
						<text
							x={ordersPoints[i]?.x ?? 0}
							y="258"
							font-size="12"
							fill="#999"
							text-anchor="middle"
						>
							{fmtShortDate(chartDates[i])}
						</text>
					{/each}

					{#if hoverIndex !== null && ordersPoints[hoverIndex] && incomePoints[hoverIndex]}
						<line
							x1={ordersPoints[hoverIndex].x}
							y1="14"
							x2={ordersPoints[hoverIndex].x}
							y2="240"
							stroke="#bbb"
							stroke-width="1"
							stroke-dasharray="3,3"
						/>
						<circle
							cx={ordersPoints[hoverIndex].x}
							cy={ordersPoints[hoverIndex].y}
							r="4.5"
							fill="#fff"
							stroke="#7ea6d8"
							stroke-width="2"
						/>
						<circle
							cx={incomePoints[hoverIndex].x}
							cy={incomePoints[hoverIndex].y}
							r="4.5"
							fill="#fff"
							stroke="#8bc34a"
							stroke-width="2"
						/>
					{/if}
				</svg>

				{#if hoverIndex !== null && hoverPct !== null}
					<div
						class="chart-tooltip"
						style={`left:${hoverPct}%`}
						class:tooltip-right={(hoverPct ?? 0) > 65}
					>
						<div class="tooltip-date">{fmtShortDate(chartDates[hoverIndex])}</div>
						<div>
							<span class="swatch" style="background:#7ea6d8"></span>Orders:
							<b>{ordersValues[hoverIndex]}</b>
						</div>
						<div>
							<span class="swatch" style="background:#8bc34a"></span>Income:
							<b>{idr(incomeValues[hoverIndex])}</b>
						</div>
					</div>
				{/if}
			</div>
		</HpPanel>

		<HpPanel title="Billing" onRefresh={hardRefresh}>
			<div style="display:grid;grid-template-columns:1fr 1fr;gap:6px">
				<div style="padding:6px 12px" data-testid="stat-income-today">
					<div style="font-size:26px;font-weight:700;color:#43a047">
						{idr(stats?.revenue_today)}
					</div>
					<div style="color:#888;font-size:12px">Today</div>
				</div>
				<div style="padding:6px 12px" data-testid="stat-income-month">
					<div style="font-size:26px;font-weight:700;color:#e6a23c">
						{idr(stats?.revenue_this_month)}
					</div>
					<div style="color:#888;font-size:12px">This Month</div>
				</div>
				<div style="padding:6px 12px">
					<div style="font-size:26px;font-weight:700;color:#d9534f">{idr(extra?.unpaid_total)}</div>
					<div style="color:#888;font-size:12px">Unpaid</div>
				</div>
				<div style="padding:6px 12px">
					<div style="font-size:26px;font-weight:700;color:#337AB7">
						{idr(extra?.overdue_total)}
					</div>
					<div style="color:#888;font-size:12px">Overdue</div>
				</div>
			</div>
		</HpPanel>

		<section class="hp-panel" data-testid="recent-orders">
			<div class="hp-panel-hd">
				<span class="title">Recent Orders</span>
				<a href="/admin/orders" style="color:#337ab7;font-size:12px">View All</a>
			</div>
			{#if recentOrders.length === 0}
				<div style="padding:22px;text-align:center;color:#999">No recent orders</div>
			{:else}
				<div class="hp-scroll">
					<table class="hp-table" style="border:none">
						<tbody>
							{#each recentOrders as order (order.id)}
								<tr class="hp-row">
									<td
										><a class="cell-link" href={`/admin/orders/${order.id}`}>{order.order_number}</a
										></td
									>
									<td>{order.client_name ?? `#${order.client_id}`}</td>
									<td class="r">{idr(order.total)}</td>
									<td class="c"
										><span class={`hp-stext ${statusClass(order.status)}`}>{order.status}</span></td
									>
									<td class="c" style="color:#999">{fmtDate(order.created_at)}</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		</section>

		<section class="hp-panel">
			<div class="hp-panel-hd">
				<span class="title">Recent Tickets</span>
				<a href="/admin/tickets" style="color:#337ab7;font-size:12px">View All</a>
			</div>
			{#if recentTickets.length === 0}
				<div style="padding:22px;text-align:center;color:#999">No recent tickets</div>
			{:else}
				<div class="hp-scroll">
					<table class="hp-table" style="border:none">
						<tbody>
							{#each recentTickets as ticket (ticket.id)}
								<tr class="hp-row">
									<td
										><a class="cell-link" href={`/admin/tickets/${ticket.id}`}
											>{ticket.ticket_number}</a
										></td
									>
									<td
										style="max-width:220px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap"
										>{ticket.subject}</td
									>
									<td class="c"
										><span class={`hp-stext ${statusClass(ticket.status)}`}>{ticket.status}</span
										></td
									>
									<td class="c" style="color:#999"
										>{fmtDate(ticket.last_reply_at ?? ticket.created_at)}</td
									>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		</section>
	</div>

	<div class="hp-col">
		<HpPanel title="Automation Overview" flush onRefresh={hardRefresh}>
			<div style="display:grid;grid-template-columns:1fr 1fr;gap:2px;background:#f0f0f0">
				{#each automation as a (a.label)}
					<div style="background:#fff;padding:14px 16px;text-align:center">
						<div style="color:#888;font-size:12px">{a.label}</div>
						<div style={`font-size:22px;font-weight:700;color:${a.color}`}>{a.value}</div>
					</div>
				{/each}
			</div>
			<div style="padding:9px 12px;border-top:1px solid #f0f0f0;color:#888;font-size:12px">
				<i class="far fa-clock" style="margin-right:5px"></i>Dashboard data as of:
				<b style="color:#555">{fmtDateTime(d?.generated_at)}</b>
			</div>
		</HpPanel>

		<HpPanel title="Overview" onRefresh={hardRefresh}>
			<div style="display:grid;grid-template-columns:1fr 1fr;gap:10px">
				<div style="display:flex;align-items:center;gap:12px" data-testid="stat-active-services">
					<i class="fas fa-server" style="font-size:26px;color:#8bc34a"></i>
					<div>
						<div style="font-weight:700;color:#555">Active Services</div>
						<div style="color:#888">
							<b style="color:#333;font-size:16px">{num(stats?.services_active)}</b> Active
						</div>
					</div>
				</div>
				<div style="display:flex;align-items:center;gap:12px" data-testid="stat-orders-today">
					<i class="fas fa-shopping-cart" style="font-size:26px;color:#e6a23c"></i>
					<div>
						<div style="font-weight:700;color:#555">Orders Today</div>
						<div style="color:#888">
							<b style="color:#333;font-size:16px">{num(extra?.orders_today)}</b> New
						</div>
					</div>
				</div>
			</div>
		</HpPanel>

		<HpPanel title="Staff Online" onRefresh={hardRefresh}>
			{#if onlineStaff.length === 0}
				<div style="text-align:center;padding:8px">
					<i class="fas fa-user-circle" style="font-size:52px;color:#d0d0d0"></i>
					<div style="font-weight:600;color:#555;margin-top:8px">{data.user?.name ?? 'Admin'}</div>
					<div style="color:#999;font-size:12px">Online now</div>
				</div>
			{:else}
				<div
					style="display:flex;flex-direction:column;gap:10px;padding:4px 0"
					data-testid="online-staff-list"
				>
					{#each onlineStaff as s (s.user_id)}
						<div style="display:flex;align-items:center;gap:10px">
							<i class="fas fa-user-circle" style="font-size:28px;color:#8bc34a"></i>
							<div>
								<div style="font-weight:600;color:#555;font-size:13px">{s.email}</div>
								<div style="color:#999;font-size:11px;text-transform:capitalize">
									{s.role} · Online now
								</div>
							</div>
						</div>
					{/each}
				</div>
			{/if}
		</HpPanel>

		<HpPanel title="System Health" onRefresh={hardRefresh}>
			<div style="display:flex;align-items:center;justify-content:space-between">
				<div style="display:flex;align-items:center;gap:12px">
					<i class="fas fa-heartbeat" style="font-size:30px;color:#8bc34a"></i>
					<div>
						<div style="color:#888;font-size:12px">Overall Rating</div>
						<div style="font-size:20px;font-weight:700;color:#555">Good</div>
					</div>
				</div>
				<a class="hp-btn" href="/admin/logs/audit"><i class="fas fa-arrow-right"></i>View Issues</a>
			</div>
			<div style="height:14px;background:#8bc34a;border-radius:3px;margin:16px 0 10px"></div>
			<div style="display:flex;justify-content:space-between;font-size:12px">
				<span style="color:#e6a23c"
					><i class="fas fa-exclamation-triangle" style="margin-right:5px"></i>All systems
					operational</span
				>
			</div>
		</HpPanel>
	</div>
</div>

<style>
	.chart-legend {
		display: flex;
		justify-content: center;
		flex-wrap: wrap;
		gap: 22px;
		margin-bottom: 2px;
		font-size: 12px;
		color: #666;
	}
	.swatch {
		display: inline-block;
		width: 11px;
		height: 11px;
		margin-right: 5px;
		vertical-align: -1px;
		border-radius: 2px;
	}
	.chart-subtitle {
		text-align: center;
		margin-bottom: 6px;
		font-size: 11px;
		color: #aaa;
	}
	.chart-wrap {
		position: relative;
		touch-action: pan-y;
	}
	.chart-tooltip {
		position: absolute;
		top: 8px;
		transform: translateX(-50%);
		background: #fff;
		border: 1px solid #ddd;
		border-radius: 4px;
		box-shadow: 0 2px 8px rgba(0, 0, 0, 0.12);
		padding: 8px 10px;
		font-size: 12px;
		color: #444;
		white-space: nowrap;
		pointer-events: none;
		z-index: 5;
	}
	.chart-tooltip.tooltip-right {
		transform: translateX(-85%);
	}
	.chart-tooltip .tooltip-date {
		font-weight: 700;
		color: #333;
		margin-bottom: 3px;
	}
	@media (max-width: 560px) {
		.chart-legend {
			gap: 12px;
			font-size: 11px;
		}
		.chart-tooltip {
			font-size: 11px;
			padding: 6px 8px;
		}
	}
</style>
