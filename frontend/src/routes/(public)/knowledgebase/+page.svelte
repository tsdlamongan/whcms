<script lang="ts">
	import { appName } from '$lib/appName';
	import { Alert, EmptyState } from '$lib/components';
	import CaTile from '$lib/components/ca/CaTile.svelte';
	import { t } from '$lib/i18n';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();

	const categories = $derived(
		data.categories.filter((c) => !c.hidden).sort((a, b) => a.sort - b.sort || a.id - b.id)
	);
</script>

<svelte:head>
	<title>{t('portal.kb.title')} — {appName}</title>
</svelte:head>

<div data-testid="kb-home">
	<h1 class="ca-h1">{t('portal.kb.title')}</h1>
	<p class="ca-lead">{t('portal.kb.subtitle')}</p>

	<!-- Search box (GET name="search" -> /knowledgebase) -->
	<form class="ca-hero-box" method="GET" action="/knowledgebase" style="margin-bottom:24px;">
		<div class="ca-input-group">
			<div class="ca-input-group-prepend">
				<button type="submit" class="ca-btn ca-btn-default" aria-label={t('portal.kb.search')}>
					<i class="fas fa-search" aria-hidden="true"></i>
				</button>
			</div>
			<input
				class="ca-input"
				type="text"
				name="search"
				value={data.term}
				placeholder={t('portal.kb.searchPlaceholder')}
				aria-label={t('portal.kb.searchPlaceholder')}
				data-testid="kb-search-input"
			/>
		</div>
	</form>

	{#if data.loadError}
		<Alert type="error">{data.loadError}</Alert>
	{/if}

	<!-- Search results (only when a term was submitted) -->
	{#if data.results !== null}
		<section data-testid="kb-results" style="margin-bottom:28px;">
			<h2 class="ca-h2">{t('portal.kb.resultsTitle', { term: data.term })}</h2>
			{#if data.resultsError}
				<Alert type="error">{data.resultsError}</Alert>
			{:else if data.results.length === 0}
				<section class="ca-card">
					<div class="ca-card-body">
						<EmptyState title={t('portal.kb.noResults')} />
					</div>
				</section>
			{:else}
				<div class="ca-card">
					<ul class="ca-list-group">
						{#each data.results as art (art.id)}
							<li class="ca-list-item" style="border-top:1px solid #f0f0f0;">
								<i class="fas fa-file-alt" aria-hidden="true"></i>
								<a href={`/knowledgebase/article/${encodeURIComponent(art.slug)}`}>{art.title}</a>
							</li>
						{/each}
					</ul>
				</div>
			{/if}
		</section>
	{/if}

	<!-- Category grid -->
	{#if categories.length === 0}
		{#if !data.loadError}
			<section class="ca-card">
				<div class="ca-card-body">
					<EmptyState title={t('portal.kb.noCategories')} />
				</div>
			</section>
		{/if}
	{:else}
		<div class="ca-tiles-5">
			{#each categories as cat (cat.id)}
				<CaTile
					icon="fas fa-folder-open"
					label={cat.name}
					href={`/knowledgebase/category/${encodeURIComponent(cat.slug)}`}
					border="teal"
					testid={`kb-category-${cat.slug}`}
				/>
			{/each}
		</div>
	{/if}
</div>
