<script lang="ts">
	import { appName } from '$lib/appName';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { Alert, DateText, EmptyState } from '$lib/components';
	import { t } from '$lib/i18n';
	import type { PageProps } from './$types';

	const PAGE_SIZE_OPTIONS = [10, 25, 50, 100, 500, 1000];

	let { data }: PageProps = $props();

	function changePerPage(e: Event) {
		const url = new URL(page.url);
		url.searchParams.set('per_page', (e.currentTarget as HTMLSelectElement).value);
		url.searchParams.set('page', '1');
		goto(`${url.pathname}${url.search}`);
	}

	/** Strip HTML tags and collapse whitespace for a plain-text excerpt. */
	function excerpt(html: string, max = 220): string {
		const text = html
			.replace(/<[^>]*>/g, ' ')
			.replace(/\s+/g, ' ')
			.trim();
		return text.length > max ? text.slice(0, max).trimEnd() + '…' : text;
	}

	const total = $derived(data.meta?.total ?? data.announcements.length);
	const hasPrev = $derived(data.page > 1);
	const hasNext = $derived(data.page * data.perPage < total);
</script>

<svelte:head>
	<title>{t('portal.announcements.title')} — {appName}</title>
</svelte:head>

<h1 class="ca-h1">{t('portal.announcements.title')}</h1>
<p class="ca-lead">{t('portal.announcements.subtitle')}</p>

{#if data.loadError}
	<Alert type="error">{data.loadError}</Alert>
{/if}

{#if data.announcements.length === 0}
	{#if !data.loadError}
		<section class="ca-card">
			<div class="ca-card-body">
				<EmptyState title={t('portal.announcements.empty')} />
			</div>
		</section>
	{/if}
{:else}
	<div data-testid="announcements-list">
		{#each data.announcements as a (a.id)}
			<article class="ca-card" data-testid={`announcement-row-${a.id}`}>
				<div class="ca-card-body">
					<h2 class="ca-h3" style="margin-bottom:6px;">
						<a href={`/announcements/${encodeURIComponent(a.slug)}`}>{a.title}</a>
					</h2>
					<p class="ca-muted" style="font-size:0.85rem;margin:0 0 10px;">
						<i class="fas fa-calendar" aria-hidden="true"></i>
						<DateText value={a.published_at ?? a.created_at} />
					</p>
					{#if a.body}
						<p style="margin:0 0 12px;">{excerpt(a.body)}</p>
					{/if}
					<a
						class="ca-btn ca-btn-outline-primary"
						href={`/announcements/${encodeURIComponent(a.slug)}`}
					>
						{t('portal.announcements.readMore')}
						<i class="fas fa-arrow-right" aria-hidden="true"></i>
					</a>
				</div>
			</article>
		{/each}
	</div>

	<nav class="ca-pager" aria-label="Pagination">
		<div class="ca-pager-nav">
			{#if hasPrev}
				<a href={`/announcements?page=${data.page - 1}`}>{t('portal.announcements.prev')}</a>
			{:else}
				<span class="disabled">{t('portal.announcements.prev')}</span>
			{/if}
			{#if hasNext}
				<a href={`/announcements?page=${data.page + 1}`}>{t('portal.announcements.next')}</a>
			{:else}
				<span class="disabled">{t('portal.announcements.next')}</span>
			{/if}
		</div>
		<label class="ca-pager-size">
			<select
				class="ca-select"
				value={data.perPage}
				onchange={changePerPage}
				data-testid="page-size-select"
			>
				{#each PAGE_SIZE_OPTIONS as opt (opt)}
					<option value={opt}>{opt}</option>
				{/each}
			</select>
			{t('table.perPage')}
		</label>
	</nav>
{/if}
