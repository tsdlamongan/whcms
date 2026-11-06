<script lang="ts">
	import { appName } from '$lib/appName';
	import DateText from '$lib/components/DateText.svelte';
	import HpModal from '$lib/components/hp/HpModal.svelte';
	import HpPager from '$lib/components/hp/HpPager.svelte';
	import type { PageProps } from './$types';
	import type { IntegrationLogRow } from './+page.server';

	let { data }: PageProps = $props();

	let viewerOpen = $state(false);
	let viewerRow = $state<IntegrationLogRow | null>(null);

	const PROVIDERS = ['duitku', 'cpanel', 'directadmin', 'rdash'];

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

	function openViewer(row: IntegrationLogRow) {
		viewerRow = row;
		viewerOpen = true;
	}
</script>

<svelte:head>
	<title>Integration Log — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Integration Log</h1>

{#if data.listError}
	<div class="hp-alert-yellow" data-testid="integration-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

<form method="get" class="hp-filter" data-testid="integration-filter-form">
	<div style="flex:0 0 200px">
		<div class="hp-field-label">Provider</div>
		<select class="hp-select" name="provider" data-testid="integration-provider-filter">
			<option value="" selected={data.filters.provider === ''}>All — Provider</option>
			{#each PROVIDERS as p (p)}
				<option value={p} selected={data.filters.provider === p}>{p}</option>
			{/each}
		</select>
	</div>
	<div style="flex:0 0 180px">
		<div class="hp-field-label">Result</div>
		<select class="hp-select" name="success" data-testid="integration-success-filter">
			<option value="" selected={data.filters.success === ''}>All</option>
			<option value="true" selected={data.filters.success === 'true'}>Success only</option>
			<option value="false" selected={data.filters.success === 'false'}>Failed only</option>
		</select>
	</div>
	<button class="hp-btn hp-btn-primary" type="submit" data-testid="integration-filter-submit">
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
				<th style="width:100px">Provider</th>
				<th>Endpoint</th>
				<th style="width:70px">Method</th>
				<th class="c" style="width:80px">Status Code</th>
				<th style="width:90px">Result</th>
				<th class="r" style="width:90px">Latency</th>
				<th class="r" style="width:110px">Data</th>
			</tr>
		</thead>
		<tbody>
			{#each data.logs as row (row.id)}
				<tr class="hp-row">
					<td data-testid={`row-integration-log-${row.id}`}
						><DateText value={row.created_at} mode="datetime" /></td
					>
					<td style="font-weight:600;color:#444">{row.provider}</td>
					<td>
						<code
							style="display:block;max-width:280px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;background:#f5f5f5;border-radius:3px;padding:1px 5px;font-size:12px"
							title={row.endpoint}
						>
							{row.endpoint}
						</code>
					</td>
					<td>{row.method}</td>
					<td class="c">
						<span
							style={row.status_code >= 400 || row.status_code === 0
								? 'font-weight:700;color:#d9534f'
								: ''}
						>
							{row.status_code || '—'}
						</span>
					</td>
					<td
						><span class={`hp-stext ${row.success ? 'green' : 'red'}`}
							>{row.success ? 'Success' : 'Failed'}</span
						></td
					>
					<td class="r" style="font-variant-numeric:tabular-nums">{row.latency_ms} ms</td>
					<td class="r">
						<button
							type="button"
							class="hp-btn"
							data-testid={`integration-json-view-${row.id}`}
							onclick={() => openViewer(row)}
						>
							View JSON
						</button>
					</td>
				</tr>
			{:else}
				<tr
					><td colspan="8" style="text-align:center;padding:28px;color:#999"
						>No integration log entries found</td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

<HpPager page={curPage} {perPage} {total} />

<HpModal open={viewerOpen} title="Integration Call Detail" onClose={() => (viewerOpen = false)}>
	{#if viewerRow}
		<div
			style="margin-bottom:10px;display:flex;flex-wrap:wrap;align-items:center;gap:8px;font-size:13px;color:#666"
		>
			<span style="font-weight:700;color:#444">{viewerRow.provider}</span>
			<code style="background:#f5f5f5;border-radius:3px;padding:1px 5px;font-size:12px">
				{viewerRow.method}
				{viewerRow.endpoint}
			</code>
			<span style="font-variant-numeric:tabular-nums"
				>HTTP {viewerRow.status_code} · {viewerRow.latency_ms} ms</span
			>
		</div>
		{#if viewerRow.error}
			<div style="margin-bottom:10px">
				<div
					style="font-size:11px;font-weight:700;color:#888;text-transform:uppercase;margin-bottom:4px"
				>
					Error
				</div>
				<p
					style="background:#f2dede;border-radius:4px;padding:8px 10px;font-size:12px;color:#a94442;margin:0"
				>
					{viewerRow.error}
				</p>
			</div>
		{/if}
		<div style="display:grid;grid-template-columns:1fr 1fr;gap:12px">
			<div>
				<div
					style="font-size:11px;font-weight:700;color:#888;text-transform:uppercase;margin-bottom:4px"
				>
					Request
				</div>
				<pre
					style="max-height:280px;overflow:auto;background:#2b2b2b;color:#eee;border-radius:4px;padding:10px;font-size:11px;line-height:1.5"
					data-testid="integration-json-request">{prettyJson(viewerRow.request)}</pre>
			</div>
			<div>
				<div
					style="font-size:11px;font-weight:700;color:#888;text-transform:uppercase;margin-bottom:4px"
				>
					Response
				</div>
				<pre
					style="max-height:280px;overflow:auto;background:#2b2b2b;color:#eee;border-radius:4px;padding:10px;font-size:11px;line-height:1.5"
					data-testid="integration-json-response">{prettyJson(viewerRow.response)}</pre>
			</div>
		</div>
	{/if}
</HpModal>
