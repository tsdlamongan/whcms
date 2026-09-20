<script lang="ts">
	import { appName } from '$lib/appName';

	import { sidebarSections, topMenus, type SidebarKind } from './nav';

	/** ports.PresenceEntry - one admin/staff user active within the presence window. */
	interface OnlineStaffEntry {
		user_id: number;
		email: string;
		role: string;
	}

	interface Props {
		kind: SidebarKind;
		pathname: string;
		search: string;
		userName: string;
		/** Currently-active admin/staff users (HostPanel "Staff Online" widget). */
		onlineStaff?: OnlineStaffEntry[];
		/** Mobile drawer open state. */
		open?: boolean;
		/** Collapsed-to-rail state. */
		minimised?: boolean;
		onCloseDrawer: () => void;
		onToggleMinimise: () => void;
	}

	let {
		kind,
		pathname,
		search,
		userName,
		onlineStaff = [],
		open = false,
		minimised = false,
		onCloseDrawer,
		onToggleMinimise
	}: Props = $props();

	// Fall back to just the current user if the presence list hasn't
	// populated yet (e.g. a fresh Redis instance) so the widget never
	// renders empty.
	const staffOnline = $derived(
		onlineStaff.length > 0 ? onlineStaff : [{ user_id: 0, email: userName, role: '' }]
	);

	const sections = $derived(sidebarSections(kind));

	function isActive(href: string): boolean {
		const [lpath, lquery] = href.split('?');
		if (pathname !== lpath) return false;
		const cur = search.startsWith('?') ? search.slice(1) : search;
		if (lquery) return cur.includes(lquery);
		return !cur.includes('status=');
	}
</script>

{#if minimised}
	<div class="hp-sidebar-min hp-navdesk">
		<button
			type="button"
			title="Expand sidebar"
			onclick={onToggleMinimise}
			style="border:none;background:none;cursor:pointer;color:#666"
			aria-label="Expand sidebar"
		>
			<i class="fas fa-angle-double-right" aria-hidden="true"></i>
		</button>
	</div>
{:else}
	<aside class="hp-sidebar" class:open>
		<!-- Mobile-only nav list mirroring the top navbar menus -->
		<div class="hp-navmob">
			{#each topMenus as m (m.label)}
				<a
					href={m.href}
					onclick={onCloseDrawer}
					style="display:block;padding:8px 14px;color:#202F60;border-bottom:1px solid #e4e4e4;font-weight:600"
				>
					{m.label}
				</a>
			{/each}
		</div>

		{#each sections as s (s.title)}
			<div>
				<div class="hp-side-head"><i class={s.icon} aria-hidden="true"></i>{s.title}</div>
				<div class="hp-side-links">
					{#each s.links as ln (ln.href + ln.label)}
						<a
							class="hp-slink"
							class:sub={ln.sub}
							class:danger={ln.danger}
							class:active={isActive(ln.href)}
							href={ln.href}
							onclick={onCloseDrawer}
						>
							{ln.label}
						</a>
					{/each}
				</div>
			</div>
		{/each}

		{#if kind === 'dashboard'}
			<div>
				<div class="hp-side-head">
					<i class="fas fa-info-circle" aria-hidden="true"></i>System Information
				</div>
				<div class="hp-side-body">
					Registered To: {appName}<br />
					License Type: Open Source<br />
					Expires: Never<br />
					Version: 1.0.0
				</div>
			</div>
		{/if}

		{#if kind !== 'config'}
			<div>
				<div class="hp-side-head">
					<i class="fas fa-users" aria-hidden="true"></i>Staff Online ({staffOnline.length})
				</div>
				<div class="hp-side-body" style="padding:0">
					{#each staffOnline as s (s.user_id || s.email)}
						<div style="padding:4px 14px;color:#337ab7">{s.email}</div>
					{/each}
				</div>
			</div>
			<button type="button" class="hp-side-minimise hp-navdesk" onclick={onToggleMinimise}>
				« Minimise Sidebar
			</button>
		{/if}
	</aside>
{/if}
