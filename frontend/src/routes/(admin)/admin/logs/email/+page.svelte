<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import { invalidateAll } from '$app/navigation';
	import DateText from '$lib/components/DateText.svelte';
	import HpPager from '$lib/components/hp/HpPager.svelte';
	import { toast } from '$lib/stores/toast.svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	let retryingId = $state<number | null>(null);

	const total = $derived(data.meta?.total ?? data.logs.length);
	const curPage = $derived(data.meta?.page ?? data.page);
	const perPage = $derived(data.meta?.per_page ?? data.perPage);
	const from = $derived(total === 0 ? 0 : (curPage - 1) * perPage + 1);
	const to = $derived(Math.min(curPage * perPage, total));

	function statusClass(status: string): string {
		if (status === 'sent') return 'green';
		if (status === 'failed') return 'red';
		return 'orange';
	}
</script>

<svelte:head>
	<title>Email Log — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Email Log</h1>

{#if data.listError}
	<div class="hp-alert-yellow" data-testid="email-log-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

{#if form?.retryErrorMessage}
	<div class="hp-alert-red" data-testid="email-log-retry-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{form.retryErrorMessage}
	</div>
{/if}

<form method="get" class="hp-filter" data-testid="email-log-filter-form">
	<div class="grow">
		<div class="hp-field-label">Search</div>
		<input
			class="hp-input"
			type="search"
			name="search"
			value={data.filters.search}
			placeholder="Search"
			data-testid="email-log-search-input"
		/>
	</div>
	<div style="flex:0 0 160px">
		<div class="hp-field-label">Status</div>
		<select class="hp-select" name="status" data-testid="email-log-status-filter">
			<option value="" selected={data.filters.status === ''}>All</option>
			<option value="queued" selected={data.filters.status === 'queued'}>Queued</option>
			<option value="sent" selected={data.filters.status === 'sent'}>Sent</option>
			<option value="failed" selected={data.filters.status === 'failed'}>Failed</option>
		</select>
	</div>
	<button class="hp-btn hp-btn-primary" type="submit" data-testid="email-log-filter-submit">
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
				<th>To</th>
				<th>Template</th>
				<th>Subject</th>
				<th style="width:100px">Status</th>
				<th style="width:150px">Sent At</th>
				<th class="r" style="width:110px">Actions</th>
			</tr>
		</thead>
		<tbody>
			{#each data.logs as row (row.id)}
				<tr class="hp-row">
					<td data-testid={`row-email-log-${row.id}`}
						><DateText value={row.created_at} mode="datetime" /></td
					>
					<td>{row.to_email}</td>
					<td
						><code style="background:#f5f5f5;border-radius:3px;padding:1px 5px;font-size:12px"
							>{row.template_key || '—'}</code
						></td
					>
					<td>{row.subject}</td>
					<td>
						<span class={`hp-stext ${statusClass(row.status)}`}>{row.status}</span>
						{#if row.status === 'failed' && row.error}
							<div
								style="margin-top:2px;max-width:200px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:11px;color:#d9534f"
								title={row.error}
							>
								{row.error}
							</div>
						{/if}
					</td>
					<td><DateText value={row.sent_at} mode="datetime" /></td>
					<td class="r">
						{#if row.status === 'failed'}
							<form
								method="POST"
								action="?/retry"
								style="display:inline"
								use:enhance={() => {
									retryingId = row.id;
									return async ({ result, update }) => {
										retryingId = null;
										if (result.type === 'success') {
											toast.success('Email retried');
											await invalidateAll();
										}
										await update({ reset: false });
									};
								}}
							>
								<input type="hidden" name="id" value={row.id} />
								<button
									type="submit"
									class="hp-btn"
									disabled={retryingId === row.id}
									aria-busy={retryingId === row.id}
									data-testid={`email-log-retry-${row.id}`}
								>
									Retry
								</button>
							</form>
						{:else}
							<span style="color:#ccc">—</span>
						{/if}
					</td>
				</tr>
			{:else}
				<tr
					><td colspan="7" style="text-align:center;padding:28px;color:#999"
						>No email log entries found</td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

<HpPager page={curPage} {perPage} {total} />
