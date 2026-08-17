<script lang="ts">
	import { enhance } from '$app/forms';
	import { invalidateAll } from '$app/navigation';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import DateText from '$lib/components/DateText.svelte';
	import HpModal from '$lib/components/hp/HpModal.svelte';
	import HpPager from '$lib/components/hp/HpPager.svelte';
	import { toast } from '$lib/stores/toast.svelte';
	import type { PageProps } from './$types';
	import type { ModuleActionRow } from './+page.server';

	let { data, form }: PageProps = $props();

	type Row = ModuleActionRow;

	const TYPE_LABELS: Record<string, string> = {
		'provision:create': 'Create Hosting Account',
		'provision:suspend': 'Suspend Hosting Account',
		'provision:unsuspend': 'Unsuspend Hosting Account',
		'provision:terminate': 'Terminate Hosting Account',
		'provision:change_package': 'Change Hosting Package',
		'provision:change_password': 'Change Hosting Password',
		'domain:register': 'Register Domain',
		'domain:transfer': 'Transfer Domain',
		'domain:renew': 'Renew Domain'
	};
	const MODULE_ACTION_TYPES = Object.keys(TYPE_LABELS);
	function typeLabel(t: string): string {
		return TYPE_LABELS[t] ?? t;
	}

	const total = $derived(data.meta?.total ?? data.actions.length);
	const curPage = $derived(data.meta?.page ?? data.page);
	const perPage = $derived(data.meta?.per_page ?? data.perPage);
	const from = $derived(total === 0 ? 0 : (curPage - 1) * perPage + 1);
	const to = $derived(Math.min(curPage * perPage, total));

	let retryingId = $state<string | null>(null);

	// delete confirm
	let confirmOpen = $state(false);
	let deleting = $state(false);
	let deleteTarget = $state<Row | null>(null);
	let deleteFormEl = $state<HTMLFormElement | null>(null);

	function askDelete(row: Row) {
		deleteTarget = row;
		confirmOpen = true;
	}

	// dismiss-all confirm
	let dismissAllOpen = $state(false);
	let dismissingAll = $state(false);
	let dismissAllFormEl = $state<HTMLFormElement | null>(null);

	// payload/error viewer
	let viewerOpen = $state(false);
	let viewerRow = $state<Row | null>(null);

	function openViewer(row: Row) {
		viewerRow = row;
		viewerOpen = true;
	}

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
</script>

<svelte:head>
	<title>Pending Module Actions — HostPanel Admin</title>
</svelte:head>

<h1 class="hp-h1">Pending Module Actions</h1>
<p style="margin-top:-8px;margin-bottom:14px;color:#888;font-size:13px">
	Provisioning and domain-registrar actions that failed and exhausted their automatic retries (or
	are still auto-retrying) — WHMCS calls this the Module Queue. Retry once the underlying issue is
	fixed, or dismiss if it was resolved manually.
</p>

{#if data.listError}
	<div class="hp-alert-yellow" data-testid="module-queue-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

{#if form && !form.success}
	<div class="hp-alert-red" data-testid="module-queue-action-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{form.errorMessage}
	</div>
{/if}

<form method="get" class="hp-filter" data-testid="module-queue-filter-form">
	<div style="flex:0 0 220px">
		<div class="hp-field-label">Action Type</div>
		<select class="hp-select" name="type" data-testid="module-queue-type-filter">
			<option value="" selected={data.filters.type === ''}>All Types</option>
			{#each MODULE_ACTION_TYPES as t (t)}
				<option value={t} selected={data.filters.type === t}>{typeLabel(t)}</option>
			{/each}
		</select>
	</div>
	<div style="flex:0 0 160px">
		<div class="hp-field-label">State</div>
		<select class="hp-select" name="state" data-testid="module-queue-state-filter">
			<option value="" selected={data.filters.state === ''}>All</option>
			<option value="archived" selected={data.filters.state === 'archived'}>Archived (stuck)</option
			>
			<option value="retry" selected={data.filters.state === 'retry'}>Still retrying</option>
		</select>
	</div>
	<button class="hp-btn hp-btn-primary" type="submit" data-testid="module-queue-filter-submit">
		<i class="fas fa-search"></i>Filter
	</button>
</form>

<div class="hp-listbar" style="display:flex;align-items:center;justify-content:space-between;gap:12px">
	<div class="hp-count">{total} Records Found, Showing {from} to {to}</div>
	{#if total > 0}
		<button
			type="button"
			class="hp-btn hp-btn-danger"
			style="padding:5px 10px"
			data-testid="module-queue-dismiss-all"
			onclick={() => (dismissAllOpen = true)}
		>
			Dismiss All ({total})
		</button>
	{/if}
</div>

<div class="hp-scroll">
	<table class="hp-table">
		<thead>
			<tr>
				<th style="width:150px">Last Failed</th>
				<th>Action</th>
				<th style="width:90px">Queue</th>
				<th class="c" style="width:90px">Attempts</th>
				<th style="width:100px">State</th>
				<th>Last Error</th>
				<th class="r" style="width:170px">Actions</th>
			</tr>
		</thead>
		<tbody>
			{#each data.actions as row (row.queue + '/' + row.id)}
				<tr class="hp-row" data-testid={`row-module-action-${row.id}`}>
					<td><DateText value={row.last_failed_at} mode="datetime" /></td>
					<td style="font-weight:600;color:#444">{typeLabel(row.type)}</td>
					<td>{row.queue}</td>
					<td class="c" style="font-variant-numeric:tabular-nums">{row.retried}/{row.max_retry}</td>
					<td>
						<span class={`hp-stext ${row.state === 'archived' ? 'red' : 'orange'}`}
							>{row.state === 'archived' ? 'Archived' : 'Retrying'}</span
						>
					</td>
					<td>
						<button
							type="button"
							class="module-action-error-btn"
							title={row.last_err}
							data-testid={`module-queue-view-${row.id}`}
							onclick={() => openViewer(row)}
						>
							{row.last_err || '—'}
						</button>
					</td>
					<td class="r">
						<form
							method="POST"
							action="?/retry"
							style="display:inline"
							use:enhance={() => {
								retryingId = row.id;
								return async ({ result, update }) => {
									retryingId = null;
									if (result.type === 'success') {
										toast.success('Module action re-queued');
										await invalidateAll();
									}
									await update({ reset: false });
								};
							}}
						>
							<input type="hidden" name="queue" value={row.queue} />
							<input type="hidden" name="id" value={row.id} />
							<button
								type="submit"
								class="hp-btn"
								style="padding:5px 10px"
								disabled={retryingId === row.id}
								aria-busy={retryingId === row.id}
								data-testid={`module-queue-retry-${row.id}`}
							>
								Retry
							</button>
						</form>
						<button
							type="button"
							class="hp-btn hp-btn-danger"
							style="padding:5px 10px"
							data-testid={`module-queue-delete-${row.id}`}
							onclick={() => askDelete(row)}
						>
							Delete
						</button>
					</td>
				</tr>
			{:else}
				<tr
					><td colspan="7" style="text-align:center;padding:28px;color:#999"
						>No pending module actions — everything is up to date.</td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

<HpPager page={curPage} {perPage} {total} />

<!-- delete confirm -->
<form
	method="POST"
	action="?/delete"
	class="hidden"
	bind:this={deleteFormEl}
	use:enhance={() => {
		deleting = true;
		return async ({ result, update }) => {
			deleting = false;
			confirmOpen = false;
			if (result.type === 'success') {
				toast.success('Module action dismissed');
				await invalidateAll();
			}
			await update({ reset: false });
		};
	}}
>
	<input type="hidden" name="queue" value={deleteTarget?.queue ?? ''} />
	<input type="hidden" name="id" value={deleteTarget?.id ?? ''} />
</form>

<ConfirmDialog
	bind:open={confirmOpen}
	danger
	loading={deleting}
	title="Dismiss Module Action"
	message={`Dismiss this "${deleteTarget ? typeLabel(deleteTarget.type) : ''}" action without retrying it? This cannot be undone.`}
	confirmLabel="Dismiss"
	onConfirm={() => deleteFormEl?.requestSubmit()}
	onCancel={() => (deleteTarget = null)}
/>

<!-- dismiss-all confirm -->
<form
	method="POST"
	action="?/dismissAll"
	class="hidden"
	bind:this={dismissAllFormEl}
	use:enhance={() => {
		dismissingAll = true;
		return async ({ result, update }) => {
			dismissingAll = false;
			dismissAllOpen = false;
			if (result.type === 'success') {
				const count =
					result.data && 'dismissedCount' in result.data ? Number(result.data.dismissedCount) : 0;
				toast.success(`${count} module action${count === 1 ? '' : 's'} dismissed`);
				await invalidateAll();
			}
			await update({ reset: false });
		};
	}}
>
	<input type="hidden" name="type" value={data.filters.type} />
	<input type="hidden" name="state" value={data.filters.state} />
</form>

<ConfirmDialog
	bind:open={dismissAllOpen}
	danger
	loading={dismissingAll}
	title="Dismiss All Module Actions"
	message={`Dismiss all ${total} module action${total === 1 ? '' : 's'} matching the current filter, without retrying any of them? This cannot be undone.`}
	confirmLabel="Dismiss All"
	onConfirm={() => dismissAllFormEl?.requestSubmit()}
/>

<HpModal open={viewerOpen} title="Module Action Detail" onClose={() => (viewerOpen = false)}>
	{#if viewerRow}
		<div
			style="margin-bottom:10px;display:flex;flex-wrap:wrap;align-items:center;gap:8px;font-size:13px;color:#666"
		>
			<span style="font-weight:700;color:#444">{typeLabel(viewerRow.type)}</span>
			<code style="background:#f5f5f5;border-radius:3px;padding:1px 5px;font-size:12px">
				{viewerRow.queue}/{viewerRow.id}
			</code>
			<span style="font-variant-numeric:tabular-nums"
				>{viewerRow.retried}/{viewerRow.max_retry} attempts</span
			>
		</div>
		{#if viewerRow.last_err}
			<div style="margin-bottom:10px">
				<div
					style="font-size:11px;font-weight:700;color:#888;text-transform:uppercase;margin-bottom:4px"
				>
					Last Error
				</div>
				<p
					style="background:#f2dede;border-radius:4px;padding:8px 10px;font-size:12px;color:#a94442;margin:0"
				>
					{viewerRow.last_err}
				</p>
			</div>
		{/if}
		<div>
			<div
				style="font-size:11px;font-weight:700;color:#888;text-transform:uppercase;margin-bottom:4px"
			>
				Payload
			</div>
			<pre
				style="max-height:320px;overflow:auto;background:#2b2b2b;color:#eee;border-radius:4px;padding:10px;font-size:11px;line-height:1.5"
				data-testid="module-queue-payload">{prettyJson(viewerRow.payload)}</pre>
		</div>
	{/if}
</HpModal>

<style>
	.module-action-error-btn {
		max-width: 320px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		background: none;
		border: none;
		padding: 0;
		font-size: 12px;
		color: #a94442;
		text-align: left;
		cursor: pointer;
		display: block;
	}
	.module-action-error-btn:hover {
		text-decoration: underline;
	}
</style>
