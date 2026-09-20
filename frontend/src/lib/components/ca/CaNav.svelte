<script lang="ts">
	import { appName } from '$lib/appName';
	import { t } from '$lib/i18n';
	import { accountMenu, isActivePath, primaryNav, type CaNavItem, type CaNavLink } from './nav';

	/**
	 * Twenty-One primary navigation (DESIGN §5). Dropdown open/close mechanics
	 * mirror HpNavbar (a single `openMenu` key + a full-screen scrim). The
	 * hamburger drives a self-contained slide-in drawer for < md.
	 *
	 * Testids preserved for the host layouts: `client-nav` (left list), `nav-<id>`
	 * / `nav-mobile-<id>` (items), `nav-mobile-toggle` (hamburger), `user-name`
	 * and `logout-button` (account). `loginTestid` / `clientAreaTestid` let the
	 * /order layout keep its legacy `order-login-link` / `client-area-link`.
	 */
	interface Props {
		user?: SessionUser | null;
		pathname: string;
		loginTestid?: string;
		clientAreaTestid?: string;
	}

	let { user, pathname, loginTestid, clientAreaTestid }: Props = $props();

	const primary = $derived(primaryNav(user));
	const overflow = $derived(primary.filter((i) => i.overflow));
	const account = $derived(accountMenu(!!user));

	let openMenu = $state<string | null>(null);
	let drawerOpen = $state(false);

	function toggle(key: string) {
		openMenu = openMenu === key ? null : key;
	}
	function close() {
		openMenu = null;
	}
	function closeDrawer() {
		drawerOpen = false;
	}

	function itemActive(item: CaNavItem): boolean {
		if (isActivePath(pathname, item.href)) return true;
		return (item.children ?? []).some((c) => isActivePath(pathname, c.href));
	}

	/** Right-menu item testid, honoring the /order legacy overrides. */
	function accountTestid(link: CaNavLink): string {
		if (link.id === 'login' && loginTestid) return loginTestid;
		if (link.id === 'dashboard' && clientAreaTestid) return clientAreaTestid;
		return `nav-${link.id}`;
	}
</script>

<nav class="ca-nav">
	<div class="ca-container">
		<!-- Hamburger (mobile) -->
		<button
			type="button"
			class="ca-hamburger"
			aria-label={t('portal.nav.menu')}
			data-testid="nav-mobile-toggle"
			onclick={() => (drawerOpen = true)}
		>
			<i class="fas fa-bars" aria-hidden="true"></i>
			<span class="label">{t('portal.nav.menu')}</span>
		</button>

		<!-- Primary (left) nav -->
		<ul class="ca-nav-list" data-testid="client-nav">
			{#each primary as item (item.id)}
				<li class="ca-nav-item" class:ca-dropdown={item.children} class:overflow={item.overflow}>
					{#if item.children}
						<button
							type="button"
							class="ca-navlink"
							class:active={itemActive(item)}
							data-testid={`nav-${item.id}`}
							onclick={() => toggle(item.id)}
						>
							{t(item.labelKey)}
							<i class="caret fas fa-caret-down" aria-hidden="true"></i>
						</button>
						<div class="ca-dropdown-menu" class:open={openMenu === item.id}>
							{#each item.children as c (c.id)}
								<a class="ca-dropdown-item" href={c.href} onclick={close}>{t(c.labelKey)}</a>
							{/each}
						</div>
					{:else}
						<a
							class="ca-navlink"
							class:active={itemActive(item)}
							href={item.href}
							data-testid={`nav-${item.id}`}
						>
							{t(item.labelKey)}
						</a>
					{/if}
				</li>
			{/each}

			<!-- "More" overflow (visible only in the md..lg band) -->
			<li class="ca-nav-item ca-dropdown ca-more">
				<button type="button" class="ca-navlink" onclick={() => toggle('more')}>
					{t('portal.nav.more')}
					<i class="caret fas fa-caret-down" aria-hidden="true"></i>
				</button>
				<div class="ca-dropdown-menu" class:open={openMenu === 'more'}>
					{#each overflow as item (item.id)}
						<a class="ca-dropdown-item" href={item.href} onclick={close}>{t(item.labelKey)}</a>
					{/each}
				</div>
			</li>
		</ul>

		<!-- Account (right) -->
		<ul class="ca-nav-list right">
			<li class="ca-nav-item ca-dropdown">
				<button
					type="button"
					class="ca-navlink"
					class:active={openMenu === 'account'}
					onclick={() => toggle('account')}
				>
					{#if user}
						<i class="fas fa-user-circle" aria-hidden="true"></i>
						<span data-testid="user-name">{user.name}</span>
					{:else}
						<i class="fas fa-user" aria-hidden="true"></i>
						{t('portal.nav.account')}
					{/if}
					<i class="caret fas fa-caret-down" aria-hidden="true"></i>
				</button>
				<div class="ca-dropdown-menu right" class:open={openMenu === 'account'}>
					{#each account as a (a.id)}
						<a
							class="ca-dropdown-item"
							href={a.href}
							data-testid={accountTestid(a)}
							onclick={close}
						>
							{#if a.icon}<i class={a.icon} aria-hidden="true"></i>{/if}
							{t(a.labelKey)}
						</a>
					{/each}
					{#if user}
						<div class="ca-dropdown-divider"></div>
						<form method="POST" action="/logout">
							<button type="submit" class="ca-dropdown-item" data-testid="logout-button">
								<i class="fas fa-sign-out-alt" aria-hidden="true"></i>
								{t('portal.nav.logout')}
							</button>
						</form>
					{/if}
				</div>
			</li>
		</ul>
	</div>
</nav>

{#if openMenu}
	<button type="button" class="ca-nav-scrim" aria-label={t('portal.nav.close')} onclick={close}
	></button>
{/if}

<!-- Mobile drawer -->
{#if drawerOpen}
	<button
		type="button"
		class="ca-navmob-scrim"
		aria-label={t('portal.nav.close')}
		onclick={closeDrawer}
	></button>
{/if}
<nav class="ca-navmob" class:open={drawerOpen} aria-label={t('portal.nav.menu')}>
	<div class="ca-navmob-head">
		<span>{appName}</span>
		<button
			type="button"
			class="ca-navmob-close"
			aria-label={t('portal.nav.close')}
			onclick={closeDrawer}
		>
			<i class="fas fa-times" aria-hidden="true"></i>
		</button>
	</div>

	{#each primary as item (item.id)}
		<a
			href={item.href}
			class:active={itemActive(item)}
			data-testid={`nav-mobile-${item.id}`}
			onclick={closeDrawer}
		>
			{t(item.labelKey)}
		</a>
		{#if item.children}
			{#each item.children as c (c.id)}
				<a href={c.href} class="sub" onclick={closeDrawer}>{t(c.labelKey)}</a>
			{/each}
		{/if}
	{/each}

	<div class="ca-navmob-section">{user ? user.name : t('portal.nav.account')}</div>
	{#each account as a (a.id)}
		<a href={a.href} data-testid={`nav-mobile-${a.id}`} onclick={closeDrawer}>{t(a.labelKey)}</a>
	{/each}
	{#if user}
		<form method="POST" action="/logout">
			<button type="submit" class="ca-navmob-link" data-testid="nav-mobile-logout">
				{t('portal.nav.logout')}
			</button>
		</form>
	{/if}
</nav>
