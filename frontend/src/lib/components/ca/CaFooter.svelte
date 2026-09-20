<script lang="ts">
	import { appName } from '$lib/appName';
	import { i18n, LOCALES, t } from '$lib/i18n';
	import CaLanguageModal from './CaLanguageModal.svelte';

	/**
	 * Twenty-One footer (DESIGN §11): dark bar with a language/currency chooser
	 * (opens CaLanguageModal), a Contact Us link, a dynamic-year copyright and a
	 * "Powered by" strip. The inline locale buttons keep the existing
	 * `data-testid="<prefix>-<locale>"` switcher (default prefix "locale-switch";
	 * the /order layout passes "locale" to keep its legacy `locale-<locale>` ids).
	 */
	interface Props {
		/** Prefix for the locale-button testids. */
		localeTestidPrefix?: string;
	}

	let { localeTestidPrefix = 'locale-switch' }: Props = $props();

	const year = new Date().getFullYear();

	let langOpen = $state(false);

	const LOCALE_LABELS: Record<string, string> = { id: 'Bahasa Indonesia', en: 'English' };
	const currentLabel = $derived(LOCALE_LABELS[i18n.locale] ?? i18n.locale.toUpperCase());
</script>

<footer class="ca-footer">
	<div class="ca-container">
		<div class="ca-foot-lang">
			<button type="button" class="ca-foot-langbtn" onclick={() => (langOpen = true)}>
				<i class="fas fa-globe" aria-hidden="true"></i>
				{currentLabel} / Rp IDR
			</button>
			<span class="ca-foot-locale">
				{#each LOCALES as locale (locale)}
					<button
						type="button"
						class:active={i18n.locale === locale}
						data-testid={`${localeTestidPrefix}-${locale}`}
						onclick={() => i18n.setLocale(locale)}
					>
						{locale}
					</button>
				{/each}
			</span>
		</div>

		<ul class="ca-foot-links">
			<li><a href="/contact">{t('portal.nav.contactUs')}</a></li>
			<li><a href="/knowledgebase">{t('portal.nav.knowledgebase')}</a></li>
			<li><a href="/announcements">{t('portal.nav.announcements')}</a></li>
		</ul>

		<p class="ca-copyright">{t('footer.copyright', { year, name: appName })}</p>
	</div>
</footer>

<CaLanguageModal open={langOpen} onClose={() => (langOpen = false)} />
