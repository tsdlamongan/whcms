<script lang="ts">
	import { appName } from '$lib/appName';
	import { Alert, DateText, EmptyState } from '$lib/components';
	import { t } from '$lib/i18n';
	import type { PageProps } from './$types';

	let { data }: PageProps = $props();
	const a = $derived(data.announcement);
</script>

<svelte:head>
	<title>{a ? a.title : t('portal.announcements.notFound')} — {appName}</title>
</svelte:head>

{#if data.loadError}
	<Alert type="error">{data.loadError}</Alert>
{/if}

{#if !a}
	<section class="ca-card">
		<div class="ca-card-body">
			<EmptyState title={t('portal.announcements.notFound')}>
				{#snippet action()}
					<a class="ca-btn ca-btn-primary" href="/announcements">
						<i class="fas fa-arrow-left" aria-hidden="true"></i>
						{t('portal.announcements.back')}
					</a>
				{/snippet}
			</EmptyState>
		</div>
	</section>
{:else}
	<article data-testid="announcement-detail">
		<h1 class="ca-h1" data-testid="announcement-title">{a.title}</h1>
		<p class="ca-muted" style="font-size:0.85rem;margin:-8px 0 18px;">
			<i class="fas fa-calendar" aria-hidden="true"></i>
			<DateText value={a.published_at ?? a.created_at} />
		</p>
		<section class="ca-card">
			<div class="ca-card-body ca-prose">
				<!-- eslint-disable-next-line svelte/no-at-html-tags -->
				{@html a.body}
			</div>
		</section>
		<a class="ca-btn ca-btn-outline-primary" href="/announcements">
			<i class="fas fa-arrow-left" aria-hidden="true"></i>
			{t('portal.announcements.back')}
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
