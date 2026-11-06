<script lang="ts">
	import { appName } from '$lib/appName';
	import { t } from '$lib/i18n';
	import { cart } from '$lib/stores/cart.svelte';

	/**
	 * Twenty-One header (DESIGN §4): brand + KB search (hidden < xl) + cart badge.
	 * The optional href/testid props let host layouts preserve legacy testids
	 * (`order-home-link` on /order, `nav-brand` in the client area) without
	 * forking the component.
	 */
	interface Props {
		user?: SessionUser | null;
		/** Where the brand links (default "/"). */
		brandHref?: string;
		/** data-testid for the brand link. */
		brandTestid?: string;
		/** data-testid for the cart link (default "cart-link"). */
		cartTestid?: string;
		/** data-testid for the cart count badge (default "cart-count"). */
		cartCountTestid?: string;
	}

	let {
		user,
		brandHref = '/',
		brandTestid,
		cartTestid = 'cart-link',
		cartCountTestid = 'cart-count'
	}: Props = $props();

	// Load the persisted cart after hydration so the badge count is accurate on
	// every page (SSR always renders an empty cart - idempotent + browser-guarded).
	$effect(() => {
		cart.init();
	});
</script>

<header class="ca-header">
	<div class="ca-container ca-navbar">
		<!-- Brand: /logo.png would be used here if present in static/; falls back to
		     the app-name text brand. -->
		<a class="ca-brand" href={brandHref} data-testid={brandTestid}>
			<span class="ca-brand-badge">{appName.slice(0, 1)}</span>
			<span data-testid="app-name">{appName}</span>
		</a>

		<!-- KB search (hidden below xl via .ca-search) -->
		<form class="ca-search" method="GET" action="/knowledgebase">
			<div class="ca-input-group">
				<div class="ca-input-group-prepend">
					<!-- type="button" (not submit) so it doesn't collide with a page's real
					     submit button (e.g. the login form). Enter in the input still submits
					     the GET form via native implicit submission; the magnifier submits it
					     programmatically. -->
					<button
						type="button"
						class="ca-btn ca-btn-default"
						aria-label={t('portal.header.search')}
						onclick={(e) => e.currentTarget.closest('form')?.requestSubmit()}
					>
						<i class="fas fa-search" aria-hidden="true"></i>
					</button>
				</div>
				<input
					class="ca-input ca-search-input"
					type="text"
					name="search"
					placeholder={t('portal.header.searchPlaceholder')}
				/>
			</div>
		</form>

		<ul class="ca-toolbar" style="list-style:none;margin:0;padding:0">
			<li>
				<a
					class="ca-cart-btn"
					href="/order/cart"
					data-testid={cartTestid}
					title={t('portal.header.cart')}
				>
					<i class="fas fa-shopping-cart" aria-hidden="true"></i>
					<span class="ca-badge-info" data-testid={cartCountTestid}>{cart.count}</span>
					<span
						class="sr-only"
						style="position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0)"
					>
						{t('portal.header.cart')}
					</span>
				</a>
			</li>
		</ul>
	</div>
</header>
