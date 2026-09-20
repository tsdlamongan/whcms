<script lang="ts">
	import { appName } from '$lib/appName';
	import HpPager from '$lib/components/hp/HpPager.svelte';
	import { fmtDateTime } from '$lib/date';
	import { toast } from '$lib/stores/toast.svelte';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	type Row = (typeof data.tickets)[number];

	const priorityLabels: Record<string, string> = { low: 'Low', medium: 'Medium', high: 'High' };

	function departmentName(id: number): string {
		return data.departments.find((d) => d.id === id)?.name ?? `#${id}`;
	}
	function requestor(row: Row): string {
		return row.client_name ?? (row.client_id ? `#${row.client_id}` : 'Guest');
	}
	function statusClass(s: string): string {
		if (s === 'open') return 'green';
		if (s === 'closed') return 'gray';
		return 'orange';
	}
	function priorityClass(p: string): string {
		if (p === 'high') return 'red';
		if (p === 'medium') return 'orange';
		return 'gray';
	}
	const f = $derived(data.filters);
	let filterOpen = $state(false);

	function bulk(label: string) {
		toast.info(`Bulk action: ${label}`);
	}

	const total = $derived(data.meta?.total ?? data.tickets.length);
	const curPage = $derived(data.meta?.page ?? data.page);
	const perPage = $derived(data.meta?.per_page ?? data.perPage);
	const from = $derived(total === 0 ? 0 : (curPage - 1) * perPage + 1);
	const to = $derived(Math.min(curPage * perPage, total));
</script>

<svelte:head>
	<title>Support Tickets — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Support Tickets</h1>

{#if data.listError}
	<div class="hp-alert-yellow" data-testid="ticket-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

<div style="display:flex;gap:6px;margin-bottom:10px;flex-wrap:wrap">
	<button class="hp-btn" onclick={() => (filterOpen = !filterOpen)}>Search/Filter</button>
	<button class="hp-btn" onclick={() => toast.info('Auto-refresh toggled')}>Auto Refresh</button>
</div>

{#if filterOpen}
	<form method="get" class="hp-filter" data-testid="ticket-filter-form">
		<div class="grow">
			<div class="hp-field-label">Search</div>
			<input
				class="hp-input"
				type="search"
				name="search"
				value={f.search}
				data-testid="ticket-search-input"
			/>
		</div>
		<div style="flex:0 0 150px">
			<div class="hp-field-label">Status</div>
			<select class="hp-select" name="status" data-testid="ticket-status-filter">
				<option value="" selected={f.status === ''}>Any</option>
				{#each ['open', 'answered', 'customer_reply', 'on_hold', 'closed'] as s (s)}
					<option value={s} selected={f.status === s}>{s}</option>
				{/each}
			</select>
		</div>
		<div style="flex:0 0 170px">
			<div class="hp-field-label">Department</div>
			<select class="hp-select" name="department_id" data-testid="ticket-dept-filter">
				<option value="" selected={f.departmentId === ''}>Any</option>
				{#each data.departments as d (d.id)}
					<option value={String(d.id)} selected={f.departmentId === String(d.id)}>{d.name}</option>
				{/each}
			</select>
		</div>
		<div style="flex:0 0 130px">
			<div class="hp-field-label">Priority</div>
			<select class="hp-select" name="priority" data-testid="ticket-priority-filter">
				<option value="" selected={f.priority === ''}>Any</option>
				{#each ['low', 'medium', 'high'] as p (p)}
					<option value={p} selected={f.priority === p}>{priorityLabels[p]}</option>
				{/each}
			</select>
		</div>
		<div style="flex:0 0 180px">
			<div class="hp-field-label">Assigned</div>
			<select class="hp-select" name="assigned_user_id" data-testid="ticket-assigned-filter">
				<option value="" selected={f.assigned === ''}>Any</option>
				{#each data.staff as s (s.id)}
					<option value={String(s.id)} selected={f.assigned === String(s.id)}>{s.email}</option>
				{/each}
			</select>
		</div>
		<button class="hp-btn hp-btn-primary" type="submit" data-testid="ticket-filter-submit">
			<i class="fas fa-search"></i>Filter
		</button>
	</form>
{/if}

<div class="hp-count" style="margin-bottom:8px">{total} Records Found, Showing {from} to {to}</div>

<div class="hp-bulk" style="margin-top:0;margin-bottom:8px">
	<span>With Selected:</span>
	<button class="hp-btn" onclick={() => bulk('Pin')}>Pin</button>
	<button class="hp-btn" onclick={() => bulk('Unpin')}>Unpin</button>
	<button class="hp-btn" onclick={() => bulk('Merge')}>Merge</button>
	<button class="hp-btn" onclick={() => bulk('Close')}>Close</button>
	<button class="hp-btn hp-btn-danger" onclick={() => bulk('Delete')}>Delete</button>
</div>

<div class="hp-scroll">
	<table class="hp-table">
		<thead>
			<tr>
				<th class="c" style="width:34px"><input type="checkbox" aria-label="Select all" /></th>
				<th>Department</th>
				<th>Subject</th>
				<th>Requestor</th>
				<th class="c" style="width:120px">Priority</th>
				<th class="c" style="width:110px">Status</th>
				<th class="c" style="width:160px">Last Reply ▼</th>
			</tr>
		</thead>
		<tbody>
			{#each data.tickets as tk (tk.id)}
				<tr class="hp-row">
					<td class="c"><input type="checkbox" aria-label={`Select ${tk.ticket_number}`} /></td>
					<td
						><i class="fas fa-flag" style="color:#e6a23c;margin-right:6px"
						></i>{tk.department_name ?? departmentName(tk.department_id)}</td
					>
					<td>
						<a
							class="cell-link"
							href={`/admin/tickets/${tk.id}`}
							data-testid={`row-ticket-${tk.id}`}
						>
							{tk.ticket_number} - {tk.subject}
						</a>
					</td>
					<td><span style="color:#337ab7">{requestor(tk)}</span></td>
					<td class="c"
						><span class={`hp-stext ${priorityClass(tk.priority)}`}
							>{priorityLabels[tk.priority] ?? tk.priority}</span
						></td
					>
					<td class="c"><span class={`hp-stext ${statusClass(tk.status)}`}>{tk.status}</span></td>
					<td class="c" style="color:#666">{fmtDateTime(tk.last_reply_at ?? tk.created_at)}</td>
				</tr>
			{:else}
				<tr
					><td colspan="7" style="text-align:center;padding:28px;color:#999">No tickets found</td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

<HpPager page={curPage} {perPage} {total} />
