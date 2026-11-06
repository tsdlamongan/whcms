<script lang="ts">
	import { appName } from '$lib/appName';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import Alert from '$lib/components/Alert.svelte';
	import Breadcrumb from '$lib/components/Breadcrumb.svelte';
	import DataTable from '$lib/components/DataTable.svelte';
	import DateText from '$lib/components/DateText.svelte';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import Tabs from '$lib/components/Tabs.svelte';
	import type { Column, TabItem } from '$lib/components/types';
	import { t } from '$lib/i18n';
	import type { PageProps } from './$types';
	import { EMAIL_LOG_STATUSES, type EmailLogEntry } from './types';

	let { data }: PageProps = $props();

	const columns: Column[] = $derived([
		{ key: 'created_at', label: t('clientEmailHistory.colDate'), class: 'w-44' },
		{ key: 'subject', label: t('clientEmailHistory.colSubject') },
		{ key: 'template_key', label: t('clientEmailHistory.colTemplate'), class: 'w-40' },
		{ key: 'status', label: t('clientEmailHistory.colStatus'), class: 'w-32' }
	]);

	const filterTabs: TabItem[] = $derived([
		{ id: 'all', label: t('common.all'), href: '/account/email-history' },
		...EMAIL_LOG_STATUSES.map((s) => ({
			id: s,
			label: t(`status.${s}`),
			href: `/account/email-history?status=${s}`
		}))
	]);

	const activeTab = $derived(data.status || 'all');

	function gotoPage(p: number) {
		const url = new URL(page.url);
		url.searchParams.set('page', String(p));
		goto(url.pathname + url.search);
	}

	function changePerPage(perPage: number) {
		const url = new URL(page.url);
		url.searchParams.set('per_page', String(perPage));
		url.searchParams.set('page', '1');
		goto(url.pathname + url.search);
	}
</script>

<svelte:head>
	<title>{t('clientEmailHistory.title')} — {appName}</title>
</svelte:head>

<Breadcrumb
	items={[{ label: t('nav.home'), href: '/dashboard' }, { label: t('clientEmailHistory.title') }]}
/>

<div class="mb-4">
	<h1 class="ca-h1" style="margin-bottom:4px">{t('clientEmailHistory.title')}</h1>
	<p class="ca-muted">{t('clientEmailHistory.subtitle')}</p>
</div>

{#if data.loadError}
	<div class="mb-4">
		<Alert type="error" title={t('clientEmailHistory.loadFailed')}>{data.loadError}</Alert>
	</div>
{/if}

<div class="mb-4" data-testid="email-history-status-filter">
	<Tabs tabs={filterTabs} active={activeTab} />
</div>

<div data-testid="email-history-list">
	<DataTable
		{columns}
		rows={data.emails}
		page={data.meta.page}
		perPage={data.meta.per_page}
		total={data.meta.total}
		onPageChange={gotoPage}
		onPerPageChange={changePerPage}
		emptyTitle={t('clientEmailHistory.emptyTitle')}
		emptyDescription={t('clientEmailHistory.emptyDescription')}
	>
		{#snippet cell({ row, column, value }: { row: EmailLogEntry; column: Column; value: unknown })}
			{#if column.key === 'created_at'}
				<DateText value={row.created_at} mode="datetime" />
			{:else if column.key === 'subject'}
				<span class="font-medium text-gray-800" data-testid={`row-email-${row.id}`}>
					{row.subject}
				</span>
			{:else if column.key === 'status'}
				<span data-testid={`email-status-${row.id}`}>
					<StatusBadge status={row.status} />
				</span>
			{:else}
				{value ?? '—'}
			{/if}
		{/snippet}
	</DataTable>
</div>
