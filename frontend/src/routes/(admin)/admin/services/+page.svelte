<script lang="ts">
	import DateText from '$lib/components/DateText.svelte';
	import HpPager from '$lib/components/hp/HpPager.svelte';
	import MoneyText from '$lib/components/MoneyText.svelte';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	const statuses = ['pending', 'active', 'suspended', 'terminated', 'cancelled'] as const;
	const cycleLabels: Record<string, string> = {
		one_time: 'One time',
		monthly: 'Monthly',
		quarterly: 'Quarterly',
		semiannually: 'Semi-annually',
		annually: 'Annually',
		biennially: 'Biennially'
	};

	function hasActiveFilters(): boolean {
		return !!(data.status || data.productId || data.serverId);
	}
	let filterOpen = $state(hasActiveFilters());

	const total = $derived(data.meta?.total ?? data.services.length);
	const curPage = $derived(data.meta?.page ?? data.page);
	const perPage = $derived(data.meta?.per_page ?? data.perPage);
	const from = $derived(total === 0 ? 0 : (curPage - 1) * perPage + 1);
	const to = $derived(Math.min(curPage * perPage, total));

	function statusClass(s: string): string {
		return (['active', 'pending', 'suspended', 'terminated', 'cancelled'] as string[]).includes(s)
			? s
			: 'cancelled';
	}
</script>

<svelte:head>
	<title>Services — HostPanel Admin</title>
</svelte:head>

<h1 class="hp-h1">Services</h1>

{#if data.listError}
	<div class="hp-alert-yellow" data-testid="service-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

<div style="margin-bottom:10px;display:flex;gap:8px">
	<a href="/admin/services/new" class="hp-btn hp-btn-primary" data-testid="service-add-new">
		<i class="fas fa-plus"></i>Add Existing Service
	</a>
	<button class="hp-btn" onclick={() => (filterOpen = !filterOpen)}>Search/Filter</button>
</div>

{#if filterOpen}
	<form method="get" class="hp-filter" data-testid="service-filter-form">
		<div style="flex:0 0 160px">
			<div class="hp-field-label">Status</div>
			<select class="hp-select" name="status" data-testid="service-status-filter">
				<option value="" selected={data.status === ''}>Any</option>
				{#each statuses as s (s)}
					<option value={s} selected={data.status === s}>{s[0].toUpperCase() + s.slice(1)}</option>
				{/each}
			</select>
		</div>
		<div style="flex:0 0 200px">
			<div class="hp-field-label">Product</div>
			<select class="hp-select" name="product_id" data-testid="service-product-filter">
				<option value="" selected={data.productId === ''}>All Products</option>
				{#each data.products as p (p.id)}
					<option value={String(p.id)} selected={data.productId === String(p.id)}>{p.name}</option>
				{/each}
			</select>
		</div>
		<div style="flex:0 0 200px">
			<div class="hp-field-label">Server</div>
			<select class="hp-select" name="server_id" data-testid="service-server-filter">
				<option value="" selected={data.serverId === ''}>All Servers</option>
				{#each data.servers as s (s.id)}
					<option value={String(s.id)} selected={data.serverId === String(s.id)}>{s.name}</option>
				{/each}
			</select>
		</div>
		<button class="hp-btn hp-btn-primary" type="submit" data-testid="service-filter-submit">
			<i class="fas fa-search"></i>Filter
		</button>
	</form>
{/if}

<div class="hp-count" style="margin-bottom:8px">{total} Records Found, Showing {from} to {to}</div>

<div class="hp-scroll">
	<table class="hp-table">
		<thead>
			<tr>
				<th style="width:60px">ID</th>
				<th>Product</th>
				<th>Domain</th>
				<th>Client</th>
				<th>Server</th>
				<th class="r" style="width:130px">Price</th>
				<th class="c" style="width:110px">Next Due</th>
				<th class="c" style="width:100px">Status</th>
			</tr>
		</thead>
		<tbody>
			{#each data.services as s (s.id)}
				<tr class="hp-row">
					<td>{s.id}</td>
					<td>
						<a
							class="cell-link"
							href={`/admin/services/${s.id}`}
							data-testid={`row-service-${s.id}`}
						>
							{s.product_name ?? `#${s.product_id}`}
						</a>
						<div style="font-size:11px;color:#999">
							{cycleLabels[s.billing_cycle] ?? s.billing_cycle}
						</div>
					</td>
					<td>{s.domain || '—'}</td>
					<td>
						<a class="cell-link" href={`/admin/clients/${s.client_id}`}>
							{s.client_name ?? `#${s.client_id}`}
						</a>
					</td>
					<td>
						{#if s.server_id}
							<a class="cell-link" href={`/admin/servers/${s.server_id}`}>
								{s.server_name ?? `#${s.server_id}`}
							</a>
						{:else}
							—
						{/if}
					</td>
					<td class="r"><MoneyText amount={s.recurring_amount ?? 0} /></td>
					<td class="c" style="color:#666"><DateText value={s.next_due_date} /></td>
					<td class="c"><span class={`hp-badge ${statusClass(s.status)}`}>{s.status}</span></td>
				</tr>
			{:else}
				<tr
					><td colspan="8" style="text-align:center;padding:28px;color:#999">No services found</td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

<HpPager page={curPage} {perPage} {total} />
