<script lang="ts">
	import { appName } from '$lib/appName';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import Alert from '$lib/components/Alert.svelte';
	import Breadcrumb from '$lib/components/Breadcrumb.svelte';
	import DataTable from '$lib/components/DataTable.svelte';
	import DateText from '$lib/components/DateText.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import type { BreadcrumbItem, Column } from '$lib/components/types';
	import { t } from '$lib/i18n';
	import type { PageProps } from './$types';
	import { SNIPE_STATUSES, snipeVariant } from './shared';

	let { data }: PageProps = $props();

	const columns: Column[] = $derived([
		{ key: 'domain_name', label: t('portal.snipe.list.colDomain') },
		{ key: 'status', label: t('portal.snipe.list.colStatus') },
		{ key: 'plan_name', label: t('portal.snipe.list.colPlan') },
		{ key: 'deadline_at', label: t('portal.snipe.list.colDeadline') },
		{ key: 'attempts', label: t('portal.snipe.list.colAttempts'), align: 'right' },
		{ key: 'actions', label: '', align: 'right' }
	]);

	const breadcrumb: BreadcrumbItem[] = $derived([
		{ label: t('nav.home'), href: '/dashboard' },
		{ label: t('portal.snipe.list.title') }
	]);

	function onPageChange(p: number) {
		const url = new URL(page.url);
		url.searchParams.set('page', String(p));
		goto(`${url.pathname}${url.search}`, { keepFocus: true });
	}

	function onPerPageChange(perPage: number) {
		const url = new URL(page.url);
		url.searchParams.set('per_page', String(perPage));
		url.searchParams.set('page', '1');
		goto(`${url.pathname}${url.search}`, { keepFocus: true });
	}
</script>

<svelte:head>
	<title>{t('portal.snipe.list.title')} — {appName}</title>
</svelte:head>

<Breadcrumb items={breadcrumb} />

<div
	class="mb-5"
	style="display:flex;flex-wrap:wrap;align-items:flex-start;justify-content:space-between;gap:12px"
>
	<div>
		<h1 class="ca-h1" style="margin-bottom:2px">{t('portal.snipe.list.title')}</h1>
		<p class="ca-muted">{t('portal.snipe.list.subtitle')}</p>
	</div>
	<a href="/snipes/new" class="ca-btn ca-btn-primary" data-testid="snipe-new-cta">
		<i class="fas fa-crosshairs" aria-hidden="true"></i>
		{t('portal.snipe.list.newCta')}
	</a>
</div>

{#if data.loadError}
	<div class="mb-4" data-testid="snipes-load-error">
		<Alert type="error">{t('portal.snipe.list.loadFailed', { message: data.loadError })}</Alert>
	</div>
{/if}

<div data-testid="snipes-table">
	<DataTable
		{columns}
		rows={data.snipes}
		page={data.meta.page}
		perPage={data.meta.per_page}
		total={data.meta.total}
		{onPageChange}
		{onPerPageChange}
		rowKey={(row) => row.id}
		onRowClick={(row) => goto(`/snipes/${row.id}`)}
	>
		{#snippet filter()}
			<form method="GET" class="flex flex-wrap items-end gap-3">
				<div class="w-full sm:w-64">
					<label class="mb-1 block text-xs font-medium text-gray-600" for="snipe-search">
						{t('common.search')}
					</label>
					<input
						id="snipe-search"
						type="search"
						name="search"
						value={data.search}
						placeholder={t('portal.snipe.list.searchPlaceholder')}
						class="ca-input"
						data-testid="snipe-search-input"
					/>
				</div>
				<div class="w-full sm:w-52">
					<label class="mb-1 block text-xs font-medium text-gray-600" for="snipe-status">
						{t('portal.snipe.list.statusLabel')}
					</label>
					<select
						id="snipe-status"
						name="status"
						class="ca-select"
						data-testid="snipe-status-filter"
					>
						<option value="">{t('common.all')}</option>
						{#each SNIPE_STATUSES as s (s)}
							<option value={s} selected={data.status === s}>{t(`portal.snipe.status.${s}`)}</option
							>
						{/each}
					</select>
				</div>
				<button type="submit" class="ca-btn ca-btn-primary" data-testid="snipe-filter-submit">
					{t('action.filter')}
				</button>
			</form>
		{/snippet}

		{#snippet cell({ row, column })}
			{#if column.key === 'domain_name'}
				<a
					href={`/snipes/${row.id}`}
					class="font-medium text-primary hover:underline"
					data-testid={`row-snipe-${row.id}`}
					onclick={(e) => e.stopPropagation()}
				>
					{row.domain_name}
				</a>
			{:else if column.key === 'status'}
				<span data-testid={`snipe-row-status-${row.id}`}>
					<StatusBadge
						status={row.status}
						variant={snipeVariant(row.status)}
						label={t(`portal.snipe.status.${row.status}`)}
					/>
				</span>
			{:else if column.key === 'plan_name'}
				{row.plan_name || '—'}
			{:else if column.key === 'deadline_at'}
				<DateText value={row.deadline_at} mode="datetime" />
			{:else if column.key === 'attempts'}
				{row.attempts}
			{:else if column.key === 'actions'}
				<a
					href={`/snipes/${row.id}`}
					class="text-sm font-medium text-primary hover:underline"
					onclick={(e) => e.stopPropagation()}
				>
					{t('portal.snipe.list.view')}
				</a>
			{/if}
		{/snippet}

		{#snippet empty()}
			<EmptyState
				title={t('portal.snipe.list.emptyTitle')}
				description={t('portal.snipe.list.emptyDesc')}
			>
				{#snippet action()}
					<a href="/snipes/new" class="ca-btn ca-btn-primary" data-testid="snipe-empty-cta">
						{t('portal.snipe.list.newCta')}
					</a>
				{/snippet}
			</EmptyState>
		{/snippet}
	</DataTable>
</div>
