<script lang="ts">
	import { appName } from '$lib/appName';

	import { addNewLinks, topMenus } from './nav';

	interface Props {
		userName: string;
		locale: string;
		onHamburger: () => void;
		onToggleLocale: () => void;
	}

	let { userName, locale, onHamburger, onToggleLocale }: Props = $props();

	// Which dropdown is open: a top-menu label, '+' (add-new), 'account', or null.
	let openMenu = $state<string | null>(null);

	function toggle(key: string) {
		openMenu = openMenu === key ? null : key;
	}
	function close() {
		openMenu = null;
	}
</script>

<nav class="hp-nav">
	<button
		class="hp-navlink hp-hamburger"
		style="padding:0 15px;color:#fff;font-size:18px"
		onclick={onHamburger}
		aria-label="Menu"
	>
		<i class="fas fa-bars" aria-hidden="true"></i>
	</button>

	<a class="hp-brand" href="/admin">
		<i class="fas fa-cloud" aria-hidden="true"></i><span data-testid="app-name">{appName}</span>
	</a>

	<!-- Add New (+) -->
	<div class="hp-nav-menu hp-navdesk" class:open={openMenu === '+'}>
		<button
			class="hp-navlink"
			style="font-size:16px"
			onclick={() => toggle('+')}
			aria-label="Add new"
		>
			<i class="fas fa-plus" aria-hidden="true"></i>
		</button>
		{#if openMenu === '+'}
			<div class="hp-dropdown">
				{#each addNewLinks as ln (ln.href + ln.label)}
					<a class="hp-dditem" href={ln.href} onclick={close}>{ln.label}</a>
				{/each}
			</div>
		{/if}
	</div>

	<!-- Main menus -->
	{#each topMenus as m (m.label)}
		<div class="hp-nav-menu hp-navdesk" class:open={openMenu === m.label}>
			<button class="hp-navlink" onclick={() => toggle(m.label)}>
				{m.label}
				<i
					class="fas fa-caret-down"
					style="font-size:11px;opacity:.7;margin-left:3px"
					aria-hidden="true"
				></i>
			</button>
			{#if openMenu === m.label}
				<div class="hp-dropdown">
					{#each m.links as ln (ln.href + ln.label)}
						<a class="hp-dditem" href={ln.href} onclick={close}>{ln.label}</a>
					{/each}
				</div>
			{/if}
		</div>
	{/each}

	<div class="hp-nav-spacer"></div>

	<a class="hp-navlink hp-navdesk" href="/admin/clients" title="Search"
		><i class="fas fa-search" aria-hidden="true"></i></a
	>
	<a class="hp-navlink" href="/admin/settings/overview" title="Configuration"
		><i class="fas fa-cogs" aria-hidden="true"></i></a
	>

	<!-- Account -->
	<div class="hp-nav-menu" class:open={openMenu === 'account'}>
		<button
			class="hp-navlink"
			style="font-size:20px"
			onclick={() => toggle('account')}
			title={userName}
			aria-label="Account"
		>
			<i class="fas fa-user-circle" aria-hidden="true"></i>
		</button>
		{#if openMenu === 'account'}
			<div class="hp-dropdown right" style="min-width:180px">
				<div
					style="padding:6px 16px;color:#999;font-size:12px;border-bottom:1px solid #eee;margin-bottom:4px"
				>
					{userName}
				</div>
				<button
					class="hp-dditem"
					style="width:100%;text-align:left;border:none;background:none;font:inherit"
					onclick={() => {
						onToggleLocale();
						close();
					}}
				>
					Language: {locale.toUpperCase()}
				</button>
				<form method="POST" action="/logout">
					<button
						type="submit"
						class="hp-dditem"
						style="width:100%;text-align:left;border:none;background:none;font:inherit;cursor:pointer"
						data-testid="logout-button"
					>
						Logout
					</button>
				</form>
			</div>
		{/if}
	</div>
</nav>

{#if openMenu}
	<button class="hp-nav-scrim" onclick={close} aria-label="Close menu" tabindex="-1"></button>
{/if}
