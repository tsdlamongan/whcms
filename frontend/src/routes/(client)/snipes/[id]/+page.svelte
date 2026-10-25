<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import Alert from '$lib/components/Alert.svelte';
	import Breadcrumb from '$lib/components/Breadcrumb.svelte';
	import DateText from '$lib/components/DateText.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import MoneyText from '$lib/components/MoneyText.svelte';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import { t } from '$lib/i18n';
	import { formatIDR } from '$lib/money';
	import { toast } from '$lib/stores/toast.svelte';
	import type { SubmitFunction } from '@sveltejs/kit';
	import type { PageProps } from './$types';
	import { isTerminal, snipeVariant } from '../shared';

	let { data, form }: PageProps = $props();

	const sn = $derived(data.snipe);
	const paid = $derived(sn.status === 'active');
	let cancelOpen = $state(false);
	let cancelling = $state(false);

	const cancelHandler: SubmitFunction = () => {
		cancelling = true;
		return async ({ result, update }) => {
			cancelling = false;
			if (result.type === 'success') {
				cancelOpen = false;
				toast.success(t('portal.snipe.toast.cancelled'));
			} else {
				toast.error(t('portal.snipe.toast.cancelFailed'));
			}
			await update();
		};
	};
</script>

<svelte:head>
	<title>{sn.domain_name} — {t('portal.snipe.list.title')} — {appName}</title>
</svelte:head>

<Breadcrumb
	items={[
		{ label: t('nav.home'), href: '/dashboard' },
		{ label: t('portal.snipe.list.title'), href: '/snipes' },
		{ label: sn.domain_name }
	]}
/>

<div
	class="mb-5"
	style="display:flex;flex-wrap:wrap;align-items:flex-start;justify-content:space-between;gap:12px"
>
	<div>
		<h1 class="ca-h1" style="margin-bottom:4px">
			{sn.domain_name}
			<span data-testid="snipe-status" style="margin-left:8px;vertical-align:middle">
				<StatusBadge
					status={sn.status}
					variant={snipeVariant(sn.status)}
					label={t(`portal.snipe.status.${sn.status}`)}
				/>
			</span>
		</h1>
		{#if sn.status === 'active' && sn.phase}
			<p class="ca-muted" data-testid="snipe-phase">{t(`portal.snipe.phase.${sn.phase}`)}</p>
		{/if}
	</div>
	<div style="display:flex;gap:8px;flex-wrap:wrap">
		{#if sn.status === 'pending_payment' && sn.invoice_id}
			<a
				href={`/billing/invoices/${sn.invoice_id}`}
				class="ca-btn ca-btn-primary"
				data-testid="snipe-pay"
			>
				{t('portal.snipe.detail.payNow')}
			</a>
		{/if}
		{#if sn.won_domain_id}
			<a
				href={`/domains/${sn.won_domain_id}`}
				class="ca-btn ca-btn-primary"
				data-testid="snipe-view-domain"
			>
				{t('portal.snipe.detail.viewDomain')}
			</a>
		{/if}
		{#if !isTerminal(sn.status)}
			<button
				type="button"
				class="ca-btn ca-btn-danger"
				data-testid="snipe-cancel"
				onclick={() => (cancelOpen = true)}
			>
				{t('portal.snipe.detail.cancel')}
			</button>
		{/if}
	</div>
</div>

{#if sn.status === 'pending_payment'}
	<div class="mb-4"><Alert type="info">{t('portal.snipe.detail.payHint')}</Alert></div>
{/if}
{#if form?.errorMessage}
	<div class="mb-4" data-testid="snipe-cancel-error">
		<Alert type="error">{form.errorMessage}</Alert>
	</div>
{/if}

<div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(300px,1fr));gap:16px">
	<div class="ca-card ca-card-body">
		<h2 class="ca-h2" style="margin:0 0 10px;font-size:16px">
			{t('portal.snipe.detail.overview')}
		</h2>
		<dl class="kv">
			<dt>{t('portal.snipe.detail.plan')}</dt>
			<dd>{sn.plan_name || '—'}</dd>
			<dt>{t('portal.snipe.detail.years')}</dt>
			<dd>{t('portal.snipe.new.years', { n: sn.years })}</dd>
			<dt>{t('portal.snipe.detail.window')}</dt>
			<dd>
				{#if sn.hunt_from}
					<DateText value={sn.hunt_from} mode="datetime" /> →
					{#if sn.hunt_until}<DateText value={sn.hunt_until} mode="datetime" />{:else}{t(
							'portal.snipe.detail.deadline'
						)}{/if}
				{:else}
					{t('portal.snipe.detail.windowNone')}
				{/if}
			</dd>
			<dt>{t('portal.snipe.detail.deadline')}</dt>
			<dd data-testid="snipe-deadline"><DateText value={sn.deadline_at} mode="datetime" /></dd>
			<dt>{t('portal.snipe.detail.attempts')}</dt>
			<dd data-testid="snipe-attempts">{sn.attempts}</dd>
			<dt>{t('portal.snipe.detail.created')}</dt>
			<dd><DateText value={sn.created_at} mode="datetime" /></dd>
			{#if sn.paid_at}
				<dt>{t('portal.snipe.detail.paidAt')}</dt>
				<dd><DateText value={sn.paid_at} mode="datetime" /></dd>
			{/if}
			{#if sn.won_at}
				<dt>{t('portal.snipe.detail.wonAt')}</dt>
				<dd><DateText value={sn.won_at} mode="datetime" /></dd>
			{/if}
		</dl>
	</div>

	<div class="ca-card ca-card-body">
		<h2 class="ca-h2" style="margin:0 0 10px;font-size:16px">{t('portal.snipe.detail.billing')}</h2>
		<dl class="kv">
			{#if sn.invoice_id}
				<dt>{t('portal.snipe.detail.invoice')}</dt>
				<dd>
					<a class="text-primary hover:underline" href={`/billing/invoices/${sn.invoice_id}`}
						>#{sn.invoice_id}</a
					>
				</dd>
			{/if}
			<dt>{t('portal.snipe.detail.fee')}</dt>
			<dd><MoneyText amount={sn.fee_amount} /></dd>
			<dt>{t('portal.snipe.detail.domainAmount')}</dt>
			<dd><MoneyText amount={sn.domain_amount} /></dd>
			{#if sn.status !== 'won'}
				<dt>{t('portal.snipe.detail.refundable')}</dt>
				<dd data-testid="snipe-refundable"><MoneyText amount={sn.refundable_domain} /></dd>
			{/if}
			{#if sn.refund_amount > 0}
				<dt>{t('portal.snipe.detail.refunded')}</dt>
				<dd data-testid="snipe-refunded" style="color:#218739;font-weight:600">
					<MoneyText amount={sn.refund_amount} />
				</dd>
			{/if}
		</dl>
	</div>
</div>

<div class="ca-card ca-card-body" style="margin-top:16px">
	<h2 class="ca-h2" style="margin:0 0 10px;font-size:16px">{t('portal.snipe.detail.timeline')}</h2>
	<ul class="events" data-testid="snipe-events">
		{#each data.events as ev, i (i)}
			<li data-testid="snipe-event" data-kind={ev.kind}>
				<span>{t(`portal.snipe.detail.events.${ev.kind}`)}</span>
				<span class="when"><DateText value={ev.created_at} mode="datetime" /></span>
			</li>
		{:else}
			<li class="ca-muted">{t('portal.snipe.detail.timelineEmpty')}</li>
		{/each}
	</ul>
</div>

<Modal bind:open={cancelOpen} title={t('portal.snipe.detail.cancelTitle')} size="sm">
	<form method="POST" action="?/cancel" use:enhance={cancelHandler} data-testid="snipe-cancel-form">
		<p class="text-sm text-gray-600" style="margin-bottom:16px">
			{#if paid}
				{t('portal.snipe.detail.cancelBodyPaid', { amount: formatIDR(sn.refundable_domain) })}
			{:else}
				{t('portal.snipe.detail.cancelBodyUnpaid')}
			{/if}
		</p>
		<div style="display:flex;justify-content:flex-end;gap:8px">
			<button
				type="button"
				class="ca-btn ca-btn-default"
				disabled={cancelling}
				onclick={() => (cancelOpen = false)}
			>
				{t('portal.snipe.detail.keep')}
			</button>
			<button
				type="submit"
				class="ca-btn ca-btn-danger"
				disabled={cancelling}
				aria-busy={cancelling}
				data-testid="snipe-cancel-confirm"
			>
				{#if cancelling}<span class="ca-spinner" aria-hidden="true"></span>{/if}{t(
					'portal.snipe.detail.cancelConfirm'
				)}
			</button>
		</div>
	</form>
</Modal>

<style>
	.kv {
		display: grid;
		grid-template-columns: 170px 1fr;
		gap: 6px 12px;
		margin: 0;
	}
	.kv dt {
		color: #6c757d;
	}
	.kv dd {
		margin: 0;
	}
	.events {
		list-style: none;
		margin: 0;
		padding: 0;
	}
	.events li {
		display: flex;
		gap: 12px;
		justify-content: space-between;
		padding: 7px 0;
		border-bottom: 1px solid #eee;
	}
	.events li:last-child {
		border-bottom: 0;
	}
	.when {
		color: #868e96;
		font-size: 12px;
		white-space: nowrap;
	}
</style>
