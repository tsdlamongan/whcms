<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import HpPager from '$lib/components/hp/HpPager.svelte';
	import HpStatCard from '$lib/components/hp/HpStatCard.svelte';
	import { fmtDateTime } from '$lib/date';
	import { formatIDR } from '$lib/money';
	import { toast } from '$lib/stores/toast.svelte';
	import type { SubmitFunction } from '@sveltejs/kit';
	import type { PageProps } from './$types';
	import { SNIPE_SETTINGS, SNIPE_STATUSES, snipeStatusClass } from './types';

	let { data }: PageProps = $props();

	function hasActiveFilters(): boolean {
		return !!(data.search || data.status);
	}
	let filterOpen = $state(hasActiveFilters());
	let settingsOpen = $state(false);
	let saving = $state(false);

	const total = $derived(data.meta?.total ?? data.snipes.length);
	const curPage = $derived(data.meta?.page ?? data.page);
	const perPage = $derived(data.meta?.per_page ?? data.perPage);
	const from = $derived(total === 0 ? 0 : (curPage - 1) * perPage + 1);
	const to = $derived(Math.min(curPage * perPage, total));

	const counts = $derived(data.overview?.counts ?? {});
	const supervisor = $derived.by(() => {
		const o = data.overview;
		if (!o) return { label: 'Unknown', cls: 'cancelled' };
		if (o.pause_reason) return { label: 'Paused', cls: 'terminated' };
		if (!o.enabled) return { label: 'Disabled', cls: 'suspended' };
		return { label: 'Running', cls: 'active' };
	});

	const saveHandler: SubmitFunction = () => {
		saving = true;
		return async ({ result, update }) => {
			saving = false;
			if (result.type === 'success') toast.success('Hunt settings saved');
			else if (result.type === 'failure') {
				const d = result.data as { errorMessage?: string } | undefined;
				toast.error(d?.errorMessage ?? 'Could not save the settings');
			}
			await update({ reset: false });
		};
	};
</script>

<svelte:head>
	<title>Domain Snipes — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Domain Snipes</h1>

<div class="hp-info">
	<i class="fas fa-info-circle" style="margin-right:8px"></i>Background hunts that keep trying to
	register a dropping domain until the registrar accepts it. A hunt probes cheaply, then bursts
	register calls inside the drop window. Start a free hunt for a client below; paid client snipes
	appear here too.
</div>

{#if data.listError}
	<div class="hp-alert-yellow" data-testid="snipe-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

{#if data.overview?.pause_reason}
	<div class="hp-alert-red" data-testid="snipe-paused">
		<i class="fas fa-pause-circle" style="margin-right:8px"></i><strong
			>All hunts are paused:</strong
		>
		{data.overview.pause_reason}. They resume automatically once the registrar account is healthy
		again.
	</div>
{/if}

<div class="stat-grid" data-testid="snipes-overview">
	<div data-testid="snipe-stat-hunting">
		<HpStatCard
			value={data.overview?.hunting ?? '—'}
			label="Hunting Now"
			icon="fas fa-crosshairs"
			color="#64b843"
		/>
	</div>
	<div data-testid="snipe-stat-active">
		<HpStatCard
			value={counts.active ?? 0}
			label="Active Snipes"
			icon="fas fa-hourglass-half"
			color="#e6a23c"
			href="/admin/snipes?status=active"
		/>
	</div>
	<div data-testid="snipe-stat-won">
		<HpStatCard
			value={counts.won ?? 0}
			label="Domains Won"
			icon="fas fa-trophy"
			color="#56bfbd"
			href="/admin/snipes?status=won"
		/>
	</div>
	<div data-testid="snipe-stat-balance">
		<HpStatCard
			value={data.overview?.reseller_balance != null
				? formatIDR(data.overview.reseller_balance)
				: '—'}
			label="Reseller Balance"
			icon="fas fa-wallet"
			color="#337ab7"
		/>
	</div>
</div>

<div style="margin:10px 0;display:flex;flex-wrap:wrap;gap:8px;align-items:center">
	<a href="/admin/snipes/new" class="hp-btn hp-btn-primary" data-testid="snipe-add-new">
		<i class="fas fa-plus"></i>Start a Hunt
	</a>
	<a href="/admin/snipes/plans" class="hp-btn" data-testid="snipe-plans-link">
		<i class="fas fa-tags"></i>Plans
	</a>
	<button class="hp-btn" onclick={() => (filterOpen = !filterOpen)}>Search/Filter</button>
	{#if data.canEditSettings}
		<button
			class="hp-btn"
			data-testid="snipe-settings-toggle"
			onclick={() => (settingsOpen = !settingsOpen)}
		>
			<i class="fas fa-cog"></i>Hunt Settings
		</button>
	{/if}
	<span style="margin-left:auto;color:#666">
		Supervisor:
		<span class={`hp-badge ${supervisor.cls}`} data-testid="snipe-supervisor-state"
			>{supervisor.label}</span
		>
	</span>
</div>

{#if data.overview?.reseller_error}
	<div class="hp-alert-yellow" data-testid="snipe-reseller-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>Reseller account: {data
			.overview.reseller_error}
	</div>
{/if}

{#if settingsOpen && data.canEditSettings}
	<form
		method="POST"
		action="?/saveSettings"
		class="hp-panel"
		style="padding:16px 20px;margin-bottom:16px"
		data-testid="snipe-settings-form"
		use:enhance={saveHandler}
	>
		<div class="settings-grid">
			{#each SNIPE_SETTINGS as def (def.key)}
				<div class="hp-formrow" style="display:block;margin-bottom:12px">
					{#if def.kind === 'bool'}
						<label class="hp-checkline" style="padding:0">
							<input
								type="checkbox"
								name={def.key}
								checked={data.settingValues[def.key] === true}
								data-testid={`snipe-setting-${def.key.slice(6)}`}
							/>
							<strong>{def.label}</strong>
						</label>
					{:else}
						<label for={`s-${def.key}`}
							><strong>{def.label}</strong>{def.unit ? ` (${def.unit})` : ''}</label
						>
						<input
							id={`s-${def.key}`}
							class="hp-input"
							type="number"
							min="0"
							step="1"
							name={def.key}
							value={data.settingValues[def.key]}
							data-testid={`snipe-setting-${def.key.slice(6)}`}
						/>
					{/if}
					<div class="hp-help" style="margin-top:3px">{def.help}</div>
				</div>
			{/each}
		</div>
		<button
			class="hp-btn hp-btn-primary"
			type="submit"
			disabled={saving}
			aria-busy={saving}
			data-testid="snipe-settings-save"
		>
			{#if saving}<i class="fas fa-spinner fa-spin"></i>{/if}Save Hunt Settings
		</button>
	</form>
{/if}

{#if filterOpen}
	<form method="get" class="hp-filter" data-testid="snipe-filter-form">
		<div class="grow">
			<div class="hp-field-label">Domain</div>
			<input
				class="hp-input"
				type="search"
				name="search"
				value={data.search}
				data-testid="snipe-search-input"
			/>
		</div>
		<div style="flex:0 0 190px">
			<div class="hp-field-label">Status</div>
			<select class="hp-select" name="status" data-testid="snipe-status-filter">
				<option value="" selected={data.status === ''}>Any</option>
				{#each SNIPE_STATUSES as s (s)}
					<option value={s} selected={data.status === s}>{s}</option>
				{/each}
			</select>
		</div>
		<button class="hp-btn hp-btn-primary" type="submit" data-testid="snipe-filter-submit"
			><i class="fas fa-search"></i>Filter</button
		>
	</form>
{/if}

<div class="hp-count" style="margin-bottom:8px">{total} Records Found, Showing {from} to {to}</div>

<div class="hp-scroll">
	<table class="hp-table" data-testid="snipes-table">
		<thead>
			<tr>
				<th style="width:60px">ID</th>
				<th>Domain</th>
				<th>Client</th>
				<th class="c" style="width:90px">Source</th>
				<th class="c" style="width:130px">Status</th>
				<th class="c" style="width:90px">Phase</th>
				<th class="r" style="width:90px">Attempts</th>
				<th class="c" style="width:140px">Deadline</th>
			</tr>
		</thead>
		<tbody>
			{#each data.snipes as sn (sn.id)}
				<tr class="hp-row">
					<td>{sn.id}</td>
					<td
						><a class="cell-link" href={`/admin/snipes/${sn.id}`} data-testid={`row-snipe-${sn.id}`}
							>{sn.domain_name}</a
						></td
					>
					<td
						><a class="cell-link" href={`/admin/clients/${sn.client_id}`}
							>{sn.client_name ?? `#${sn.client_id}`}</a
						></td
					>
					<td class="c" style="color:#555">{sn.source}</td>
					<td class="c"
						><span
							class={`hp-badge ${snipeStatusClass(sn.status)}`}
							data-testid={`snipe-row-status-${sn.id}`}>{sn.status}</span
						></td
					>
					<td class="c" style="color:#555">{sn.status === 'active' ? sn.phase : '—'}</td>
					<td class="r">{sn.attempts}</td>
					<td class="c" style="color:#666">{fmtDateTime(sn.deadline_at)}</td>
				</tr>
			{:else}
				<tr
					><td colspan="8" style="text-align:center;padding:28px;color:#999">No snipes found</td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

<HpPager page={curPage} {perPage} {total} />

<style>
	.stat-grid {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(210px, 1fr));
		gap: 14px;
		margin-bottom: 8px;
	}
	.settings-grid {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
		gap: 0 28px;
	}
</style>
