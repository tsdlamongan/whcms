<script lang="ts">
	import { navigating } from '$app/state';
	import { t } from '$lib/i18n';

	/**
	 * Twenty-One domain search hero (DESIGN §7a / clientarea.web.id). Full-width
	 * white band; a bordered box with a multi-line search textarea, Search (submits
	 * GET /order/domain?q=…) + Transfer buttons bottom-right, and the TLD / length /
	 * safe-search controls below. "View all pricing" sits right-aligned under the box.
	 * The controls are presentational (unnamed -> never post); real availability logic
	 * lives on /order/domain.
	 */
	interface Props {
		searchAction?: string;
		placeholder?: string;
		/** Optional data-testid applied to the search form. */
		testid?: string;
	}

	let { searchAction = '/order/domain', placeholder, testid }: Props = $props();

	// GET form -> router navigation; the destination page's load() queries the
	// registrar, which can take seconds. Show it and refuse repeat submits.
	const searching = $derived(
		navigating.to?.url.pathname === searchAction && navigating.to.url.searchParams.has('q')
	);
</script>

<section class="ca-heroband">
	<div class="ca-hero-inner">
		<h2 class="ca-h2 ca-hero-title">{t('portal.hero.title')}</h2>
		<form
			class="ca-hero-box"
			method="GET"
			action={searchAction}
			data-testid={testid}
			aria-busy={searching}
			onsubmit={(e) => {
				if (searching) e.preventDefault();
			}}
		>
			<textarea
				class="ca-hero-input"
				name="q"
				rows="3"
				placeholder={placeholder ?? t('portal.hero.placeholder')}
				aria-label={t('portal.hero.title')}></textarea>
			<div class="ca-hero-actions">
				<button
					type="submit"
					class="ca-btn ca-btn-primary"
					disabled={searching}
					aria-busy={searching}
				>
					{t('portal.hero.search')}
					{#if searching}
						<span class="ca-spinner" aria-hidden="true"></span>
					{:else}
						<i class="fas fa-wand-magic-sparkles" aria-hidden="true"></i>
					{/if}
				</button>
				<a class="ca-btn ca-btn-success" href={searchAction}>{t('portal.hero.transfer')}</a>
			</div>
			<div class="ca-hero-controls">
				<label class="ca-hero-control">
					<span class="ca-muted">{t('portal.hero.includeTlds')}</span>
					<select class="ca-select" aria-label={t('portal.hero.includeTlds')}>
						<option>.com</option>
						<option>.net</option>
						<option>.id</option>
						<option>.co.id</option>
					</select>
				</label>
				<label class="ca-hero-control">
					<span class="ca-muted">{t('portal.hero.maxLength')}</span>
					<select class="ca-select" aria-label={t('portal.hero.maxLength')}>
						<option>-</option>
						<option>10</option>
						<option>15</option>
						<option>20</option>
					</select>
				</label>
				<label class="ca-hero-control ca-hero-check">
					<input type="checkbox" checked />
					<span class="ca-muted">{t('portal.hero.safeSearch')}</span>
				</label>
			</div>
		</form>
		<div class="ca-hero-pricing-row">
			<a class="ca-hero-pricing-link" href="/order/domain">{t('portal.hero.viewAllPricing')}</a>
		</div>
	</div>
</section>
