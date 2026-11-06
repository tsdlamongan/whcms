<script lang="ts">
	import { appName } from '$lib/appName';
	import { Alert, EmptyState } from '$lib/components';
	import { t } from '$lib/i18n';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();
	const art = $derived(data.article);
</script>

<svelte:head>
	<title>{art ? art.title : t('portal.kb.notFoundArticle')} — {appName}</title>
</svelte:head>

{#if data.loadError}
	<Alert type="error">{data.loadError}</Alert>
{/if}

{#if !art}
	<section class="ca-card">
		<div class="ca-card-body">
			<EmptyState title={t('portal.kb.notFoundArticle')}>
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
	<article data-testid="kb-article">
		<h1 class="ca-h1">{art.title}</h1>
		<p class="ca-muted" style="font-size:0.85rem;margin:-8px 0 18px;">
			<i class="fas fa-eye" aria-hidden="true"></i>
			{art.views}
			{t('portal.kb.views')}
		</p>
		<section class="ca-card">
			<div class="ca-card-body ca-prose">
				<!-- eslint-disable-next-line svelte/no-at-html-tags -->
				{@html art.body}
			</div>
		</section>
		<a class="ca-btn ca-btn-outline-primary" href="/knowledgebase">
			<i class="fas fa-arrow-left" aria-hidden="true"></i>
			{t('portal.kb.back')}
		</a>
	</article>
{/if}

<style>
	.ca-prose :global(p) {
		margin: 0 0 1em;
	}
	.ca-prose :global(a) {
		color: var(--ca-primary);
		text-decoration: underline;
	}
	.ca-prose :global(h2),
	.ca-prose :global(h3) {
		margin: 1.2em 0 0.5em;
	}
	.ca-prose :global(ul),
	.ca-prose :global(ol) {
		margin: 0 0 1em;
		padding-left: 1.5em;
	}
	.ca-prose :global(img) {
		max-width: 100%;
		height: auto;
	}
</style>
