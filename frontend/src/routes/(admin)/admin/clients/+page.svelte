<script lang="ts">
	import { appName } from '$lib/appName';
	import HpModal from '$lib/components/hp/HpModal.svelte';
	import HpPager from '$lib/components/hp/HpPager.svelte';
	import { fmtDate } from '$lib/date';
	import { formatIDR } from '$lib/money';
	import { toast } from '$lib/stores/toast.svelte';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	type Row = (typeof data.clients)[number];

	// client-side sort of the current page
	let sortCol = $state('id');
	let sortDir = $state<'asc' | 'desc'>('desc');

	const accessors: Record<string, (r: Row) => string | number> = {
		id: (r) => r.id,
		first: (r) => (r.first_name ?? '').toLowerCase(),
		last: (r) => (r.last_name ?? '').toLowerCase(),
		company: (r) => (r.company ?? '').toLowerCase(),
		email: (r) => (r.email ?? '').toLowerCase(),
		credit: (r) => r.credit_balance ?? 0,
		created: (r) => r.created_at ?? '',
		status: (r) => (r.status ?? '').toLowerCase()
	};

	const rows = $derived.by(() => {
		const acc = accessors[sortCol] ?? accessors.id;
		const dir = sortDir === 'asc' ? 1 : -1;
		return [...data.clients].sort((a, b) => {
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

	let msgOpen = $state(false);

	// pagination
	const total = $derived(data.meta?.total ?? data.clients.length);
	const curPage = $derived(data.meta?.page ?? data.page);
	const perPage = $derived(data.meta?.per_page ?? data.perPage);
	const from = $derived(total === 0 ? 0 : (curPage - 1) * perPage + 1);
	const to = $derived(Math.min(curPage * perPage, total));

	const idr = (n: number) => formatIDR(n);
	function fullName(r: Row): string {
		return [r.first_name, r.last_name].filter(Boolean).join(' ') || `#${r.id}`;
	}
	function statusClass(s: string): string {
		return s.toLowerCase() === 'active'
			? 'active'
			: s.toLowerCase() === 'inactive'
				? 'inactive'
				: 'cancelled';
	}
</script>

<svelte:head>
	<title>Clients — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">View/Search Clients</h1>

{#if data.listError}
	<div class="hp-alert-yellow" data-testid="client-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

<!-- Filter bar -->
<form method="get" class="hp-filter" data-testid="client-filter-form">
	<div
		style="width:56px;height:56px;border-radius:50%;background:#1A4D80;display:flex;align-items:center;justify-content:center;color:#fff;font-size:24px;flex:0 0 auto"
	>
		<i class="fas fa-search" aria-hidden="true"></i>
	</div>
	<div class="grow">
		<div class="hp-field-label">Client/Company Name</div>
		<input
			class="hp-input"
			type="search"
			name="search"
			value={data.search}
			data-testid="client-search-input"
		/>
	</div>
	<div style="flex:0 0 150px">
		<div class="hp-field-label">Status</div>
		<select class="hp-select" name="status" data-testid="client-status-filter">
			<option value="" selected={data.status === ''}>Any</option>
			<option value="active" selected={data.status === 'active'}>Active</option>
			<option value="inactive" selected={data.status === 'inactive'}>Inactive</option>
			<option value="closed" selected={data.status === 'closed'}>Closed</option>
		</select>
	</div>
	<a class="hp-btn" href="/admin/clients/export.csv" data-testid="client-export-csv" download>
		<i class="fas fa-download"></i>Export
	</a>
	<button class="hp-btn hp-btn-primary" type="submit" data-testid="client-filter-submit">
		<i class="fas fa-search"></i>Search
	</button>
</form>

<div class="hp-listbar">
	<div class="hp-count">{total} Records Found, Showing {from} to {to}</div>
	<a class="hp-btn hp-btn-primary" href="/admin/clients/new" data-testid="client-create-link">
		<i class="fas fa-plus"></i>Add New Client
	</a>
</div>

<div class="hp-scroll">
	<table class="hp-table">
		<thead>
			<tr>
				<th class="c" style="width:34px">
					<input
						type="checkbox"
						checked={allChecked}
						onchange={toggleAll}
						aria-label="Select all"
					/>
				</th>
				<th style="width:70px"
					><button class="hp-sort" onclick={() => sortBy('id')}>ID {arrow('id')}</button></th
				>
				<th
					><button class="hp-sort" onclick={() => sortBy('first')}
						>First Name {arrow('first')}</button
					></th
				>
				<th
					><button class="hp-sort" onclick={() => sortBy('last')}>Last Name {arrow('last')}</button
					></th
				>
				<th
					><button class="hp-sort" onclick={() => sortBy('company')}
						>Company Name {arrow('company')}</button
					></th
				>
				<th
					><button class="hp-sort" onclick={() => sortBy('email')}
						>Email Address {arrow('email')}</button
					></th
				>
				<th class="r"
					><button class="hp-sort" onclick={() => sortBy('credit')}>Credit {arrow('credit')}</button
					></th
				>
				<th class="c" style="width:110px"
					><button class="hp-sort" onclick={() => sortBy('created')}
						>Created {arrow('created')}</button
					></th
				>
				<th class="c" style="width:90px"
					><button class="hp-sort" onclick={() => sortBy('status')}>Status {arrow('status')}</button
					></th
				>
			</tr>
		</thead>
		<tbody>
			{#each rows as c (c.id)}
				<tr class="hp-row">
					<td class="c"
						><input
							type="checkbox"
							checked={checked.has(c.id)}
							onchange={() => toggle(c.id)}
							aria-label={`Select ${c.id}`}
						/></td
					>
					<td
						><a class="cell-link" href={`/admin/clients/${c.id}`} data-testid={`row-client-${c.id}`}
							>{c.id}</a
						></td
					>
					<td><a class="cell-link" href={`/admin/clients/${c.id}`}>{c.first_name || '—'}</a></td>
					<td><a class="cell-link" href={`/admin/clients/${c.id}`}>{c.last_name || '—'}</a></td>
					<td>{c.company || '—'}</td>
					<td><a class="cell-link" href={`mailto:${c.email}`}>{c.email || '—'}</a></td>
					<td class="r">{idr(c.credit_balance)}</td>
					<td class="c" style="color:#666">{fmtDate(c.created_at)}</td>
					<td class="c"><span class={`hp-badge ${statusClass(c.status)}`}>{c.status}</span></td>
				</tr>
			{:else}
				<tr
					><td colspan="9" style="text-align:center;padding:28px;color:#999">No clients found</td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

<div class="hp-bulk">
	<span>With Selected:</span>
	<button
		class="hp-btn"
		onclick={() => (checked.size ? (msgOpen = true) : toast.info('Select at least one client'))}
	>
		Send Message
	</button>
</div>

<HpPager page={curPage} {perPage} {total} />

<HpModal open={msgOpen} title="Send Message" onClose={() => (msgOpen = false)}>
	<div style="color:#666;margin-bottom:10px">{checked.size} recipient(s) selected</div>
	<input class="hp-input" placeholder="Subject" style="margin-bottom:8px" />
	<textarea class="hp-textarea" placeholder="Message" rows="5"></textarea>
	<div style="text-align:right;margin-top:12px">
		<button
			class="hp-btn hp-btn-primary"
			onclick={() => {
				msgOpen = false;
				toast.success('Message sent');
			}}
		>
			Send Message
		</button>
	</div>
</HpModal>
