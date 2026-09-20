<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import FormField from '$lib/components/FormField.svelte';
	import HpPager from '$lib/components/hp/HpPager.svelte';
	import LoadingButton from '$lib/components/LoadingButton.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import { toast } from '$lib/stores/toast.svelte';
	import type { SubmitFunction } from '@sveltejs/kit';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	// servers pagination (server-side, ?page=)
	const serverTotal = $derived(data.meta?.total ?? data.servers.length);
	const serverPage = $derived(data.meta?.page ?? data.page);
	const serverPerPage = $derived(data.meta?.per_page ?? data.perPage);
	const serverFrom = $derived(serverTotal === 0 ? 0 : (serverPage - 1) * serverPerPage + 1);
	const serverTo = $derived(Math.min(serverPage * serverPerPage, serverTotal));

	// server-groups pagination (client-side slice, ?gpage=/?gper_page=)
	const groupPerPage = $derived(data.groupPerPage);
	const groupTotal = $derived(data.groups.length);
	const groupPages = $derived(Math.max(1, Math.ceil(groupTotal / groupPerPage)));
	const groupPage = $derived(Math.min(Math.max(1, data.groupPage), groupPages));
	const pagedGroups = $derived(
		data.groups.slice((groupPage - 1) * groupPerPage, groupPage * groupPerPage)
	);
	const groupFrom = $derived(groupTotal === 0 ? 0 : (groupPage - 1) * groupPerPage + 1);
	const groupTo = $derived(Math.min(groupPage * groupPerPage, groupTotal));
	const moduleLabels: Record<string, string> = {
		cpanel: 'cPanel',
		directadmin: 'DirectAdmin',
		none: 'None'
	};

	let testingId = $state<number | null>(null);

	// Group create/edit modal state.
	let groupModalOpen = $state(false);
	let groupId = $state(0);
	let groupName = $state('');
	let groupStrategy = $state('round_robin');
	let groupSaving = $state(false);

	// Group delete confirm state.
	let deleteGroupId = $state(0);
	let deleteGroupOpen = $state(false);
	let deleteGroupLoading = $state(false);
	let deleteGroupForm = $state<HTMLFormElement>();

	const strategyOptions = [
		{ value: 'round_robin', label: 'Round Robin' },
		{ value: 'least_used', label: 'Least Used' }
	];

	const errorText = $derived(
		form?.errorKey ? 'Group name is required' : (form?.errorMessage ?? null)
	);

	function groupNameOf(id: number | null): string {
		if (!id) return '—';
		return data.groups.find((g) => g.id === id)?.name ?? `#${id}`;
	}
	function membersOf(gid: number): string {
		const names = data.servers.filter((s) => s.group_id === gid).map((s) => s.name);
		return names.length ? names.join(', ') : 'No members';
	}
	function openCreateGroup() {
		groupId = 0;
		groupName = '';
		groupStrategy = 'round_robin';
		groupModalOpen = true;
	}
	function openEditGroup(g: (typeof data.groups)[number]) {
		groupId = g.id;
		groupName = g.name;
		groupStrategy = g.strategy;
		groupModalOpen = true;
	}
	function askDeleteGroup(id: number) {
		deleteGroupId = id;
		deleteGroupOpen = true;
	}

	const testHandler: SubmitFunction = ({ formData }) => {
		testingId = Number(formData.get('id')) || null;
		return async ({ result, update }) => {
			testingId = null;
			if (result.type === 'success') {
				const d = result.data as { message?: string } | undefined;
				toast.success(d?.message ? `Connection OK — ${d.message}` : 'Connection OK');
			} else if (result.type === 'failure') {
				const d = result.data as { errorMessage?: string } | undefined;
				toast.error(
					d?.errorMessage ? `Connection failed — ${d.errorMessage}` : 'Connection failed'
				);
			}
			await update();
		};
	};

	function simpleHandler(
		successMsg: string,
		setLoading: (v: boolean) => void,
		onDone?: () => void
	): SubmitFunction {
		return () => {
			setLoading(true);
			return async ({ result, update }) => {
				setLoading(false);
				if (result.type === 'success') {
					onDone?.();
					toast.success(successMsg);
				} else if (result.type === 'failure') {
					const d = result.data as { errorKey?: string; errorMessage?: string } | undefined;
					toast.error(d?.errorMessage ?? 'Action failed');
				}
				await update();
			};
		};
	}
</script>

<svelte:head>
	<title>Servers — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Servers</h1>

<p class="hp-lead">
	Configure all your servers so that {appName} can communicate with them. You must select a default server
	for automatic setup to function correctly.
</p>

{#if errorText}
	<div class="hp-alert-red" data-testid="server-action-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{errorText}
	</div>
{/if}
{#if data.listError}
	<div class="hp-alert-red" data-testid="server-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

<div style="display:flex;gap:8px;margin-bottom:16px;flex-wrap:wrap">
	<a class="hp-btn" href="/admin/servers/new" data-testid="server-create-link"
		><i class="fas fa-plus" style="color:#5cb85c"></i>Add New Server</a
	>
	<span data-testid="server-group-create">
		<button type="button" class="hp-btn" onclick={openCreateGroup}
			><i class="fas fa-layer-group" style="color:#5b9bd5"></i>Create New Group</button
		>
	</span>
</div>

<div class="hp-count">{serverTotal} Servers Found, Showing {serverFrom} to {serverTo}</div>

<div class="hp-scroll">
	<table class="hp-table" style="margin-bottom:26px">
		<thead>
			<tr>
				<th style="width:60px">ID</th>
				<th>Server Name</th>
				<th class="c">Module</th>
				<th>Hostname</th>
				<th>Group</th>
				<th class="c">Usage</th>
				<th class="c" style="width:90px">Status</th>
				<th class="c" style="width:90px"></th>
			</tr>
		</thead>
		<tbody>
			{#each data.servers as s (s.id)}
				<tr class="hp-row">
					<td>{s.id}</td>
					<td
						><a class="cell-link" href={`/admin/servers/${s.id}`} data-testid={`row-server-${s.id}`}
							>{s.name}</a
						></td
					>
					<td class="c"
						><span class="hp-stext" style="color:#5b9bd5">{moduleLabels[s.module] ?? s.module}</span
						></td
					>
					<td style="font-family:monospace;font-size:12px">{s.hostname}:{s.port}</td>
					<td>{groupNameOf(s.group_id)}</td>
					<td class="c" style="font-variant-numeric:tabular-nums"
						>{s.accounts_count ?? '—'} / {s.max_accounts > 0 ? s.max_accounts : '∞'}</td
					>
					<td class="c">
						{#if s.active}
							<i class="fas fa-check-circle" style="color:#5cb85c;font-size:16px" title="Active"
							></i>
						{:else}
							<span class="hp-badge inactive">Off</span>
						{/if}
					</td>
					<td class="c">
						<form method="POST" action="?/test" use:enhance={testHandler} style="display:inline">
							<input type="hidden" name="id" value={s.id} />
							<button
								type="submit"
								class="hp-btn"
								style="padding:5px 10px"
								disabled={testingId === s.id}
								data-testid={`server-test-${s.id}`}
							>
								{testingId === s.id ? 'Testing…' : 'Test'}
							</button>
						</form>
					</td>
				</tr>
			{:else}
				<tr
					><td colspan="8" style="text-align:center;padding:28px;color:#999"
						>No servers configured</td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

<HpPager page={serverPage} perPage={serverPerPage} total={serverTotal} testid="servers-pager" />

<h2 class="hp-h2">Server Groups</h2>
<p class="hp-lead">
	Server groups let you assign products to a set of servers and rotate new orders around them.
</p>

{#if data.groupsError}
	<div class="hp-alert-red" data-testid="server-groups-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.groupsError}
	</div>
{/if}

<div class="hp-count">{groupTotal} Server Groups Found, Showing {groupFrom} to {groupTo}</div>

<div class="hp-scroll">
	<table class="hp-table">
		<thead>
			<tr>
				<th>Group Name</th>
				<th>Fill Type</th>
				<th>Servers</th>
				<th class="c" style="width:150px"></th>
			</tr>
		</thead>
		<tbody>
			{#each pagedGroups as g (g.id)}
				<tr class="hp-row">
					<td
						><span style="font-weight:600;color:#333" data-testid={`row-server-group-${g.id}`}
							>{g.name}</span
						></td
					>
					<td style="color:#555">{g.strategy === 'least_used' ? 'Least Used' : 'Round Robin'}</td>
					<td style="color:#666;font-size:12px">{membersOf(g.id)}</td>
					<td class="c" style="white-space:nowrap">
						<button
							type="button"
							class="hp-btn"
							style="padding:5px 10px"
							onclick={() => openEditGroup(g)}
							data-testid={`server-group-edit-${g.id}`}>Edit</button
						>
						<button
							type="button"
							class="hp-btn hp-btn-danger"
							style="padding:5px 10px"
							onclick={() => askDeleteGroup(g.id)}
							data-testid={`server-group-delete-${g.id}`}>Delete</button
						>
					</td>
				</tr>
			{:else}
				<tr
					><td colspan="4" style="text-align:center;padding:28px;color:#999">No server groups</td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

<HpPager
	page={groupPage}
	perPage={groupPerPage}
	total={groupTotal}
	pageParam="gpage"
	perPageParam="gper_page"
	testid="server-groups-pager"
/>

<!-- Group create/edit modal -->
<Modal
	bind:open={groupModalOpen}
	title={groupId ? 'Edit Server Group' : 'Create Server Group'}
	size="sm"
>
	<form
		method="POST"
		action="?/saveGroup"
		use:enhance={simpleHandler(
			'Group saved',
			(v) => (groupSaving = v),
			() => (groupModalOpen = false)
		)}
	>
		<input type="hidden" name="id" value={groupId || ''} />
		<FormField label="Group Name" name="name" bind:value={groupName} required />
		<FormField
			label="Fill Strategy"
			name="strategy"
			type="select"
			bind:value={groupStrategy}
			options={strategyOptions}
			required
		/>
		<div class="flex justify-end gap-2">
			<LoadingButton variant="secondary" onclick={() => (groupModalOpen = false)}
				>Cancel</LoadingButton
			>
			<span class="contents" data-testid="server-group-save">
				<LoadingButton type="submit" loading={groupSaving}>Save</LoadingButton>
			</span>
		</div>
	</form>
</Modal>

<!-- Group delete confirm -->
<form
	method="POST"
	action="?/deleteGroup"
	class="hidden"
	bind:this={deleteGroupForm}
	use:enhance={simpleHandler(
		'Group deleted',
		(v) => (deleteGroupLoading = v),
		() => (deleteGroupOpen = false)
	)}
>
	<input type="hidden" name="id" value={deleteGroupId} />
</form>

<ConfirmDialog
	bind:open={deleteGroupOpen}
	title="Delete Server Group"
	message="Are you sure you want to delete this server group?"
	danger
	loading={deleteGroupLoading}
	onConfirm={() => deleteGroupForm?.requestSubmit()}
/>

<style>
	.hidden {
		display: none;
	}
</style>
