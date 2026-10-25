/**
 * Twenty-One (client/public) navigation model - the CaShell counterpart to
 * hp/nav.ts. Labels are stored as i18n keys (`portal.nav.*`) and resolved with
 * `t()` in the components so the menu re-renders on locale change.
 *
 * The primary (left) nav is constant across public/store/client; the right-hand
 * account area swaps between the logged-out (Login / Register / Forgot) and the
 * logged-in client menu (Dashboard / My Services / … / Logout).
 */

export interface CaNavLink {
	/** Stable id - also drives `data-testid="nav-<id>"` / `nav-mobile-<id>`. */
	id: string;
	/** i18n key (resolve with `t(labelKey)`). */
	labelKey: string;
	href: string;
	/** Optional Font Awesome icon class. */
	icon?: string;
}

export interface CaNavItem extends CaNavLink {
	/** Dropdown children (renders a caret + menu). */
	children?: CaNavLink[];
	/** Fold into the "More" overflow in the md..lg band. */
	overflow?: boolean;
}

/** Store dropdown (DESIGN §5). */
export const storeMenu: CaNavLink[] = [
	{ id: 'browse-all', labelKey: 'portal.nav.browseAll', href: '/order' },
	{ id: 'register-domain', labelKey: 'portal.nav.registerDomain', href: '/order/domain' },
	{ id: 'transfer-domain', labelKey: 'portal.nav.transferDomain', href: '/order/domain' },
	{ id: 'domain-snipe', labelKey: 'portal.nav.domainSnipe', href: '/snipes/new' }
];

/**
 * Primary (left) nav. When logged in, "Home" resolves to /dashboard (DESIGN §5).
 */
export function primaryNav(user?: SessionUser | null): CaNavItem[] {
	return [
		{ id: 'home', labelKey: 'portal.nav.home', href: user ? '/dashboard' : '/' },
		{ id: 'store', labelKey: 'portal.nav.store', href: '/order', children: storeMenu },
		{ id: 'announcements', labelKey: 'portal.nav.announcements', href: '/announcements' },
		{ id: 'knowledgebase', labelKey: 'portal.nav.knowledgebase', href: '/knowledgebase' },
		{
			id: 'network-status',
			labelKey: 'portal.nav.networkStatus',
			href: '/network-status',
			overflow: true
		},
		{ id: 'contact', labelKey: 'portal.nav.contactUs', href: '/contact', overflow: true }
	];
}

/**
 * Right-hand account menu. Logged-in yields the client links (with `Logout`
 * rendered as a POST form by the component); logged-out yields the auth links.
 */
export function accountMenu(loggedIn: boolean): CaNavLink[] {
	if (loggedIn) {
		return [
			{
				id: 'dashboard',
				labelKey: 'portal.nav.dashboard',
				href: '/dashboard',
				icon: 'fas fa-home'
			},
			{
				id: 'services',
				labelKey: 'portal.nav.myServices',
				href: '/services',
				icon: 'fas fa-cubes'
			},
			{ id: 'domains', labelKey: 'portal.nav.myDomains', href: '/domains', icon: 'fas fa-globe' },
			{
				id: 'snipes',
				labelKey: 'portal.nav.mySnipes',
				href: '/snipes',
				icon: 'fas fa-crosshairs'
			},
			{
				id: 'billing',
				labelKey: 'portal.nav.myInvoices',
				href: '/billing',
				icon: 'fas fa-file-invoice-dollar'
			},
			{ id: 'support', labelKey: 'portal.nav.support', href: '/support', icon: 'fas fa-life-ring' },
			{ id: 'account', labelKey: 'portal.nav.editAccount', href: '/account', icon: 'fas fa-user' },
			{
				id: 'email-history',
				labelKey: 'portal.nav.emailHistory',
				href: '/account/email-history',
				icon: 'fas fa-envelope'
			}
		];
	}
	return [
		{ id: 'login', labelKey: 'portal.nav.login', href: '/login', icon: 'fas fa-sign-in-alt' },
		{
			id: 'register',
			labelKey: 'portal.nav.register',
			href: '/register',
			icon: 'fas fa-user-plus'
		},
		{
			id: 'forgot-password',
			labelKey: 'portal.nav.forgotPassword',
			href: '/forgot-password',
			icon: 'fas fa-key'
		}
	];
}

/** Whether a nav link is the active route for the given pathname. */
export function isActivePath(pathname: string, href: string, exact = false): boolean {
	if (href === '/' || href === '/dashboard') return pathname === href;
	if (exact) return pathname === href;
	return pathname === href || pathname.startsWith(href + '/');
}
