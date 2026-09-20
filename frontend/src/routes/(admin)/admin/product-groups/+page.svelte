<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import FormField from '$lib/components/FormField.svelte';
	import LoadingButton from '$lib/components/LoadingButton.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import { fmtDate } from '$lib/date';
	import { slugify } from '$lib/slug';
	import { toast } from '$lib/stores/toast.svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	type GroupRow = (typeof data.groups)[number];

	const ERROR_TEXT: Record<string, string> = {
		'adcatalog.groups.fillRequired': 'Name and slug are required.',
		'adcatalog.groups.deleteFailed': 'Failed to delete the product group.'
	};
	function errText(key?: string): string | undefined {
		if (!key) return undefined;
		return ERROR_TEXT[key] ?? key;
	}

	// create / edit modal state
	let modalOpen = $state(false);
	let editing = $state<GroupRow | null>(null);
	let saving = $state(false);
	let fName = $state('');
	let fSlug = $state('');
	let fSort = $state(0);
	let fHidden = $state(false);
	let slugTouched = $state(false);

	$effect(() => {
		if (!slugTouched && !editing) fSlug = slugify(fName);
	});

	function openCreate() {
		editing = null;
		fName = '';
		fSlug = '';
		fSort = 0;
		fHidden = false;
		slugTouched = false;
		modalOpen = true;
	}

	function openEdit(row: GroupRow) {
		editing = row;
		fName = row.name;
		fSlug = row.slug;
		fSort = row.sort;
		fHidden = row.hidden;
		slugTouched = true;
		modalOpen = true;
	}

	// delete confirm state
	let confirmOpen = $state(false);
	let deleting = $state(false);
	let deleteTarget = $state<GroupRow | null>(null);
	let deleteForm = $state<HTMLFormElement | null>(null);

	function askDelete(row: GroupRow) {
		deleteTarget = row;
		confirmOpen = true;
	}

	const modalError = $derived(
		modalOpen && form?.op === 'save' && !form?.success
			? form?.errorKey
				? errText(form.errorKey)
				: (form?.errorMessage ?? null)
			: null
	);
</script>

<svelte:head>
	<title>Product Groups — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Product Groups</h1>

<p class="hp-lead">Group products in the order catalog. Each product must belong to a group.</p>

{#if data.listError}
	<div class="hp-alert-yellow" data-testid="group-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

{#if form?.op === 'delete' && !form?.success && (form?.errorMessage || form?.errorKey)}
	<div class="hp-alert-red" data-testid="group-delete-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{form.errorKey
			? errText(form.errorKey)
			: form.errorMessage}
	</div>
{/if}

<div class="hp-listbar">
	<div class="hp-count">{data.groups.length} group{data.groups.length === 1 ? '' : 's'}</div>
	<span data-testid="group-create-button">
		<button type="button" class="hp-btn hp-btn-primary" onclick={openCreate}>
			<i class="fas fa-plus"></i>New Group
		</button>
	</span>
</div>

<div class="hp-scroll">
	<table class="hp-table">
		<thead>
			<tr>
				<th style="width:60px">ID</th>
				<th>Name</th>
				<th>Slug</th>
				<th class="c" style="width:80px">Sort</th>
				<th class="c" style="width:100px">Visibility</th>
				<th style="width:110px">Created</th>
				<th style="width:140px"></th>
			</tr>
		</thead>
		<tbody>
			{#each data.groups as row (row.id)}
				<tr class="hp-row">
					<td>{row.id}</td>
					<td>
						<button
							type="button"
							class="hp-rowbtn"
							data-testid={`row-group-${row.id}`}
							onclick={() => openEdit(row)}
						>
							{row.name}
						</button>
					</td>
					<td
						><code
							style="background:#f5f5f5;border-radius:3px;padding:1px 6px;font-size:12px;color:#555"
							>{row.slug}</code
						></td
					>
					<td class="c" style="color:#666">{row.sort}</td>
					<td class="c">
						<span class={`hp-badge ${row.hidden ? 'inactive' : 'active'}`}
							>{row.hidden ? 'Hidden' : 'Visible'}</span
						>
					</td>
					<td style="color:#666">{fmtDate(row.created_at)}</td>
					<td style="white-space:nowrap">
						<button
							type="button"
							class="hp-btn"
							style="padding:5px 10px"
							data-testid={`group-edit-${row.id}`}
							onclick={() => openEdit(row)}
						>
							Edit
						</button>
						<button
							type="button"
							class="hp-btn hp-btn-danger"
							style="padding:5px 10px"
							data-testid={`group-delete-${row.id}`}
							onclick={() => askDelete(row)}
						>
							Delete
						</button>
					</td>
				</tr>
			{:else}
				<tr
					><td colspan="7" style="text-align:center;padding:28px;color:#999"
						>No product groups yet</td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

<!-- create / edit modal -->
<Modal bind:open={modalOpen} title={editing ? 'Edit Product Group' : 'Create Product Group'}>
	<form
		method="POST"
		action={editing ? '?/update' : '?/create'}
		data-testid="group-form"
		use:enhance={() => {
			saving = true;
			return async ({ result, update }) => {
				saving = false;
				if (result.type === 'success') {
					modalOpen = false;
					toast.success('Product group saved successfully.');
				}
				await update();
			};
		}}
	>
		{#if editing}
			<input type="hidden" name="id" value={editing.id} />
		{/if}

		{#if modalError}
			<div class="hp-alert-red" style="margin-bottom:14px" data-testid="group-form-error">
				{modalError}
			</div>
		{/if}

		<div data-testid="group-name-field">
			<FormField label="Name" name="name" bind:value={fName} required />
		</div>
		<div data-testid="group-slug-field" oninputcapture={() => (slugTouched = true)}>
			<FormField
				label="Slug"
				name="slug"
				bind:value={fSlug}
				hint="Lowercase letters, numbers and dashes. Used in the order URL."
				required
			/>
		</div>
		<FormField label="Sort" name="sort" type="number" bind:value={fSort} />
		<FormField label="Hide from catalog" name="hidden" type="checkbox" bind:value={fHidden} />

		<div class="mt-5 flex justify-end gap-2">
			<LoadingButton variant="secondary" onclick={() => (modalOpen = false)} disabled={saving}>
				Cancel
			</LoadingButton>
			<span data-testid="group-form-submit">
				<LoadingButton type="submit" loading={saving}>Save</LoadingButton>
			</span>
		</div>
	</form>
</Modal>

<!-- delete confirm -->
<form
	method="POST"
	action="?/delete"
	class="hidden"
	bind:this={deleteForm}
	use:enhance={() => {
		deleting = true;
		return async ({ result, update }) => {
			deleting = false;
			confirmOpen = false;
			if (result.type === 'success') toast.success('Product group deleted successfully.');
			await update();
		};
	}}
>
	<input type="hidden" name="id" value={deleteTarget?.id ?? ''} />
</form>

<ConfirmDialog
	bind:open={confirmOpen}
	danger
	loading={deleting}
	title="Delete"
	message={`Delete group "${deleteTarget?.name ?? ''}"? Its products will not be deleted.`}
	confirmLabel="Delete"
	onConfirm={() => deleteForm?.requestSubmit()}
	onCancel={() => (deleteTarget = null)}
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
	.hidden {
		display: none;
	}
</style>
