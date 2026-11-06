<script lang="ts">
	import { appName } from '$lib/appName';
	import DateText from '$lib/components/DateText.svelte';
	import HpModal from '$lib/components/hp/HpModal.svelte';
	import HpPager from '$lib/components/hp/HpPager.svelte';
	import type { PageProps } from './$types';
	import type { AuditLogRow } from './+page.server';

	let { data }: PageProps = $props();

	let viewerOpen = $state(false);
	let viewerRow = $state<AuditLogRow | null>(null);

	const total = $derived(data.meta?.total ?? data.logs.length);
	const curPage = $derived(data.meta?.page ?? data.page);
	const perPage = $derived(data.meta?.per_page ?? data.perPage);
	const from = $derived(total === 0 ? 0 : (curPage - 1) * perPage + 1);
	const to = $derived(Math.min(curPage * perPage, total));

	/** Pretty-print a json.RawMessage value that may arrive as object, string, or null. */
	function prettyJson(raw: unknown): string {
		if (raw === null || raw === undefined) return '—';
		let value: unknown = raw;
		if (typeof raw === 'string') {
			if (!raw.trim()) return '—';
			try {
				value = JSON.parse(raw);
			} catch {
				return raw;
			}
		}
		try {
			return JSON.stringify(value, null, 2);
		} catch {
			return String(value);
		}
	}

	function openViewer(row: AuditLogRow) {
		viewerRow = row;
		viewerOpen = true;
	}
</script>

<svelte:head>
	<title>Activity Log — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Activity Log</h1>

{#if data.listError}
	<div class="hp-alert-yellow" data-testid="audit-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

<form method="get" class="hp-filter" data-testid="audit-filter-form">
	<div style="flex:0 0 140px">
		<div class="hp-field-label">Actor (User ID)</div>
		<input
			class="hp-input"
			type="number"
			name="user_id"
			value={data.filters.userId}
			data-testid="audit-actor-filter"
		/>
	</div>
	<div style="flex:0 0 170px">
		<div class="hp-field-label">Entity</div>
		<input
			class="hp-input"
			type="text"
			name="entity"
			value={data.filters.entity}
			placeholder="invoice, service…"
			data-testid="audit-entity-filter"
		/>
	</div>
	<div style="flex:0 0 150px">
		<div class="hp-field-label">Date From</div>
		<input
			class="hp-input"
			type="date"
			name="from"
			value={data.filters.from}
			data-testid="audit-from-filter"
		/>
	</div>
	<div style="flex:0 0 150px">
		<div class="hp-field-label">Date To</div>
		<input
			class="hp-input"
			type="date"
			name="to"
			value={data.filters.to}
			data-testid="audit-to-filter"
		/>
	</div>
	<button class="hp-btn hp-btn-primary" type="submit" data-testid="audit-filter-submit">
		<i class="fas fa-search"></i>Filter
	</button>
</form>

<div class="hp-listbar">
	<div class="hp-count">{total} Records Found, Showing {from} to {to}</div>
</div>

<div class="hp-scroll">
	<table class="hp-table">
		<thead>
			<tr>
				<th style="width:150px">Time</th>
				<th>Actor</th>
				<th>Action</th>
				<th>Entity</th>
				<th style="width:120px">IP</th>
				<th class="r" style="width:110px">Data</th>
			</tr>
		</thead>
		<tbody>
			{#each data.logs as row (row.id)}
				<tr class="hp-row">
					<td data-testid={`row-audit-${row.id}`}
						><DateText value={row.created_at} mode="datetime" /></td
					>
					<td>
						{#if row.user_id}
							{row.user_email ?? `#${row.user_id}`}
						{:else}
							<span style="color:#aaa;font-style:italic">System</span>
						{/if}
					</td>
					<td
						><code style="background:#f5f5f5;border-radius:3px;padding:1px 5px;font-size:12px"
							>{row.action}</code
						></td
					>
					<td>{row.entity}{row.entity_id ? ` #${row.entity_id}` : ''}</td>
					<td style="color:#666">{row.ip}</td>
					<td class="r">
						<button
							type="button"
							class="hp-btn"
							data-testid={`audit-json-view-${row.id}`}
							onclick={() => openViewer(row)}
						>
							View JSON
						</button>
					</td>
				</tr>
			{:else}
				<tr
					><td colspan="6" style="text-align:center;padding:28px;color:#999"
						>No audit log entries found</td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

<HpPager page={curPage} {perPage} {total} />

<HpModal open={viewerOpen} title="Audit Entry Detail" onClose={() => (viewerOpen = false)}>
	{#if viewerRow}
		<div style="margin-bottom:10px;font-size:13px;color:#666">
			<code style="background:#f5f5f5;border-radius:3px;padding:1px 5px;font-size:12px"
				>{viewerRow.action}</code
			>
			<span style="margin-left:8px">{viewerRow.entity} #{viewerRow.entity_id}</span>
		</div>
		<div style="display:grid;grid-template-columns:1fr 1fr;gap:12px">
			<div>
				<div
					style="font-size:11px;font-weight:700;color:#888;text-transform:uppercase;margin-bottom:4px"
				>
					Before
				</div>
				<pre
					style="max-height:280px;overflow:auto;background:#2b2b2b;color:#eee;border-radius:4px;padding:10px;font-size:11px;line-height:1.5"
					data-testid="audit-json-before">{prettyJson(viewerRow.before)}</pre>
			</div>
			<div>
				<div
					style="font-size:11px;font-weight:700;color:#888;text-transform:uppercase;margin-bottom:4px"
				>
					After
				</div>
				<pre
					style="max-height:280px;overflow:auto;background:#2b2b2b;color:#eee;border-radius:4px;padding:10px;font-size:11px;line-height:1.5"
					data-testid="audit-json-after">{prettyJson(viewerRow.after)}</pre>
			</div>
		</div>
	{/if}
</HpModal>
