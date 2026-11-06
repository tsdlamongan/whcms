<script lang="ts">
	import { appName } from '$lib/appName';
	import { t } from '$lib/i18n';
	import ArticleForm from '../ArticleForm.svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	const errorText = $derived(form?.errorKey ? t(form.errorKey) : (form?.errorMessage ?? null));
</script>

<svelte:head>
	<title>New Article — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Add New Article</h1>
<p class="hp-lead">Write a help article for the client portal knowledgebase.</p>

{#if data.categoriesError}
	<div class="hp-alert-red" data-testid="kb-article-categories-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.categoriesError}
	</div>
{/if}

<ArticleForm categories={data.categories} {errorText} submitLabel="Create Article" />
