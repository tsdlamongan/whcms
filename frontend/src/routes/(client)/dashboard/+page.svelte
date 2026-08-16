<script lang="ts">
	import { enhance } from '$app/forms';
	import { goto } from '$app/navigation';
	import {
		Alert,
		DataTable,
		DateText,
		MoneyText,
		StatCard,
		StatusBadge,
		type Column
	} from '$lib/components';
	import { t } from '$lib/i18n';
	import { formatIDR } from '$lib/money';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	let resending = $state(false);

	// StatCard's `sub` prop is string-typed, so MoneyText cannot be used for the
	// unpaid total; use the shared formatter directly (same "Rp…,-" output).
	const idr = (n: number) => formatIDR(n);

	const statValue = (n: number | null): string | number => (n === null ? '—' : n);

	let columns: Column[] = $derived([
		{ key: 'invoice_number', label: t('billing.invoiceNumber') },
		{ key: 'created_at', label: t('billing.issuedDate') },
		{ key: 'due_date', label: t('billing.dueDate') },
		{ key: 'total', label: t('billing.total'), align: 'right' },
		{ key: 'status', label: t('clientcore.common.status') }
	]);

	const quickLinks = $derived([
		{ id: 'order', href: '/order', label: t('clientcore.dashboard.orderNew') },
		{ id: 'ticket', href: '/support/new', label: t('clientcore.dashboard.openTicket') },
		{ id: 'deposit', href: '/billing/deposit', label: t('clientcore.dashboard.addFunds') },
		{ id: 'account', href: '/account', label: t('clientcore.dashboard.manageAccount') }
	]);
</script>

<svelte:head>
	<title>{t('nav.dashboard')} — {t('common.appName')}</title>
</svelte:head>

<div class="mb-6">
	<h1 class="ca-h1" style="margin-bottom:2px">{t('nav.dashboard')}</h1>
	<p class="ca-muted" data-testid="dashboard-welcome">
		{t('common.welcome', { name: data.user.name })}
	</p>
</div>

{#if data.showVerifyBanner}
	<div class="mb-4" data-testid="dashboard-verify-banner">
		{#if form?.resendSuccess}
			<Alert type="success" title={t('clientcore.dashboard.verifyRequiredTitle')}>
				<span data-testid="dashboard-resend-success">
					{t('clientcore.dashboard.resendSuccess')}
				</span>
			</Alert>
		{:else}
			<Alert type="warning" title={t('clientcore.dashboard.verifyRequiredTitle')}>
				<p style="margin:0 0 8px">
					{t('clientcore.dashboard.verifyRequiredDesc', { email: data.user.email })}
				</p>
				{#if form?.resendError}
					<p style="margin:0 0 8px" data-testid="dashboard-resend-error">
						{t('clientcore.dashboard.resendFailed')}
					</p>
				{/if}
				<form
					method="POST"
					action="?/resendVerification"
					style="display:inline"
					use:enhance={() => {
						resending = true;
						return async ({ update }) => {
							resending = false;
							await update({ reset: false });
						};
					}}
				>
					<button
						type="submit"
						class="ca-btn ca-btn-primary"
						disabled={resending}
						data-testid="dashboard-resend-submit"
					>
						{resending
							? t('clientcore.dashboard.resendSending')
							: t('clientcore.dashboard.resendVerification')}
					</button>
				</form>
			</Alert>
		{/if}
	</div>
{/if}

{#if data.loadError}
	<div class="mb-4" data-testid="dashboard-error">
		<Alert type="error">
			{t('clientcore.dashboard.loadError', { message: data.loadError })}
		</Alert>
	</div>
{/if}

<div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
	<div data-testid="stat-active-services">
		<StatCard
			label={t('clientcore.dashboard.activeServices')}
			value={statValue(data.stats.activeServices)}
			accent="green"
			href="/services"
		/>
	</div>
	<div data-testid="stat-domains">
		<StatCard
			label={t('clientcore.dashboard.domains')}
			value={statValue(data.stats.domains)}
			accent="primary"
			href="/domains"
		/>
	</div>
	<div data-testid="stat-unpaid-invoices">
		<StatCard
			label={t('clientcore.dashboard.unpaidInvoices')}
			value={statValue(data.stats.unpaidCount)}
			sub={data.stats.unpaidTotal !== null
				? t('clientcore.dashboard.unpaidTotal', { amount: idr(data.stats.unpaidTotal) })
				: undefined}
			accent="yellow"
			href="/billing"
		/>
	</div>
	<div data-testid="stat-open-tickets">
		<StatCard
			label={t('clientcore.dashboard.openTickets')}
			value={statValue(data.stats.openTickets)}
			accent="red"
			href="/support"
		/>
	</div>
</div>

<div class="mt-6 grid grid-cols-1 gap-6 lg:grid-cols-3">
	<section class="lg:col-span-2" data-testid="recent-invoices">
		<div class="mb-3 flex items-center justify-between">
			<h2 class="ca-h3" style="margin-bottom:0">
				{t('clientcore.dashboard.recentInvoices')}
			</h2>
			<a href="/billing" class="ca-hero-pricing-link" data-testid="recent-invoices-view-all">
				{t('clientcore.dashboard.viewAll')} →
			</a>
		</div>

		<DataTable
			rows={data.recentInvoices}
			{columns}
			onRowClick={(row) => goto(`/billing/invoices/${row.id}`)}
			emptyTitle={t('clientcore.dashboard.noInvoices')}
			emptyDescription={t('clientcore.dashboard.noInvoicesDesc')}
		>
			{#snippet cell({ row, column, value })}
				{#if column.key === 'invoice_number'}
					<a
						href={`/billing/invoices/${row.id}`}
						class="font-medium text-primary hover:underline"
						data-testid={`row-invoice-${row.id}`}
						onclick={(e) => e.stopPropagation()}
					>
						{row.invoice_number}
					</a>
				{:else if column.key === 'created_at'}
					<DateText value={row.created_at} />
				{:else if column.key === 'due_date'}
					<DateText value={row.due_date} />
				{:else if column.key === 'total'}
					<MoneyText amount={row.total} />
				{:else if column.key === 'status'}
					<StatusBadge status={row.status} />
				{:else}
					{String(value ?? '—')}
				{/if}
			{/snippet}
		</DataTable>
	</section>

	<section data-testid="quick-links">
		<h2 class="ca-h3">{t('clientcore.dashboard.quickLinks')}</h2>
		<div class="ca-sidebar-card">
			<ul class="ca-list-group">
				{#each quickLinks as link (link.id)}
					<li>
						<a href={link.href} class="ca-list-item" data-testid={`quick-link-${link.id}`}>
							<span>{link.label}</span>
							<i class="fas fa-chevron-right" aria-hidden="true" style="margin-left:auto"></i>
						</a>
					</li>
				{/each}
			</ul>
		</div>
	</section>
</div>
