<script lang="ts">
	import { appName } from '$lib/appName';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import Alert from '$lib/components/Alert.svelte';
	import Breadcrumb from '$lib/components/Breadcrumb.svelte';
	import DataTable from '$lib/components/DataTable.svelte';
	import DateText from '$lib/components/DateText.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import MoneyText from '$lib/components/MoneyText.svelte';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import type { BreadcrumbItem, Column } from '$lib/components/types';
	import { t } from '$lib/i18n';
	import type { PageProps } from './$types';
	import { SERVICE_STATUSES, type Service } from './types';

	let { data }: PageProps = $props();

	const breadcrumbs: BreadcrumbItem[] = $derived([
		{ label: t('nav.home'), href: '/dashboard' },
		{ label: t('nav.services') }
	]);

	const columns: Column[] = $derived([
		{ key: 'product_name', label: t('clientsvc.list.colProduct') },
		{ key: 'domain', label: t('clientsvc.list.colDomain') },
		{ key: 'recurring_amount', label: t('clientsvc.list.colPrice'), align: 'right' },
		{ key: 'next_due_date', label: t('clientsvc.list.colNextDue') },
		{ key: 'status', label: t('clientsvc.list.colStatus') }
	]);

	function productLabel(s: Service): string {
		return s.product_name ?? t('clientsvc.list.productFallback', { id: s.product_id });
	}

	function gotoPage(p: number) {
		const params = new URLSearchParams(page.url.searchParams);
		params.set('page', String(p));
		goto(`/services?${params.toString()}`);
	}

	function changePerPage(perPage: number) {
		const params = new URLSearchParams(page.url.searchParams);
		params.set('per_page', String(perPage));
		params.set('page', '1');
		goto(`/services?${params.toString()}`);
	}

	const selectCls = 'ca-select';
	const inputCls = 'ca-input';
	const btnCls = 'ca-btn ca-btn-primary';
</script>

<svelte:head>
	<title>{t('clientsvc.list.title')} — {appName}</title>
</svelte:head>

<Breadcrumb items={breadcrumbs} />

<div class="mb-5 flex flex-wrap items-end justify-between gap-3">
	<div>
		<h1 class="ca-h1" style="margin-bottom:2px">{t('clientsvc.list.title')}</h1>
		<p class="ca-muted">{t('clientsvc.list.subtitle')}</p>
	</div>
	<a href="/order" class={btnCls} data-testid="services-order-button">
		{t('clientsvc.list.orderNow')}
	</a>
</div>

{#if data.loadError}
	<div class="mb-4">
		<Alert type="error" title={t('toast.error')}>
			{data.loadError}
			<a
				href={`/services${page.url.search}`}
				class="ml-2 font-semibold underline"
				data-testid="services-retry-link"
			>
				{t('action.retry')}
			</a>
		</Alert>
	</div>
{/if}

<DataTable
	{columns}
	rows={data.services}
	page={data.meta.page}
	perPage={data.meta.per_page}
	total={data.meta.total}
	onPageChange={gotoPage}
	onPerPageChange={changePerPage}
	onRowClick={(row) => goto(`/services/${row.id}`)}
>
	{#snippet filter()}
		<form method="GET" class="flex flex-wrap items-center gap-2" data-testid="services-filter-form">
			<label class="sr-only" for="services-status">{t('clientsvc.list.colStatus')}</label>
			<select
				id="services-status"
				name="status"
				value={data.filters.status}
				class={selectCls}
				style="width:auto;max-width:100%"
				data-testid="services-status-filter"
			>
				<option value="">{t('common.all')}</option>
				{#each SERVICE_STATUSES as s (s)}
					<option value={s}>{t(`status.${s}`)}</option>
				{/each}
			</select>
			<label class="sr-only" for="services-search">{t('common.search')}</label>
			<input
				id="services-search"
				type="search"
				name="search"
				value={data.filters.search}
				placeholder={t('clientsvc.list.searchPlaceholder')}
				class={inputCls}
				style="width:220px;max-width:100%"
				data-testid="services-search-input"
			/>
			<button type="submit" class={btnCls} data-testid="services-filter-submit">
				{t('action.filter')}
			</button>
		</form>
	{/snippet}

	{#snippet cell({ row, column, value })}
		{#if column.key === 'product_name'}
			<a
				href={`/services/${row.id}`}
				class="font-medium text-primary hover:underline"
				data-testid={`row-service-${row.id}`}
				onclick={(e) => e.stopPropagation()}
			>
				{productLabel(row)}
			</a>
		{:else if column.key === 'domain'}
			<span class="font-mono text-xs sm:text-sm">{row.domain || '—'}</span>
		{:else if column.key === 'recurring_amount'}
			<span class="whitespace-nowrap">
				<MoneyText amount={row.recurring_amount} />
				<span class="text-xs text-gray-400">/ {t(`clientsvc.cycle.${row.billing_cycle}`)}</span>
			</span>
		{:else if column.key === 'next_due_date'}
			<DateText value={row.next_due_date} />
		{:else if column.key === 'status'}
			<StatusBadge status={row.status} />
		{:else}
			{value ?? '—'}
		{/if}
	{/snippet}

	{#snippet empty()}
		<EmptyState
			title={t('clientsvc.list.empty')}
			description={t('clientsvc.list.emptyDescription')}
		>
			{#snippet action()}
				<a href="/order" class={btnCls} data-testid="services-empty-order-link">
					{t('clientsvc.list.orderNow')}
				</a>
			{/snippet}
		</EmptyState>
	{/snippet}
</DataTable>
