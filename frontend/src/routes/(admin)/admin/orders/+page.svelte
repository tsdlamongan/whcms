<script lang="ts">
	import { appName } from '$lib/appName';
	import DateText from '$lib/components/DateText.svelte';
	import HpPager from '$lib/components/hp/HpPager.svelte';
	import MoneyText from '$lib/components/MoneyText.svelte';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	const statuses = ['pending', 'active', 'fraud', 'cancelled'] as const;

	function hasActiveFilters(): boolean {
		return !!(data.search || data.status);
	}
	let filterOpen = $state(hasActiveFilters());

	const total = $derived(data.meta?.total ?? data.orders.length);
	const curPage = $derived(data.meta?.page ?? data.page);
	const perPage = $derived(data.meta?.per_page ?? data.perPage);
	const from = $derived(total === 0 ? 0 : (curPage - 1) * perPage + 1);
	const to = $derived(Math.min(curPage * perPage, total));

	function statusClass(s: string): string {
		return (['active', 'pending', 'fraud', 'cancelled'] as string[]).includes(s) ? s : 'cancelled';
	}
</script>

<svelte:head>
	<title>Orders — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Orders</h1>

{#if data.listError}
	<div class="hp-alert-yellow" data-testid="order-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

<div style="margin-bottom:10px">
	<button class="hp-btn" onclick={() => (filterOpen = !filterOpen)}>Search/Filter</button>
</div>

{#if filterOpen}
	<form method="get" class="hp-filter" data-testid="order-filter-form">
		<div class="grow">
			<div class="hp-field-label">Search</div>
			<input
				class="hp-input"
				type="search"
				name="search"
				value={data.search}
				placeholder="Search order number…"
				data-testid="order-search-input"
			/>
		</div>
		<div style="flex:0 0 160px">
			<div class="hp-field-label">Status</div>
			<select class="hp-select" name="status" data-testid="order-status-filter">
				<option value="" selected={data.status === ''}>Any</option>
				{#each statuses as s (s)}
					<option value={s} selected={data.status === s}>{s[0].toUpperCase() + s.slice(1)}</option>
				{/each}
			</select>
		</div>
		<button class="hp-btn hp-btn-primary" type="submit" data-testid="order-filter-submit">
			<i class="fas fa-search"></i>Filter
		</button>
	</form>
{/if}

<div class="hp-count" style="margin-bottom:8px">{total} Records Found, Showing {from} to {to}</div>

<div class="hp-scroll">
	<table class="hp-table">
		<thead>
			<tr>
				<th>Order #</th>
				<th>Client</th>
				<th class="c" style="width:130px">Date</th>
				<th class="r" style="width:150px">Total</th>
				<th class="c" style="width:100px">Status</th>
			</tr>
		</thead>
		<tbody>
			{#each data.orders as o (o.id)}
				<tr class="hp-row">
					<td>
						<a class="cell-link" href={`/admin/orders/${o.id}`} data-testid={`row-order-${o.id}`}>
							{o.order_number}
						</a>
					</td>
					<td>
						<a class="cell-link" href={`/admin/clients/${o.client_id}`}>
							{o.client_name ?? `#${o.client_id}`}
						</a>
					</td>
					<td class="c" style="color:#666"><DateText value={o.created_at} /></td>
					<td class="r"><MoneyText amount={o.total ?? 0} /></td>
					<td class="c"><span class={`hp-badge ${statusClass(o.status)}`}>{o.status}</span></td>
				</tr>
			{:else}
				<tr
					><td colspan="5" style="text-align:center;padding:28px;color:#999">No orders found</td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

<HpPager page={curPage} {perPage} {total} />
