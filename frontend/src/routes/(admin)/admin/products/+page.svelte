<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import HpPager from '$lib/components/hp/HpPager.svelte';
	import { toast } from '$lib/stores/toast.svelte';
	import type { PageProps } from './$types';
	import { PRODUCT_TYPES, type ProductGroupRow, type ProductRow } from './catalog';

	let { data }: PageProps = $props();

	const total = $derived(data.meta?.total ?? data.products.length);
	const curPage = $derived(data.meta?.page ?? data.page);
	const perPage = $derived(data.meta?.per_page ?? data.perPage);
	const from = $derived(total === 0 ? 0 : (curPage - 1) * perPage + 1);
	const to = $derived(Math.min(curPage * perPage, total));

	const typeLabels: Record<string, string> = {
		shared_hosting: 'Shared Hosting',
		reseller_hosting: 'Reseller Hosting',
		domain: 'Domain',
		other: 'Other'
	};
	const setupLabels: Record<string, string> = {
		on_payment: 'After First Payment',
		on_order: 'On Order',
		manual: 'Manual'
	};

	// Local, reorderable copy of the (server-sorted) product groups - drag-and-drop
	// mutates this optimistically, then persists via the ?/reorderGroups action,
	// which re-runs `load` and resyncs this from the fresh `data.groups`.
	let localGroups = $state<ProductGroupRow[]>([]);
	$effect(() => {
		localGroups = [...data.groups];
	});

	const UNGROUPED_ID = -1;

	// Group the (server-filtered) products for the WHMCS grouped table.
	const grouped = $derived.by(() => {
		const byGroup = new Map<number, ProductRow[]>();
		for (const p of data.products) {
			const arr = byGroup.get(p.group_id) ?? [];
			arr.push(p);
			byGroup.set(p.group_id, arr);
		}
		const out: { id: number; name: string; items: ProductRow[] }[] = [];
		for (const g of localGroups) {
			const items = byGroup.get(g.id);
			if (items && items.length)
				out.push({ id: g.id, name: g.name + (g.hidden ? ' (Hidden)' : ''), items });
		}
		const known = new Set(localGroups.map((g) => g.id));
		const ungrouped = data.products.filter((p) => !known.has(p.group_id));
		if (ungrouped.length) out.push({ id: UNGROUPED_ID, name: 'Ungrouped', items: ungrouped });
		return out;
	});

	// drag-and-drop group reorder
	let draggedId = $state<number | null>(null);
	let dragOverId = $state<number | null>(null);
	let reorderForm = $state<HTMLFormElement | null>(null);
	let changesJson = $state('[]');

	function onGroupDragStart(id: number) {
		draggedId = id;
	}

	function onGroupDragOver(e: DragEvent, id: number) {
		if (draggedId === null || draggedId === id) return;
		e.preventDefault();
		if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
		dragOverId = id;
	}

	function onGroupDragLeave(id: number) {
		if (dragOverId === id) dragOverId = null;
	}

	function onGroupDrop(e: DragEvent, targetId: number) {
		e.preventDefault();
		dragOverId = null;
		const fromId = draggedId;
		draggedId = null;
		if (fromId === null || fromId === targetId) return;

		const fromIdx = localGroups.findIndex((g) => g.id === fromId);
		const toIdx = localGroups.findIndex((g) => g.id === targetId);
		if (fromIdx < 0 || toIdx < 0) return;

		const reordered = [...localGroups];
		const [moved] = reordered.splice(fromIdx, 1);
		reordered.splice(toIdx, 0, moved);
		localGroups = reordered;

		// Simplest correct approach: reassign sequential sort integers (0..N-1)
		// to ALL groups in their new visual order, then persist only the ones
		// whose sort value actually changed.
		const changes = reordered
			.map((g, idx) => ({ id: g.id, sort: idx }))
			.filter((c, idx) => reordered[idx].sort !== c.sort);
		if (changes.length === 0) return;

		changesJson = JSON.stringify(changes);
		reorderForm?.requestSubmit();
	}

	function onGroupDragEnd() {
		draggedId = null;
		dragOverId = null;
	}

	function hasActiveFilters(): boolean {
		return !!(data.search || data.groupId || data.type);
	}
	let filterOpen = $state(hasActiveFilters());

	const spotlight = [
		{
			icon: 'fas fa-shield-alt',
			color: '#337AB7',
			title: 'Add SSL Certificates',
			desc: "Sell SSLs from the world's premier high-assurance certificate provider."
		},
		{
			icon: 'fas fa-drafting-compass',
			color: '#3ba9e0',
			title: 'Add Website Builder',
			desc: 'Let customers build a website with a drag & drop site builder.'
		},
		{
			icon: 'fas fa-envelope-open-text',
			color: '#444',
			title: 'Add Email Security',
			desc: 'Offer professional email with Anti-Spam, Virus Protection & Archiving.'
		},
		{
			icon: 'fas fa-lock',
			color: '#000',
			title: 'Add Website Security',
			desc: 'Security & malware scanning, detection and removal plus WAF and CDN.'
		}
	];
</script>

<svelte:head>
	<title>Products/Services — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Products/Services</h1>

<p class="hp-lead">
	This is where you configure all your products and services. Each product must be assigned to a
	group which can be visible or hidden from the order page.
</p>

{#if data.listError}
	<div class="hp-alert-yellow" data-testid="product-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

<div style="display:flex;gap:8px;margin-bottom:16px;flex-wrap:wrap">
	<a class="hp-btn" href="/admin/product-groups"
		><i class="fas fa-plus" style="color:#5cb85c"></i>Create a New Group</a
	>
	<a class="hp-btn" href="/admin/products/new" data-testid="product-create-link"
		><i class="fas fa-plus-circle" style="color:#5cb85c"></i>Create a New Product</a
	>
	<button class="hp-btn" onclick={() => toast.info('Duplicate a product from its edit page')}
		><i class="fas fa-copy" style="color:#5b9bd5"></i>Duplicate a Product</button
	>
	<button class="hp-btn" onclick={() => (filterOpen = !filterOpen)}
		><i class="fas fa-search" style="color:#5b9bd5"></i>Search/Filter</button
	>
</div>

{#if filterOpen}
	<form method="get" class="hp-filter" data-testid="product-filter-form">
		<div class="grow">
			<div class="hp-field-label">Search</div>
			<input
				class="hp-input"
				type="search"
				name="search"
				value={data.search}
				data-testid="product-search-input"
			/>
		</div>
		<div style="flex:0 0 200px">
			<div class="hp-field-label">Group</div>
			<select class="hp-select" name="group_id" data-testid="product-group-filter">
				<option value="" selected={data.groupId === 0}>Any Group</option>
				{#each data.groups as g (g.id)}
					<option value={g.id} selected={data.groupId === g.id}>{g.name}</option>
				{/each}
			</select>
		</div>
		<div style="flex:0 0 170px">
			<div class="hp-field-label">Type</div>
			<select class="hp-select" name="type" data-testid="product-type-filter">
				<option value="" selected={data.type === ''}>Any Type</option>
				{#each PRODUCT_TYPES as pt (pt)}
					<option value={pt} selected={data.type === pt}>{typeLabels[pt]}</option>
				{/each}
			</select>
		</div>
		<button class="hp-btn hp-btn-primary" type="submit" data-testid="product-filter-submit"
			><i class="fas fa-search"></i>Filter</button
		>
	</form>
{/if}

<!-- Spotlight cards -->
<div
	style="display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:14px;margin-bottom:16px"
	class="hp-prod4"
>
	{#each spotlight as s (s.title)}
		<div style="border:1px solid #e2e2e2;border-radius:4px;padding:16px;display:flex;gap:12px">
			<i class={s.icon} style={`font-size:34px;color:${s.color}`}></i>
			<div>
				<div style="font-weight:600;color:#333;margin-bottom:3px">{s.title}</div>
				<div style="color:#888;font-size:11.5px;line-height:1.5">{s.desc}</div>
			</div>
		</div>
	{/each}
</div>

<div class="hp-count" style="margin-bottom:8px">{total} Records Found, Showing {from} to {to}</div>

<div class="hp-scroll">
	<table class="hp-table">
		<thead>
			<tr>
				<th>Product Name</th>
				<th class="c">Type</th>
				<th class="c">Module</th>
				<th class="c">Auto Setup</th>
				<th class="c">Stock</th>
				<th class="c">Visibility</th>
				<th style="width:80px"></th>
			</tr>
		</thead>
		<tbody>
			{#each grouped as g (g.id)}
				<tr
					class="hp-group-row"
					class:drag-over={dragOverId === g.id}
					draggable={g.id !== UNGROUPED_ID}
					data-testid={`group-row-${g.id}`}
					ondragstart={() => onGroupDragStart(g.id)}
					ondragover={(e) => onGroupDragOver(e, g.id)}
					ondragleave={() => onGroupDragLeave(g.id)}
					ondrop={(e) => onGroupDrop(e, g.id)}
					ondragend={onGroupDragEnd}
				>
					<td colspan="7">
						<i
							class="fas fa-arrows-alt"
							style={`color:#aaa;margin-right:8px${g.id !== UNGROUPED_ID ? ';cursor:grab' : ''}`}
						></i>Group Name: {g.name}
					</td>
				</tr>
				{#each g.items as p (p.id)}
					<tr class="hp-row">
						<td
							><a
								class="cell-link"
								href={`/admin/products/${p.id}`}
								data-testid={`row-product-${p.id}`}>{p.name}</a
							></td
						>
						<td class="c" style="color:#555">{typeLabels[p.type] ?? p.type}</td>
						<td class="c" style="color:#555">{p.module && p.module !== 'none' ? p.module : '—'}</td>
						<td class="c" style="color:#555">{setupLabels[p.auto_setup] ?? p.auto_setup}</td>
						<td class="c" style="color:#999">{p.stock_enabled ? p.stock_qty : 'Unlimited'}</td>
						<td class="c"
							><span class={`hp-badge ${p.hidden ? 'inactive' : 'active'}`}
								>{p.hidden ? 'Hidden' : 'Visible'}</span
							></td
						>
						<td class="c" style="white-space:nowrap">
							<a class="cell-link" href={`/admin/products/${p.id}`} title="Edit"
								><i class="far fa-edit" style="color:#5b9bd5"></i></a
							>
						</td>
					</tr>
				{/each}
			{:else}
				<tr
					><td colspan="7" style="text-align:center;padding:28px;color:#999">No products found</td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

<HpPager page={curPage} {perPage} {total} />

<!-- Hidden form that persists a drag-and-drop group reorder (see onGroupDrop). -->
<form
	method="POST"
	action="?/reorderGroups"
	class="hidden"
	bind:this={reorderForm}
	data-testid="group-reorder-form"
	use:enhance={() => {
		return async ({ result, update }) => {
			if (result.type === 'success') toast.success('Group order updated');
			else if (result.type === 'failure')
				toast.error((result.data?.errorMessage as string) ?? 'Failed to save group order');
			await update({ reset: false });
		};
	}}
>
	<input type="hidden" name="changes" value={changesJson} />
</form>

<style>
	.hidden {
		display: none;
	}
	:global(.hp-group-row[draggable='true']) {
		cursor: grab;
	}
	:global(.hp-group-row.drag-over) {
		outline: 2px dashed #337ab7;
		outline-offset: -2px;
	}
</style>
