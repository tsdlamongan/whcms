<script lang="ts">
	import HpPager from '$lib/components/hp/HpPager.svelte';
	import { fmtDate } from '$lib/date';
	import { formatIDR } from '$lib/money';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	const statuses = ['pending', 'active', 'pending_transfer', 'expired', 'cancelled'] as const;

	function hasActiveFilters(): boolean {
		return !!(data.search || data.status);
	}
	let filterOpen = $state(hasActiveFilters());

	const total = $derived(data.meta?.total ?? data.domains.length);
	const curPage = $derived(data.meta?.page ?? data.page);
	const perPage = $derived(data.meta?.per_page ?? data.perPage);
	const from = $derived(total === 0 ? 0 : (curPage - 1) * perPage + 1);
	const to = $derived(Math.min(curPage * perPage, total));

	const idr = (n: number) => formatIDR(n);
	function statusClass(s: string): string {
		if (s === 'active') return 'active';
		if (s === 'pending' || s === 'pending_transfer') return 'pending';
		if (s === 'expired') return 'terminated';
		return 'cancelled';
	}
</script>

<svelte:head>
	<title>Domains — HostPanel Admin</title>
</svelte:head>

<h1 class="hp-h1">Domains/TLDs</h1>

<div class="hp-info">
	<i class="fas fa-info-circle" style="margin-right:8px"></i>Registered domains across all clients.
	Configure TLD pricing and registrars from
	<a href="/admin/registrars" style="color:#337AB7;font-weight:600">Domain Registrars</a>.
</div>

{#if data.listError}
	<div class="hp-alert-yellow" data-testid="domain-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

<div style="margin-bottom:10px;display:flex;gap:8px">
	<a href="/admin/domains/new" class="hp-btn hp-btn-primary" data-testid="domain-add-new">
		<i class="fas fa-plus"></i>Add Existing Domain
	</a>
	<button class="hp-btn" onclick={() => (filterOpen = !filterOpen)}>Search/Filter</button>
</div>

{#if filterOpen}
	<form method="get" class="hp-filter" data-testid="domain-filter-form">
		<div class="grow">
			<div class="hp-field-label">Search</div>
			<input
				class="hp-input"
				type="search"
				name="search"
				value={data.search}
				data-testid="domain-search-input"
			/>
		</div>
		<div style="flex:0 0 170px">
			<div class="hp-field-label">Status</div>
			<select class="hp-select" name="status" data-testid="domain-status-filter">
				<option value="" selected={data.status === ''}>Any</option>
				{#each statuses as s (s)}
					<option value={s} selected={data.status === s}>{s}</option>
				{/each}
			</select>
		</div>
		<button class="hp-btn hp-btn-primary" type="submit" data-testid="domain-filter-submit"
			><i class="fas fa-search"></i>Filter</button
		>
	</form>
{/if}

<div class="hp-count" style="margin-bottom:8px">{total} Records Found, Showing {from} to {to}</div>

<div class="hp-scroll">
	<table class="hp-table">
		<thead>
			<tr>
				<th style="width:60px">ID</th>
				<th>Domain</th>
				<th>Client</th>
				<th class="c" style="width:110px">Status</th>
				<th class="c" style="width:110px">Expiry</th>
				<th class="c" style="width:110px">Next Due</th>
				<th class="r" style="width:120px">Price</th>
				<th class="c" style="width:100px">Auto Renew</th>
			</tr>
		</thead>
		<tbody>
			{#each data.domains as dm (dm.id)}
				<tr class="hp-row">
					<td>{dm.id}</td>
					<td
						><a
							class="cell-link"
							href={`/admin/domains/${dm.id}`}
							data-testid={`row-domain-${dm.id}`}>{dm.name}</a
						></td
					>
					<td
						><a class="cell-link" href={`/admin/clients/${dm.client_id}`}
							>{dm.client_name ?? `#${dm.client_id}`}</a
						></td
					>
					<td class="c"><span class={`hp-badge ${statusClass(dm.status)}`}>{dm.status}</span></td>
					<td class="c" style="color:#666">{fmtDate(dm.expiry_date)}</td>
					<td class="c" style="color:#666">{fmtDate(dm.next_due_date)}</td>
					<td class="r">{idr(dm.recurring_amount)}</td>
					<td class="c" style="color:#555">{dm.auto_renew ? 'Yes' : 'No'}</td>
				</tr>
			{:else}
				<tr
					><td colspan="8" style="text-align:center;padding:28px;color:#999">No domains found</td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

<HpPager page={curPage} {perPage} {total} />
