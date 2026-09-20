<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
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
	import { toast } from '$lib/stores/toast.svelte';
	import type { SubmitFunction } from '@sveltejs/kit';
	import type { PageProps } from './$types';
	import { DOMAIN_STATUSES, type DomainRow } from './shared';

	let { data }: PageProps = $props();

	let togglePending = $state<number | null>(null);

	const columns: Column[] = $derived([
		{ key: 'name', label: t('clientDomains.list.colDomain') },
		{ key: 'status', label: t('clientDomains.list.colStatus') },
		{ key: 'expiry_date', label: t('clientDomains.list.colExpiry') },
		{ key: 'recurring_amount', label: t('clientDomains.list.colAmount'), align: 'right' },
		{ key: 'auto_renew', label: t('clientDomains.list.colAutoRenew'), align: 'center' },
		{ key: 'actions', label: '', align: 'right' }
	]);

	const breadcrumb: BreadcrumbItem[] = $derived([
		{ label: t('nav.home'), href: '/dashboard' },
		{ label: t('nav.domains') }
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

	const toggleEnhance =
		(row: DomainRow): SubmitFunction =>
		() => {
			togglePending = row.id;
			const next = !row.auto_renew;
			return async ({ result, update }) => {
				togglePending = null;
				if (result.type === 'success') {
					toast.success(
						t(next ? 'clientDomains.toast.autoRenewOn' : 'clientDomains.toast.autoRenewOff')
					);
				} else if (result.type === 'failure' || result.type === 'error') {
					toast.error(t('clientDomains.errors.actionFailed'));
				}
				await update();
			};
		};

	const inputCls = 'ca-input';
</script>

<svelte:head>
	<title>{t('clientDomains.list.title')} — {appName}</title>
</svelte:head>

<Breadcrumb items={breadcrumb} />

<div class="mb-5">
	<h1 class="ca-h1" style="margin-bottom:2px">{t('clientDomains.list.title')}</h1>
	<p class="ca-muted">{t('clientDomains.list.subtitle')}</p>
</div>

{#if data.loadError}
	<div class="mb-4" data-testid="domains-load-error">
		<Alert type="error">{t('clientDomains.errors.loadFailed', { message: data.loadError })}</Alert>
	</div>
{/if}

<div data-testid="domains-table">
	<DataTable
		{columns}
		rows={data.domains}
		page={data.meta.page}
		perPage={data.meta.per_page}
		total={data.meta.total}
		{onPageChange}
		{onPerPageChange}
		rowKey={(row) => row.id}
		onRowClick={(row) => goto(`/domains/${row.id}`)}
	>
		{#snippet filter()}
			<form method="GET" class="flex flex-wrap items-end gap-3">
				<div class="w-full sm:w-64">
					<label class="mb-1 block text-xs font-medium text-gray-600" for="domain-search">
						{t('common.search')}
					</label>
					<input
						id="domain-search"
						type="search"
						name="search"
						value={data.search}
						placeholder={t('clientDomains.list.searchPlaceholder')}
						class={inputCls}
						data-testid="domain-search-input"
					/>
				</div>
				<div class="w-full sm:w-48">
					<label class="mb-1 block text-xs font-medium text-gray-600" for="domain-status">
						{t('clientDomains.list.statusLabel')}
					</label>
					<select
						id="domain-status"
						name="status"
						class="ca-select"
						data-testid="domain-status-filter"
					>
						<option value="">{t('common.all')}</option>
						{#each DOMAIN_STATUSES as s (s)}
							<option value={s} selected={data.status === s}>{t(`status.${s}`)}</option>
						{/each}
					</select>
				</div>
				<button type="submit" class="ca-btn ca-btn-primary" data-testid="domain-filter-submit">
					{t('action.filter')}
				</button>
			</form>
		{/snippet}

		{#snippet cell({ row, column })}
			{#if column.key === 'name'}
				<a
					href={`/domains/${row.id}`}
					class="font-medium text-primary hover:underline"
					data-testid={`row-domain-${row.id}`}
					onclick={(e) => e.stopPropagation()}
				>
					{row.name}
				</a>
			{:else if column.key === 'status'}
				<StatusBadge status={row.status} />
			{:else if column.key === 'expiry_date'}
				<DateText value={row.expiry_date} />
			{:else if column.key === 'recurring_amount'}
				<MoneyText amount={row.recurring_amount} />
				<span class="block text-xs text-gray-400">
					{t(`clientDomains.cycle.${row.billing_cycle}`)}
				</span>
			{:else if column.key === 'auto_renew'}
				<form
					method="POST"
					action="?/autorenew"
					use:enhance={toggleEnhance(row)}
					class="inline-flex"
				>
					<input type="hidden" name="id" value={row.id} />
					<input type="hidden" name="value" value={row.auto_renew ? 'false' : 'true'} />
					<button
						type="submit"
						role="switch"
						aria-checked={row.auto_renew}
						aria-label={t('clientDomains.list.colAutoRenew')}
						disabled={togglePending === row.id}
						data-testid={`domain-autorenew-toggle-${row.id}`}
						onclick={(e) => e.stopPropagation()}
						class={`relative inline-flex h-5 w-9 items-center rounded-full transition disabled:opacity-60 ${
							row.auto_renew ? 'bg-success' : 'bg-gray-300'
						}`}
					>
						<span
							class={`inline-block h-4 w-4 transform rounded-full bg-white shadow transition ${
								row.auto_renew ? 'translate-x-4' : 'translate-x-0.5'
							}`}
						></span>
					</button>
				</form>
			{:else if column.key === 'actions'}
				<a
					href={`/domains/${row.id}`}
					class="text-sm font-medium text-primary hover:underline"
					data-testid={`domain-manage-${row.id}`}
					onclick={(e) => e.stopPropagation()}
				>
					{t('clientDomains.list.manage')}
				</a>
			{/if}
		{/snippet}

		{#snippet empty()}
			<EmptyState
				title={t('clientDomains.list.emptyTitle')}
				description={t('clientDomains.list.emptyDesc')}
			>
				{#snippet action()}
					<a href="/order/domain" class="ca-btn ca-btn-primary" data-testid="domain-register-cta">
						{t('clientDomains.list.registerCta')}
					</a>
				{/snippet}
			</EmptyState>
		{/snippet}
	</DataTable>
</div>
