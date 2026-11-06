<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import HpModal from '$lib/components/hp/HpModal.svelte';
	import { fmtDateTime } from '$lib/date';
	import { formatIDR } from '$lib/money';
	import { toast } from '$lib/stores/toast.svelte';
	import type { SubmitFunction } from '@sveltejs/kit';
	import type { PageProps } from './$types';
	import { isTerminal, snipeStatusClass } from './../types';

	let { data }: PageProps = $props();

	const sn = $derived(data.snipe);
	const paid = $derived(sn.source === 'client' && sn.status !== 'pending_payment');

	let cancelOpen = $state(false);
	let cancelling = $state(false);
	let refundFee = $state(false);

	const cancelHandler: SubmitFunction = () => {
		cancelling = true;
		return async ({ result, update }) => {
			cancelling = false;
			if (result.type === 'success') {
				cancelOpen = false;
				toast.success('The hunt was cancelled');
			} else if (result.type === 'failure') {
				const d = result.data as { errorMessage?: string } | undefined;
				toast.error(d?.errorMessage ?? 'Could not cancel the hunt');
			}
			await update();
		};
	};

	function eventClass(kind: string): string {
		if (kind === 'won') return 'green';
		if (kind === 'error' || kind === 'failed' || kind === 'late_win') return 'red';
		if (kind === 'expired' || kind === 'cancelled') return 'gray';
		return 'orange';
	}
</script>

<svelte:head>
	<title>{sn.domain_name} snipe — {appName} Admin</title>
</svelte:head>

<div
	style="display:flex;flex-wrap:wrap;align-items:center;justify-content:space-between;gap:12px;margin-bottom:14px"
>
	<div>
		<h1 class="hp-h1" style="margin-bottom:2px">
			{sn.domain_name}
			<span
				data-testid="snipe-status"
				class={`hp-badge ${snipeStatusClass(sn.status)}`}
				style="margin-left:8px;vertical-align:middle">{sn.status}</span
			>
		</h1>
		<p style="color:#888;font-size:12px;margin:0">
			Snipe #{sn.id} · {sn.source} hunt for
			<a class="cell-link" href={`/admin/clients/${sn.client_id}`}
				>{sn.client_name ?? `client #${sn.client_id}`}</a
			>
		</p>
	</div>
	<div style="display:flex;gap:8px">
		{#if sn.won_domain_id}
			<a
				class="hp-btn hp-btn-primary"
				href={`/admin/domains/${sn.won_domain_id}`}
				data-testid="snipe-view-domain"
			>
				<i class="fas fa-globe"></i>View Domain
			</a>
		{/if}
		{#if !isTerminal(sn.status)}
			<button
				type="button"
				class="hp-btn"
				data-testid="snipe-cancel-button"
				onclick={() => (cancelOpen = true)}
			>
				<i class="fas fa-ban"></i>Cancel Hunt
			</button>
		{/if}
	</div>
</div>

{#if sn.last_error && sn.status === 'active'}
	<div class="hp-alert-yellow" data-testid="snipe-last-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>Latest registrar reply: {sn.last_error}
	</div>
{/if}

<div class="grid2">
	<div class="hp-panel" style="padding:14px 18px">
		<h3 class="sec">Hunt</h3>
		<dl class="kv">
			<dt>Phase</dt>
			<dd data-testid="snipe-phase">{sn.status === 'active' ? sn.phase : '—'}</dd>
			<dt>Attempts</dt>
			<dd data-testid="snipe-attempts">{sn.attempts}</dd>
			<dt>Last attempt</dt>
			<dd>{fmtDateTime(sn.last_attempt_at)}</dd>
			<dt>Years</dt>
			<dd>{sn.years}</dd>
			<dt>Drop window</dt>
			<dd>
				{#if sn.hunt_from}{fmtDateTime(sn.hunt_from)} → {sn.hunt_until
						? fmtDateTime(sn.hunt_until)
						: 'deadline'}{:else}none (probing){/if}
			</dd>
			<dt>Gives up at</dt>
			<dd>{fmtDateTime(sn.deadline_at)}</dd>
			<dt>Nameservers</dt>
			<dd>{(sn.nameservers ?? []).join(', ') || 'system default'}</dd>
			<dt>Created</dt>
			<dd>{fmtDateTime(sn.created_at)}</dd>
			{#if sn.won_at}
				<dt>Won at</dt>
				<dd>{fmtDateTime(sn.won_at)}</dd>
			{/if}
		</dl>
	</div>

	<div class="hp-panel" style="padding:14px 18px">
		<h3 class="sec">Billing</h3>
		{#if sn.source === 'admin'}
			<p style="color:#777;margin:0">
				An admin hunt: no invoice and nothing to refund. The renewal rate for the registered domain
				is
				{formatIDR(sn.renew_per_year)} per year.
			</p>
		{:else}
			<dl class="kv">
				<dt>Invoice</dt>
				<dd>
					{#if sn.invoice_id}<a class="cell-link" href={`/admin/invoices/${sn.invoice_id}`}
							>#{sn.invoice_id}</a
						>{:else}—{/if}
				</dd>
				<dt>Service fee</dt>
				<dd>{formatIDR(sn.fee_amount)}</dd>
				<dt>Domain price</dt>
				<dd>{formatIDR(sn.domain_amount)}</dd>
				<dt>Refundable if lost</dt>
				<dd>{formatIDR(sn.refundable_domain)} (+ {formatIDR(sn.refundable_fee)} fee)</dd>
				<dt>Refunded</dt>
				<dd data-testid="snipe-refund">
					{sn.refund_amount > 0 ? formatIDR(sn.refund_amount) : '—'}{sn.refunded_at
						? ` on ${fmtDateTime(sn.refunded_at)}`
						: ''}
				</dd>
			</dl>
		{/if}
	</div>
</div>

<div class="hp-panel" style="padding:14px 18px;margin-top:16px">
	<h3 class="sec">Activity</h3>
	<ul class="events" data-testid="snipe-events">
		{#each data.events as ev (ev.id)}
			<li data-testid="snipe-event">
				<span class={`hp-stext ${eventClass(ev.kind)}`}>{ev.kind}</span>
				<span>{ev.message}</span>
				<span class="when">{fmtDateTime(ev.created_at)}</span>
			</li>
		{:else}
			<li style="color:#999">No activity recorded yet.</li>
		{/each}
	</ul>
</div>

<HpModal open={cancelOpen} title="Cancel Hunt" onClose={() => (cancelOpen = false)}>
	<form method="POST" action="?/cancel" use:enhance={cancelHandler}>
		<p style="color:#555;margin-bottom:12px">
			Stop hunting <strong>{sn.domain_name}</strong>?
			{#if paid}The domain price is credited back to the client's account credit.{/if}
		</p>
		{#if paid}
			<label class="hp-checkline" style="padding:0;margin-bottom:14px">
				<input
					type="checkbox"
					name="refund_fee"
					bind:checked={refundFee}
					data-testid="snipe-refund-fee"
				/>
				Also refund the non-refundable service fee ({formatIDR(sn.refundable_fee)})
			</label>
		{/if}
		<div style="display:flex;justify-content:flex-end;gap:8px">
			<button
				type="button"
				class="hp-btn"
				disabled={cancelling}
				onclick={() => (cancelOpen = false)}
			>
				Keep Hunting
			</button>
			<button
				type="submit"
				class="hp-btn hp-btn-primary"
				disabled={cancelling}
				aria-busy={cancelling}
				data-testid="snipe-cancel-confirm"
			>
				{#if cancelling}<i class="fas fa-spinner fa-spin"></i>{/if}Cancel Hunt
			</button>
		</div>
	</form>
</HpModal>

<style>
	.grid2 {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 16px;
	}
	@media (max-width: 900px) {
		.grid2 {
			grid-template-columns: 1fr;
		}
	}
	.sec {
		margin: 0 0 10px;
		font-size: 14px;
		color: #333;
	}
	.kv {
		display: grid;
		grid-template-columns: 150px 1fr;
		gap: 6px 12px;
		margin: 0;
	}
	.kv dt {
		color: #777;
	}
	.kv dd {
		margin: 0;
		color: #222;
	}
	.events {
		list-style: none;
		margin: 0;
		padding: 0;
	}
	.events li {
		display: flex;
		gap: 12px;
		align-items: baseline;
		padding: 7px 0;
		border-bottom: 1px solid #eee;
	}
	.events li:last-child {
		border-bottom: 0;
	}
	.events .when {
		margin-left: auto;
		color: #999;
		font-size: 12px;
		white-space: nowrap;
	}
</style>
