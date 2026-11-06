<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import HpPager from '$lib/components/hp/HpPager.svelte';
	import { t } from '$lib/i18n';
	import { toast } from '$lib/stores/toast.svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	type Row = (typeof data.articles)[number];

	const categoryName = $derived(new Map(data.categories.map((c) => [c.id, c.name] as const)));

	const total = $derived(data.meta?.total ?? data.articles.length);
	const curPage = $derived(data.meta?.page ?? data.page);
	const perPage = $derived(data.meta?.per_page ?? data.perPage);
	const from = $derived(total === 0 ? 0 : (curPage - 1) * perPage + 1);
	const to = $derived(Math.min(curPage * perPage, total));

	// delete confirm
	let confirmOpen = $state(false);
	let deleting = $state(false);
	let deleteTarget = $state<Row | null>(null);
	let deleteFormEl = $state<HTMLFormElement | null>(null);

	function askDelete(row: Row) {
		deleteTarget = row;
		confirmOpen = true;
	}

	const deleteError = $derived(
		form?.op === 'delete' && !form?.success
			? form?.errorKey
				? t(form.errorKey)
				: (form?.errorMessage ?? null)
			: null
	);
</script>

<svelte:head>
	<title>Knowledgebase Articles — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Knowledgebase Articles</h1>

<p class="hp-lead">Manage the help articles shown in the client portal knowledgebase.</p>

{#if data.listError}
	<div class="hp-alert-yellow" data-testid="kb-article-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

{#if deleteError}
	<div class="hp-alert-red" data-testid="kb-article-delete-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{deleteError}
	</div>
{/if}

<div style="display:flex;gap:8px;margin-bottom:16px;flex-wrap:wrap">
	<a class="hp-btn" href="/admin/knowledgebase"
		><i class="fas fa-folder" style="color:#5b9bd5"></i>Manage Categories</a
	>
</div>

<form method="get" class="hp-filter" data-testid="kb-article-filter-form">
	<div class="grow">
		<div class="hp-field-label">Search</div>
		<input
			class="hp-input"
			type="search"
			name="search"
			value={data.search}
			placeholder="Search article title…"
			data-testid="kb-article-search-input"
		/>
	</div>
	<div style="flex:0 0 200px">
		<div class="hp-field-label">Category</div>
		<select class="hp-select" name="category_id" data-testid="kb-article-category-filter">
			<option value="" selected={data.categoryId === 0}>Any Category</option>
			{#each data.categories as c (c.id)}
				<option value={c.id} selected={data.categoryId === c.id}>{c.name}</option>
			{/each}
		</select>
	</div>
	<div style="flex:0 0 170px">
		<div class="hp-field-label">Status</div>
		<select class="hp-select" name="status" data-testid="kb-article-status-filter">
			<option value="" selected={data.status === ''}>Any Status</option>
			<option value="published" selected={data.status === 'published'}>Published</option>
			<option value="draft" selected={data.status === 'draft'}>Draft</option>
		</select>
	</div>
	<button class="hp-btn hp-btn-primary" type="submit" data-testid="kb-article-filter-submit">
		<i class="fas fa-search"></i>Search
	</button>
</form>

<div class="hp-listbar">
	<div class="hp-count">{total} Records Found, Showing {from} to {to}</div>
	<a
		class="hp-btn hp-btn-primary"
		href="/admin/knowledgebase/articles/new"
		data-testid="kb-article-create-link"
	>
		<i class="fas fa-plus"></i>New Article
	</a>
</div>

<div class="hp-scroll">
	<table class="hp-table">
		<thead>
			<tr>
				<th style="width:60px">ID</th>
				<th>Title</th>
				<th>Category</th>
				<th class="c" style="width:90px">Views</th>
				<th class="c" style="width:100px">Status</th>
				<th style="width:160px"></th>
			</tr>
		</thead>
		<tbody>
			{#each data.articles as row (row.id)}
				<tr class="hp-row">
					<td style="color:#666">{row.id}</td>
					<td>
						<a
							class="cell-link"
							href={`/admin/knowledgebase/articles/${row.id}`}
							data-testid={`row-kb-article-${row.id}`}
						>
							{row.title}
						</a>
					</td>
					<td style="color:#555">{categoryName.get(row.category_id) ?? `#${row.category_id}`}</td>
					<td class="c" style="color:#999">{row.views}</td>
					<td class="c">
						<span class={`hp-badge ${row.published ? 'active' : 'inactive'}`}>
							{row.published ? 'Published' : 'Draft'}
						</span>
					</td>
					<td style="white-space:nowrap">
						<a
							class="hp-btn"
							style="padding:5px 10px"
							href={`/admin/knowledgebase/articles/${row.id}`}
							data-testid={`kb-article-edit-${row.id}`}
						>
							Edit
						</a>
						<button
							type="button"
							class="hp-btn hp-btn-danger"
							style="padding:5px 10px"
							data-testid={`kb-article-delete-${row.id}`}
							onclick={() => askDelete(row)}
						>
							Delete
						</button>
					</td>
				</tr>
			{:else}
				<tr
					><td colspan="6" style="text-align:center;padding:28px;color:#999">No articles found</td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

<HpPager page={curPage} {perPage} {total} />

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
			if (result.type === 'success') toast.success(t('adminPortal.kb.articleDeleted'));
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
	message={`Delete article "${deleteTarget?.title ?? ''}"? This cannot be undone.`}
	confirmLabel="Delete"
	onConfirm={() => deleteFormEl?.requestSubmit()}
	onCancel={() => (deleteTarget = null)}
/>

<style>
	.cell-link {
		font-weight: 600;
		color: var(--hp-link, #337ab7);
	}
	.cell-link:hover {
		text-decoration: underline;
	}
	.hidden {
		display: none;
	}
</style>
