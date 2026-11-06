<script lang="ts">
	import { appName } from '$lib/appName';
	import { Alert, EmptyState } from '$lib/components';
	import { t } from '$lib/i18n';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();
	const cat = $derived(data.category);
	const articles = $derived(
		(cat?.articles ?? []).filter((a) => a.published).sort((a, b) => a.sort - b.sort || a.id - b.id)
	);
</script>

<svelte:head>
	<title>{cat ? cat.name : t('portal.kb.notFoundCategory')} — {appName}</title>
</svelte:head>

{#if data.loadError}
	<Alert type="error">{data.loadError}</Alert>
{/if}

{#if !cat}
	<section class="ca-card">
		<div class="ca-card-body">
			<EmptyState title={t('portal.kb.notFoundCategory')}>
				{#snippet action()}
					<a class="ca-btn ca-btn-primary" href="/knowledgebase">
						<i class="fas fa-arrow-left" aria-hidden="true"></i>
						{t('portal.kb.back')}
					</a>
				{/snippet}
			</EmptyState>
		</div>
	</section>
{:else}
	<div data-testid="kb-category">
		<h1 class="ca-h1">{cat.name}</h1>
		{#if cat.description}
			<p class="ca-lead">{cat.description}</p>
		{/if}

		{#if articles.length === 0}
			<section class="ca-card">
				<div class="ca-card-body">
					<EmptyState title={t('portal.kb.emptyCategory')} />
				</div>
			</section>
		{:else}
			<div class="ca-card">
				<ul class="ca-list-group">
					{#each articles as art (art.id)}
						<li class="ca-list-item" style="border-top:1px solid #f0f0f0;">
							<i class="fas fa-file-alt" aria-hidden="true"></i>
							<a
								href={`/knowledgebase/article/${encodeURIComponent(art.slug)}`}
								data-testid={`kb-article-${art.slug}`}
							>
								{art.title}
							</a>
						</li>
					{/each}
				</ul>
			</div>
		{/if}

		<a class="ca-btn ca-btn-outline-primary" href="/knowledgebase">
			<i class="fas fa-arrow-left" aria-hidden="true"></i>
			{t('portal.kb.back')}
		</a>
	</div>
{/if}
