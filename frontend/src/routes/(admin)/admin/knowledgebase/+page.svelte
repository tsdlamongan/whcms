<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import FormField from '$lib/components/FormField.svelte';
	import LoadingButton from '$lib/components/LoadingButton.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import { t } from '$lib/i18n';
	import { slugify } from '$lib/slug';
	import { toast } from '$lib/stores/toast.svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	type Row = (typeof data.categories)[number];

	// create / edit modal
	let modalOpen = $state(false);
	let editing = $state<Row | null>(null);
	let saving = $state(false);
	let fName = $state('');
	let fSlug = $state('');
	let fDescription = $state('');
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
		fDescription = '';
		fSort = 0;
		fHidden = false;
		slugTouched = false;
		modalOpen = true;
	}

	function openEdit(row: Row) {
		editing = row;
		fName = row.name;
		fSlug = row.slug;
		fDescription = row.description;
		fSort = row.sort;
		fHidden = row.hidden;
		slugTouched = true;
		modalOpen = true;
	}

	// delete confirm
	let confirmOpen = $state(false);
	let deleting = $state(false);
	let deleteTarget = $state<Row | null>(null);
	let deleteFormEl = $state<HTMLFormElement | null>(null);

	function askDelete(row: Row) {
		deleteTarget = row;
		confirmOpen = true;
	}

	const modalError = $derived(
		modalOpen && form?.op === 'save' && !form?.success
			? form?.errorKey
				? t(form.errorKey)
				: (form?.errorMessage ?? null)
			: null
	);

	const deleteError = $derived(
		form?.op === 'delete' && !form?.success
			? form?.errorKey
				? t(form.errorKey)
				: (form?.errorMessage ?? null)
			: null
	);
</script>

<svelte:head>
	<title>Knowledgebase Categories — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Knowledgebase Categories</h1>

<p class="hp-lead">
	Group the help articles shown in the client portal. Each article belongs to a category.
</p>

{#if data.listError}
	<div class="hp-alert-yellow" data-testid="kb-category-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

{#if deleteError}
	<div class="hp-alert-red" data-testid="kb-category-delete-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{deleteError}
	</div>
{/if}

<div style="display:flex;gap:8px;margin-bottom:16px;flex-wrap:wrap">
	<a class="hp-btn" href="/admin/knowledgebase/articles"
		><i class="fas fa-book" style="color:#5b9bd5"></i>Manage Articles</a
	>
</div>

<div class="hp-listbar">
	<div class="hp-count">
		{data.categories.length} categor{data.categories.length === 1 ? 'y' : 'ies'}
	</div>
	<span data-testid="kb-category-create-button">
		<button type="button" class="hp-btn hp-btn-primary" onclick={openCreate}>
			<i class="fas fa-plus"></i>New Category
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
				<th>Description</th>
				<th class="c" style="width:80px">Sort</th>
				<th class="c" style="width:100px">Visibility</th>
				<th style="width:140px"></th>
			</tr>
		</thead>
		<tbody>
			{#each data.categories as row (row.id)}
				<tr class="hp-row">
					<td style="color:#666">{row.id}</td>
					<td>
						<button
							type="button"
							class="hp-rowbtn"
							data-testid={`row-kb-category-${row.id}`}
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
					<td
						style="color:#777;max-width:320px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap"
						>{row.description || '—'}</td
					>
					<td class="c" style="color:#666">{row.sort}</td>
					<td class="c">
						<span class={`hp-badge ${row.hidden ? 'inactive' : 'active'}`}
							>{row.hidden ? 'Hidden' : 'Visible'}</span
						>
					</td>
					<td style="white-space:nowrap">
						<button
							type="button"
							class="hp-btn"
							style="padding:5px 10px"
							data-testid={`kb-category-edit-${row.id}`}
							onclick={() => openEdit(row)}
						>
							Edit
						</button>
						<button
							type="button"
							class="hp-btn hp-btn-danger"
							style="padding:5px 10px"
							data-testid={`kb-category-delete-${row.id}`}
							onclick={() => askDelete(row)}
						>
							Delete
						</button>
					</td>
				</tr>
			{:else}
				<tr
					><td colspan="7" style="text-align:center;padding:28px;color:#999">No categories yet</td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

<!-- create / edit modal -->
<Modal bind:open={modalOpen} title={editing ? 'Edit Category' : 'Create Category'}>
	<form
		method="POST"
		action={editing ? '?/update' : '?/create'}
		data-testid="kb-category-form"
		use:enhance={() => {
			saving = true;
			return async ({ result, update }) => {
				saving = false;
				if (result.type === 'success') {
					modalOpen = false;
					toast.success(t('adminPortal.kb.categorySaved'));
				}
				await update();
			};
		}}
	>
		{#if editing}
			<input type="hidden" name="id" value={editing.id} />
		{/if}

		{#if modalError}
			<div class="hp-alert-red" style="margin-bottom:14px" data-testid="kb-category-form-error">
				{modalError}
			</div>
		{/if}

		<div data-testid="kb-category-name-field">
			<FormField label="Name" name="name" bind:value={fName} required />
		</div>
		<div data-testid="kb-category-slug-field" oninputcapture={() => (slugTouched = true)}>
			<FormField
				label="Slug"
				name="slug"
				bind:value={fSlug}
				hint="Lowercase letters, numbers and dashes. Leave blank to derive from the name."
			/>
		</div>
		<div data-testid="kb-category-description-field">
			<FormField label="Description" name="description" type="textarea" bind:value={fDescription} />
		</div>
		<FormField label="Sort" name="sort" type="number" bind:value={fSort} />
		<FormField label="Hide from portal" name="hidden" type="checkbox" bind:value={fHidden} />

		<div class="mt-5 flex justify-end gap-2">
			<LoadingButton variant="secondary" onclick={() => (modalOpen = false)} disabled={saving}>
				Cancel
			</LoadingButton>
			<span data-testid="kb-category-form-submit">
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
	bind:this={deleteFormEl}
	use:enhance={() => {
		deleting = true;
		return async ({ result, update }) => {
			deleting = false;
			confirmOpen = false;
			if (result.type === 'success') toast.success(t('adminPortal.kb.categoryDeleted'));
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
	message={`Delete category "${deleteTarget?.name ?? ''}"? Categories with articles cannot be deleted.`}
	confirmLabel="Delete"
	onConfirm={() => deleteFormEl?.requestSubmit()}
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
