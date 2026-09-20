<script lang="ts">
	import { appName } from '$lib/appName';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import {
		Alert,
		Breadcrumb,
		DataTable,
		DateText,
		MoneyText,
		StatusBadge,
		Tabs
	} from '$lib/components';
	import type { Column, TabItem } from '$lib/components';
	import { t } from '$lib/i18n';
	import type { PageProps } from './$types';
	import type { Invoice, Transaction } from './billing';

	let { data }: PageProps = $props();

	const tabs = $derived<TabItem[]>([
		{ id: 'invoices', label: t('clientBilling.tabs.invoices'), href: '/billing?tab=invoices' },
		{
			id: 'transactions',
			label: t('clientBilling.tabs.transactions'),
			href: '/billing?tab=transactions'
		}
	]);

	const invoiceColumns = $derived<Column[]>([
		{ key: 'invoice_number', label: t('billing.invoiceNumber') },
		{ key: 'created_at', label: t('clientBilling.invoices.colDate') },
		{ key: 'due_date', label: t('billing.dueDate') },
		{ key: 'total', label: t('billing.total'), align: 'right' },
		{ key: 'status', label: t('clientBilling.invoices.colStatus'), align: 'center' }
	]);

	const transactionColumns = $derived<Column[]>([
		{ key: 'created_at', label: t('clientBilling.transactions.colDate') },
		{ key: 'invoice_id', label: t('clientBilling.transactions.colInvoice') },
		{ key: 'gateway', label: t('clientBilling.transactions.colGateway') },
		{ key: 'method_code', label: t('clientBilling.transactions.colMethod') },
		{ key: 'amount', label: t('billing.amount'), align: 'right' },
		{ key: 'fee', label: t('clientBilling.transactions.colFee'), align: 'right' },
		{ key: 'status', label: t('clientBilling.transactions.colStatus'), align: 'center' }
	]);

	function gotoPage(p: number) {
		const params = new URLSearchParams(page.url.searchParams);
		params.set('page', String(p));
		goto(`/billing?${params.toString()}`, { noScroll: false });
	}

	function changePerPage(perPage: number) {
		const params = new URLSearchParams(page.url.searchParams);
		params.set('per_page', String(perPage));
		params.set('page', '1');
		goto(`/billing?${params.toString()}`, { noScroll: false });
	}
</script>

<svelte:head>
	<title>{t('clientBilling.title')} — {appName}</title>
</svelte:head>

<Breadcrumb
	items={[{ label: t('nav.home'), href: '/dashboard' }, { label: t('clientBilling.title') }]}
/>

<div class="mb-4 flex flex-wrap items-start justify-between gap-3">
	<div>
		<h1 class="ca-h1" style="margin-bottom:4px">{t('clientBilling.title')}</h1>
		<p class="ca-muted">{t('clientBilling.subtitle')}</p>
	</div>
	<a href="/billing/deposit" data-testid="deposit-link" class="ca-btn ca-btn-primary">
		<i class="fas fa-plus-circle" aria-hidden="true"></i>
		{t('billing.addFunds')}
	</a>
</div>

<div class="mb-4">
	<Tabs {tabs} active={data.tab} />
</div>

{#if data.errorMessage}
	<div class="mb-4">
		<Alert type="error">{data.errorMessage}</Alert>
	</div>
{/if}

{#if data.tab === 'invoices'}
	<DataTable
		columns={invoiceColumns}
		rows={data.invoices}
		page={data.page}
		perPage={data.perPage}
		total={data.total}
		onPageChange={gotoPage}
		onPerPageChange={changePerPage}
		emptyTitle={t('clientBilling.invoices.empty')}
		emptyDescription={t('clientBilling.invoices.emptyDesc')}
		onRowClick={(row: Invoice) => goto(`/billing/invoices/${row.id}`)}
	>
		{#snippet filter()}
			<form method="GET" action="/billing" class="flex flex-wrap items-center gap-2">
				<input type="hidden" name="tab" value="invoices" />
				<label class="ca-label" style="margin-bottom:0" for="invoice-status-filter">
					{t('clientBilling.invoices.filterStatus')}
				</label>
				<select
					id="invoice-status-filter"
					name="status"
					data-testid="invoice-filter-status"
					class="ca-select"
					style="width:auto;min-width:170px"
					value={data.status}
				>
					<option value="">{t('common.all')}</option>
					{#each data.statusOptions as s (s)}
						<option value={s}>{t(`status.${s}`)}</option>
					{/each}
				</select>
				<button type="submit" data-testid="invoice-filter-submit" class="ca-btn ca-btn-default">
					<i class="fas fa-filter" aria-hidden="true"></i>
					{t('action.filter')}
				</button>
			</form>
		{/snippet}

		{#snippet cell({ row, column, value }: { row: Invoice; column: Column; value: unknown })}
			{#if column.key === 'invoice_number'}
				<a
					href={`/billing/invoices/${row.id}`}
					data-testid={`row-invoice-${row.id}`}
					class="font-medium text-primary hover:underline"
					onclick={(e) => e.stopPropagation()}
				>
					{row.invoice_number}
				</a>
			{:else if column.key === 'created_at' || column.key === 'due_date'}
				<DateText value={value as string} />
			{:else if column.key === 'total'}
				<MoneyText amount={row.total} currency={row.currency || 'IDR'} />
			{:else if column.key === 'status'}
				<StatusBadge status={row.status} />
			{:else}
				{value ?? '—'}
			{/if}
		{/snippet}
	</DataTable>
{:else}
	<DataTable
		columns={transactionColumns}
		rows={data.transactions}
		page={data.page}
		perPage={data.perPage}
		total={data.total}
		onPageChange={gotoPage}
		onPerPageChange={changePerPage}
		emptyTitle={t('clientBilling.transactions.empty')}
		emptyDescription={t('clientBilling.transactions.emptyDesc')}
	>
		{#snippet cell({ row, column, value }: { row: Transaction; column: Column; value: unknown })}
			{#if column.key === 'created_at'}
				<DateText value={value as string} mode="datetime" />
			{:else if column.key === 'invoice_id'}
				<a
					href={`/billing/invoices/${row.invoice_id}`}
					data-testid={`row-transaction-${row.id}`}
					class="font-medium text-primary hover:underline"
				>
					#{row.invoice_id}
				</a>
			{:else if column.key === 'amount'}
				<MoneyText amount={row.amount} />
			{:else if column.key === 'fee'}
				<MoneyText amount={row.fee ?? 0} />
			{:else if column.key === 'status'}
				<StatusBadge status={row.status} />
			{:else}
				{value ?? '—'}
			{/if}
		{/snippet}
	</DataTable>
{/if}
