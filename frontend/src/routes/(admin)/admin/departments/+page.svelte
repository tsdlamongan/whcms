<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import { invalidateAll } from '$app/navigation';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import FormField from '$lib/components/FormField.svelte';
	import LoadingButton from '$lib/components/LoadingButton.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import { toast } from '$lib/stores/toast.svelte';
	import type { PageProps } from './$types';
	import type { Department } from './+page.server';

	let { data, form }: PageProps = $props();

	const ERROR_TEXT: Record<string, string> = {
		'adminsupport.common.requiredField': 'This field is required.'
	};
	function errText(key?: string): string | undefined {
		if (!key) return undefined;
		return ERROR_TEXT[key] ?? key;
	}

	let modalOpen = $state(false);
	let editing = $state<Department | null>(null);
	let saving = $state(false);

	let name = $state('');
	let email = $state('');
	let sort = $state(0);
	let active = $state(true);

	let deleteOpen = $state(false);
	let deleteTarget = $state<Department | null>(null);
	let deleting = $state(false);

	const errorText = $derived(form?.errorMessage ?? null);
	const fieldErrors = $derived<Record<string, string>>(form?.fieldErrors ?? {});

	function openCreate() {
		editing = null;
		name = '';
		email = '';
		sort = 0;
		active = true;
		modalOpen = true;
	}

	function openEdit(dept: Department) {
		editing = dept;
		name = dept.name;
		email = dept.email;
		sort = dept.sort;
		active = dept.active;
		modalOpen = true;
	}

	function askDelete(dept: Department) {
		deleteTarget = dept;
		deleteOpen = true;
	}
</script>

<svelte:head>
	<title>Support Departments — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Support Departments</h1>

<p class="hp-lead">Manage the departments that support tickets can be assigned to.</p>

{#if data.listError}
	<div class="hp-alert-yellow" data-testid="department-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

{#if form?.deleteErrorMessage}
	<div class="hp-alert-red">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{form.deleteErrorMessage}
	</div>
{/if}

<div class="hp-listbar">
	<div class="hp-count">
		{data.departments.length} department{data.departments.length === 1 ? '' : 's'}
	</div>
	<button
		type="button"
		class="hp-btn hp-btn-primary"
		data-testid="department-create-button"
		onclick={openCreate}
	>
		<i class="fas fa-plus"></i>Add New Department
	</button>
</div>

<div class="hp-scroll">
	<table class="hp-table">
		<thead>
			<tr>
				<th style="width:60px">ID</th>
				<th>Name</th>
				<th>Email</th>
				<th class="c" style="width:80px">Sort</th>
				<th class="c" style="width:90px">Active</th>
				<th class="c" style="width:140px"></th>
			</tr>
		</thead>
		<tbody>
			{#each data.departments as dept (dept.id)}
				<tr class="hp-row">
					<td>{dept.id}</td>
					<td>
						<button
							type="button"
							class="hp-rowbtn"
							data-testid={`row-department-${dept.id}`}
							onclick={() => openEdit(dept)}
						>
							{dept.name}
						</button>
					</td>
					<td style="color:#555">{dept.email}</td>
					<td class="c" style="color:#666">{dept.sort}</td>
					<td class="c"
						><span class={`hp-badge ${dept.active ? 'active' : 'inactive'}`}
							>{dept.active ? 'Active' : 'Inactive'}</span
						></td
					>
					<td class="c" style="white-space:nowrap">
						<button
							type="button"
							class="hp-btn"
							style="padding:5px 10px"
							data-testid={`department-edit-${dept.id}`}
							onclick={() => openEdit(dept)}
						>
							Edit
						</button>
						<button
							type="button"
							class="hp-btn hp-btn-danger"
							style="padding:5px 10px"
							data-testid={`department-delete-${dept.id}`}
							onclick={() => askDelete(dept)}
						>
							Delete
						</button>
					</td>
				</tr>
			{:else}
				<tr
					><td colspan="6" style="text-align:center;padding:28px;color:#999"
						>No departments configured</td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

<Modal bind:open={modalOpen} title={editing ? 'Edit Department' : 'Add New Department'}>
	<form
		method="POST"
		action={editing ? '?/update' : '?/create'}
		data-testid="department-form"
		use:enhance={() => {
			saving = true;
			return async ({ result, update }) => {
				saving = false;
				if (result.type === 'success') {
					toast.success(
						editing ? 'Department updated successfully.' : 'Department created successfully.'
					);
					modalOpen = false;
					await invalidateAll();
				}
				await update({ reset: false });
			};
		}}
	>
		{#if editing}
			<input type="hidden" name="id" value={editing.id} />
		{/if}

		{#if errorText}
			<div class="hp-alert-red" style="margin-bottom:14px">{errorText}</div>
		{/if}

		<FormField
			label="Department name"
			name="name"
			bind:value={name}
			error={errText(fieldErrors.name)}
			required
		/>
		<FormField
			label="Email address"
			name="email"
			type="email"
			bind:value={email}
			error={errText(fieldErrors.email)}
			required
		/>
		<FormField label="Display order" name="sort" type="number" bind:value={sort} />
		<FormField label="Active" name="active" type="checkbox" bind:value={active} />

		<div class="mt-5 flex justify-end gap-2">
			<LoadingButton variant="secondary" onclick={() => (modalOpen = false)} disabled={saving}>
				Cancel
			</LoadingButton>
			<button
				type="submit"
				class="hp-btn hp-btn-primary"
				disabled={saving}
				aria-busy={saving}
				data-testid="department-save-button"
			>
				Save
			</button>
		</div>
	</form>
</Modal>

<form
	method="POST"
	action="?/delete"
	id="department-delete-form"
	use:enhance={() => {
		deleting = true;
		return async ({ result, update }) => {
			deleting = false;
			deleteOpen = false;
			if (result.type === 'success') {
				toast.success('Deleted successfully.');
				await invalidateAll();
			}
			await update({ reset: false });
		};
	}}
>
	<input type="hidden" name="id" value={deleteTarget?.id ?? ''} />
</form>
<ConfirmDialog
	bind:open={deleteOpen}
	danger
	loading={deleting}
	title="Delete this department?"
	message="Existing tickets in this department are not deleted."
	confirmLabel="Delete"
	onConfirm={() => {
		(document.getElementById('department-delete-form') as HTMLFormElement | null)?.requestSubmit();
	}}
/>

<style>
	.hp-rowbtn {
		background: none;
		border: none;
		padding: 0;
		font: inherit;
		font-weight: 600;
		color: var(--hp-link, #337ab7);
		cursor: pointer;
	}
	.hp-rowbtn:hover {
		text-decoration: underline;
	}
</style>
