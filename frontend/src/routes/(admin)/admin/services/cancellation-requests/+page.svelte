<script lang="ts">
	import { enhance } from '$app/forms';
	import { invalidateAll } from '$app/navigation';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import DateText from '$lib/components/DateText.svelte';
	import HpPager from '$lib/components/hp/HpPager.svelte';
	import { toast } from '$lib/stores/toast.svelte';
	import type { PageProps } from './$types';
	import type { CancellationRequestRow } from './+page.server';

	let { data, form }: PageProps = $props();

	type Row = CancellationRequestRow;

	const STATUS_BADGE: Record<string, string> = {
		pending: 'pending',
		accepted: 'active',
		rejected: 'terminated',
		auto_processed: 'cancelled'
	};
	const STATUS_LABEL: Record<string, string> = {
		pending: 'Pending',
		accepted: 'Accepted',
		rejected: 'Rejected',
		auto_processed: 'Auto Processed'
	};
	const MODE_LABEL: Record<string, string> = {
		immediate: 'Immediate',
		end_of_term: 'End of Term'
	};

	const total = $derived(data.meta?.total ?? data.rows.length);
	const curPage = $derived(data.meta?.page ?? data.page);
	const perPage = $derived(data.meta?.per_page ?? data.perPage);
	const from = $derived(total === 0 ? 0 : (curPage - 1) * perPage + 1);
	const to = $derived(Math.min(curPage * perPage, total));

	// accept confirm
	let acceptOpen = $state(false);
	let acceptLoading = $state(false);
	let acceptTarget = $state<Row | null>(null);
	let acceptFormEl = $state<HTMLFormElement | null>(null);

	function askAccept(row: Row) {
		acceptTarget = row;
		acceptOpen = true;
	}

	// reject confirm
	let rejectOpen = $state(false);
	let rejectLoading = $state(false);
	let rejectTarget = $state<Row | null>(null);
	let rejectFormEl = $state<HTMLFormElement | null>(null);

	function askReject(row: Row) {
		rejectTarget = row;
		rejectOpen = true;
	}
</script>

<svelte:head>
	<title>Cancellation Requests — HostPanel Admin</title>
</svelte:head>

<h1 class="hp-h1">Cancellation Requests</h1>
<p style="margin-top:-8px;margin-bottom:14px;color:#888;font-size:13px">
	Client-submitted service cancellation requests. End-of-term requests wait here for review —
	accepting flags the service to stop at the end of its current billing period; rejecting leaves
	the service untouched. Immediate requests are processed automatically and listed here for
	history only.
</p>

{#if data.listError}
	<div class="hp-alert-yellow" data-testid="cancellation-requests-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

{#if form && !form.success}
	<div class="hp-alert-red" data-testid="cancellation-requests-action-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{form.errorMessage}
	</div>
{/if}

<form method="get" class="hp-filter" data-testid="cancellation-requests-filter-form">
	<div style="flex:0 0 200px">
		<div class="hp-field-label">Status</div>
		<select class="hp-select" name="status" data-testid="cancellation-requests-status-filter">
			<option value="" selected={data.filters.status === ''}>All Statuses</option>
			{#each Object.entries(STATUS_LABEL) as [value, label] (value)}
				<option {value} selected={data.filters.status === value}>{label}</option>
			{/each}
		</select>
	</div>
	<button class="hp-btn hp-btn-primary" type="submit" data-testid="cancellation-requests-filter-submit">
		<i class="fas fa-search"></i>Filter
	</button>
</form>

<div class="hp-listbar">
	<div class="hp-count">{total} Records Found, Showing {from} to {to}</div>
</div>

<div class="hp-scroll">
	<table class="hp-table" data-testid="cancellation-requests-table">
		<thead>
			<tr>
				<th style="width:130px">Requested</th>
				<th>Service</th>
				<th>Client</th>
				<th style="width:110px">Mode</th>
				<th>Reason</th>
				<th style="width:110px">Status</th>
				<th style="width:130px">Decided</th>
				<th class="r" style="width:170px">Actions</th>
			</tr>
		</thead>
		<tbody>
			{#each data.rows as row (row.id)}
				<tr class="hp-row" data-testid={`row-cancellation-request-${row.id}`}>
					<td><DateText value={row.requested_at} mode="datetime" /></td>
					<td>
						<a href={`/admin/services/${row.service_id}`} style="color:#337ab7;font-weight:600">
							{row.service_domain || `#${row.service_id}`}
						</a>
					</td>
					<td>{row.client_name || `#${row.client_id}`}</td>
					<td>{MODE_LABEL[row.mode] ?? row.mode}</td>
					<td style="max-width:220px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap"
						title={row.reason}>{row.reason || '—'}</td>
					<td>
						<span class={`hp-badge ${STATUS_BADGE[row.status] ?? 'inactive'}`}
							>{STATUS_LABEL[row.status] ?? row.status}</span
						>
					</td>
					<td>{#if row.decided_at}<DateText value={row.decided_at} mode="datetime" />{:else}—{/if}</td>
					<td class="r">
						{#if row.status === 'pending' && row.mode === 'end_of_term'}
							<button
								type="button"
								class="hp-btn"
								style="padding:5px 10px"
								data-testid={`cancellation-request-accept-${row.id}`}
								onclick={() => askAccept(row)}
							>
								Accept
							</button>
							<button
								type="button"
								class="hp-btn hp-btn-danger"
								style="padding:5px 10px"
								data-testid={`cancellation-request-reject-${row.id}`}
								onclick={() => askReject(row)}
							>
								Reject
							</button>
						{:else if row.status === 'pending' && row.mode === 'immediate'}
							<span style="color:#999;font-style:italic">Processing…</span>
						{:else}
							—
						{/if}
					</td>
				</tr>
			{:else}
				<tr
					><td colspan="8" style="text-align:center;padding:28px;color:#999"
						>No cancellation requests found.</td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

<HpPager page={curPage} {perPage} {total} />

<!-- accept confirm -->
<form
	method="POST"
	action="?/accept"
	class="hidden"
	bind:this={acceptFormEl}
	use:enhance={() => {
		acceptLoading = true;
		return async ({ result, update }) => {
			acceptLoading = false;
			acceptOpen = false;
			if (result.type === 'success') {
				toast.success('Cancellation request accepted');
				await invalidateAll();
			}
			await update({ reset: false });
		};
	}}
>
	<input type="hidden" name="id" value={acceptTarget?.id ?? ''} />
</form>

<ConfirmDialog
	bind:open={acceptOpen}
	loading={acceptLoading}
	title="Accept Cancellation Request"
	message={`Accept this cancellation request for ${acceptTarget?.service_domain ?? `#${acceptTarget?.service_id ?? ''}`}? The service will be flagged to stop at the end of its current billing period.`}
	confirmLabel="Accept"
	onConfirm={() => acceptFormEl?.requestSubmit()}
	onCancel={() => (acceptTarget = null)}
/>

<!-- reject confirm -->
<form
	method="POST"
	action="?/reject"
	class="hidden"
	bind:this={rejectFormEl}
	use:enhance={() => {
		rejectLoading = true;
		return async ({ result, update }) => {
			rejectLoading = false;
			rejectOpen = false;
			if (result.type === 'success') {
				toast.success('Cancellation request rejected');
				await invalidateAll();
			}
			await update({ reset: false });
		};
	}}
>
	<input type="hidden" name="id" value={rejectTarget?.id ?? ''} />
</form>

<ConfirmDialog
	bind:open={rejectOpen}
	danger
	loading={rejectLoading}
	title="Reject Cancellation Request"
	message={`Reject this cancellation request for ${rejectTarget?.service_domain ?? `#${rejectTarget?.service_id ?? ''}`}? The service will be left untouched.`}
	confirmLabel="Reject"
	onConfirm={() => rejectFormEl?.requestSubmit()}
	onCancel={() => (rejectTarget = null)}
/>
