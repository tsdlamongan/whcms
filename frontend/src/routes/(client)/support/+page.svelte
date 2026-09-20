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
	import { TICKET_PRIORITIES, TICKET_STATUSES, type Ticket } from './types';

	let { data }: PageProps = $props();

	const columns: Column[] = $derived([
		{ key: 'ticket_number', label: t('supportfe.list.colNumber'), class: 'w-32' },
		{ key: 'subject', label: t('supportfe.list.colSubject') },
		{ key: 'department_id', label: t('supportfe.list.colDepartment') },
		{ key: 'priority', label: t('supportfe.list.colPriority'), class: 'w-28' },
		{ key: 'status', label: t('supportfe.list.colStatus'), class: 'w-36' },
		{ key: 'last_reply_at', label: t('supportfe.list.colLastReply'), class: 'w-44' }
	]);

	const filterTabs: TabItem[] = $derived([
		{ id: 'all', label: t('common.all'), href: '/support' },
		...TICKET_STATUSES.map((s) => ({
			id: s,
			label: t(`status.${s}`),
			href: `/support?status=${s}`
		}))
	]);

	const activeTab = $derived(data.status || 'all');

	const deptNames = $derived(new Map(data.departments.map((d) => [d.id, d.name])));

	const priorityVariant: Record<string, 'gray' | 'blue' | 'red'> = {
		low: 'gray',
		medium: 'blue',
		high: 'red'
	};

	function priorityLabel(priority: string): string {
		return (TICKET_PRIORITIES as readonly string[]).includes(priority)
			? t(`supportfe.priority.${priority}`)
			: priority;
	}

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
	<title>{t('supportfe.list.title')} — {appName}</title>
</svelte:head>

<Breadcrumb items={[{ label: t('nav.home'), href: '/dashboard' }, { label: t('nav.support') }]} />

<div class="mb-4 flex flex-wrap items-start justify-between gap-3">
	<div>
		<h1 class="ca-h1" style="margin-bottom:4px">{t('supportfe.list.title')}</h1>
		<p class="ca-muted">{t('supportfe.list.subtitle')}</p>
	</div>
	<a href="/support/new" class="ca-btn ca-btn-primary" data-testid="new-ticket-button">
		<i class="fas fa-plus" aria-hidden="true"></i>
		{t('supportfe.list.newTicket')}
	</a>
</div>

{#if data.loadError}
	<div class="mb-4">
		<Alert type="error" title={t('supportfe.list.loadFailed')}>{data.loadError}</Alert>
	</div>
{/if}

<div class="mb-4" data-testid="ticket-status-filter">
	<Tabs tabs={filterTabs} active={activeTab} />
</div>

<div data-testid="ticket-list">
	<DataTable
		{columns}
		rows={data.tickets}
		page={data.meta.page}
		perPage={data.meta.per_page}
		total={data.meta.total}
		onPageChange={gotoPage}
		onPerPageChange={changePerPage}
		onRowClick={(row: Ticket) => goto(`/support/${row.id}`)}
		emptyTitle={t('supportfe.list.emptyTitle')}
		emptyDescription={t('supportfe.list.emptyDescription')}
	>
		{#snippet cell({ row, column, value })}
			{#if column.key === 'ticket_number'}
				<a
					href={`/support/${row.id}`}
					class="font-mono text-xs font-semibold text-primary hover:underline"
					data-testid={`row-ticket-${row.id}`}
				>
					#{row.ticket_number}
				</a>
			{:else if column.key === 'subject'}
				<span class="font-medium text-gray-800">{row.subject}</span>
			{:else if column.key === 'department_id'}
				{deptNames.get(row.department_id) ?? '—'}
			{:else if column.key === 'priority'}
				<StatusBadge
					status={row.priority}
					variant={priorityVariant[row.priority] ?? 'gray'}
					label={priorityLabel(row.priority)}
				/>
			{:else if column.key === 'status'}
				<span data-testid={`ticket-status-${row.id}`}>
					<StatusBadge status={row.status} />
				</span>
			{:else if column.key === 'last_reply_at'}
				<DateText value={row.last_reply_at ?? row.created_at} mode="datetime" />
			{:else}
				{value ?? '—'}
			{/if}
		{/snippet}
	</DataTable>
</div>
