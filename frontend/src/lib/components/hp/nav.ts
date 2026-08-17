/**
 * HostPanel admin navigation model - ported from HostPanel.dc.html's buildNav /
 * buildSidebar, with the mockup's client-side `go(screen)` handlers replaced by
 * real SvelteKit admin routes. The top navbar is constant; the left sidebar is
 * contextual and derived from the current pathname (`sidebarKind`).
 */

export interface HpLink {
	label: string;
	href: string;
}

export interface HpNavMenu {
	label: string;
	/** Clicking the menu label itself navigates here. */
	href: string;
	links: HpLink[];
}

export interface HpSideLink {
	label: string;
	href: string;
	/** Indented sub-item (prefixed with "- " in the design). */
	sub?: boolean;
	/** Destructive action rendered in red. */
	danger?: boolean;
}

export interface HpSideSection {
	title: string;
	icon: string;
	links: HpSideLink[];
}

export type SidebarKind = 'dashboard' | 'clients' | 'billing' | 'support' | 'content' | 'config';

/** Top navbar "+" (Add New) quick actions. */
export const addNewLinks: HpLink[] = [
	{ label: 'New Client', href: '/admin/clients/new' },
	{ label: 'New Order', href: '/admin/orders' },
	{ label: 'New Invoice', href: '/admin/invoices/new' },
	{ label: 'New Product', href: '/admin/products/new' },
	{ label: 'New Ticket', href: '/admin/tickets' }
];

/** Top navbar dropdown menus (Clients · Orders · Billing · Support · Reports · Utilities · Addons). */
export const topMenus: HpNavMenu[] = [
	{
		label: 'Clients',
		href: '/admin/clients',
		links: [
			{ label: 'View/Search Clients', href: '/admin/clients' },
			{ label: 'Add New Client', href: '/admin/clients/new' },
			{ label: 'Products/Services', href: '/admin/services' },
			{ label: 'Cancellation Requests', href: '/admin/services/cancellation-requests' },
			{ label: 'Domain Registrations', href: '/admin/domains' }
		]
	},
	{
		label: 'Orders',
		href: '/admin/orders',
		links: [
			{ label: 'List All Orders', href: '/admin/orders' },
			{ label: 'Pending Orders', href: '/admin/orders?status=pending' },
			{ label: 'Add New Order', href: '/admin/orders' },
			{ label: 'Fraud Orders', href: '/admin/orders?status=fraud' }
		]
	},
	{
		label: 'Billing',
		href: '/admin/invoices',
		links: [
			{ label: 'Invoices', href: '/admin/invoices' },
			{ label: 'Transactions List', href: '/admin/transactions' },
			{ label: 'Reports', href: '/admin/reports/revenue' }
		]
	},
	{
		label: 'Support',
		href: '/admin/tickets',
		links: [
			{ label: 'Support Tickets', href: '/admin/tickets' },
			{ label: 'Departments', href: '/admin/departments' }
		]
	},
	{
		label: 'Content',
		href: '/admin/announcements',
		links: [
			{ label: 'Announcements', href: '/admin/announcements' },
			{ label: 'Knowledgebase', href: '/admin/knowledgebase' },
			{ label: 'Knowledgebase Articles', href: '/admin/knowledgebase/articles' },
			{ label: 'Network Status', href: '/admin/network-status' }
		]
	},
	{
		label: 'Reports',
		href: '/admin/reports/revenue',
		links: [
			{ label: 'Revenue', href: '/admin/reports/revenue' },
			{ label: 'Orders', href: '/admin/reports/orders' },
			{ label: 'Services', href: '/admin/reports/services' }
		]
	},
	{
		label: 'Utilities',
		href: '/admin/servers',
		links: [
			{ label: 'Domains/TLDs', href: '/admin/domains' },
			{ label: 'Servers', href: '/admin/servers' },
			{ label: 'Activity Log', href: '/admin/logs/audit' },
			{ label: 'Email Log', href: '/admin/logs/email' },
			{ label: 'Pending Module Actions', href: '/admin/logs/queue' }
		]
	},
	{
		label: 'Addons',
		href: '/admin/registrars',
		links: [
			{ label: 'Domain Registrars', href: '/admin/registrars' },
			{ label: 'Domain Addons', href: '/admin/domains/addons' }
		]
	}
];

/** Pick the contextual sidebar variant for a given admin pathname. */
export function sidebarKind(pathname: string): SidebarKind {
	const p = pathname;
	if (
		p.startsWith('/admin/clients') ||
		p.startsWith('/admin/orders') ||
		p.startsWith('/admin/services')
	)
		return 'clients';
	if (p.startsWith('/admin/invoices') || p.startsWith('/admin/transactions')) return 'billing';
	if (p.startsWith('/admin/tickets') || p.startsWith('/admin/departments')) return 'support';
	if (
		p.startsWith('/admin/announcements') ||
		p.startsWith('/admin/knowledgebase') ||
		p.startsWith('/admin/network-status')
	)
		return 'content';
	if (
		p.startsWith('/admin/products') ||
		p.startsWith('/admin/product-groups') ||
		p.startsWith('/admin/coupons') ||
		p.startsWith('/admin/domains') ||
		p.startsWith('/admin/servers') ||
		p.startsWith('/admin/gateways') ||
		p.startsWith('/admin/registrars') ||
		p.startsWith('/admin/settings') ||
		p.startsWith('/admin/email-templates') ||
		p.startsWith('/admin/staff')
	)
		return 'config';
	return 'dashboard';
}

/** Sidebar section definitions per kind (links point at real routes). */
export function sidebarSections(kind: SidebarKind): HpSideSection[] {
	switch (kind) {
		case 'clients':
			return [
				{
					title: 'Clients',
					icon: 'fas fa-users',
					links: [
						{ label: 'View/Search Clients', href: '/admin/clients' },
						{ label: 'Add New Client', href: '/admin/clients/new' }
					]
				},
				{
					title: 'Products/Services',
					icon: 'fas fa-box',
					links: [
						{ label: 'List All Services', href: '/admin/services' },
						{ label: '- Active', href: '/admin/services?status=active', sub: true },
						{ label: '- Pending', href: '/admin/services?status=pending', sub: true },
						{ label: '- Suspended', href: '/admin/services?status=suspended', sub: true },
						{ label: 'Cancellation Requests', href: '/admin/services/cancellation-requests' },
						{ label: 'Orders', href: '/admin/orders' },
						{ label: 'Domain Registrations', href: '/admin/domains' }
					]
				}
			];
		case 'billing':
			return [
				{
					title: 'Billing',
					icon: 'fas fa-file-invoice-dollar',
					links: [
						{ label: 'Transactions List', href: '/admin/transactions' },
						{ label: 'Revenue Report', href: '/admin/reports/revenue' }
					]
				},
				{
					title: 'Invoices',
					icon: 'fas fa-file-invoice',
					links: [
						{ label: 'List All Invoices', href: '/admin/invoices' },
						{ label: '- Paid', href: '/admin/invoices?status=paid', sub: true },
						{ label: '- Unpaid', href: '/admin/invoices?status=unpaid', sub: true },
						{ label: '- Overdue', href: '/admin/invoices?status=overdue', sub: true },
						{ label: '- Cancelled', href: '/admin/invoices?status=cancelled', sub: true },
						{ label: '- Refunded', href: '/admin/invoices?status=refunded', sub: true },
						{ label: 'Create New Invoice', href: '/admin/invoices/new' }
					]
				}
			];
		case 'support':
			return [
				{
					title: 'Support',
					icon: 'fas fa-life-ring',
					links: [
						{ label: 'Support Tickets', href: '/admin/tickets' },
						{ label: '- Open', href: '/admin/tickets?status=open', sub: true },
						{ label: '- Answered', href: '/admin/tickets?status=answered', sub: true },
						{ label: '- Closed', href: '/admin/tickets?status=closed', sub: true },
						{ label: 'Departments', href: '/admin/departments' }
					]
				}
			];
		case 'content':
			return [
				{
					title: 'Announcements',
					icon: 'fas fa-bullhorn',
					links: [
						{ label: 'All Announcements', href: '/admin/announcements' },
						{ label: '- Published', href: '/admin/announcements?status=published', sub: true },
						{ label: '- Drafts', href: '/admin/announcements?status=draft', sub: true },
						{ label: 'Add New Announcement', href: '/admin/announcements/new' }
					]
				},
				{
					title: 'Knowledgebase',
					icon: 'fas fa-book',
					links: [
						{ label: 'Categories', href: '/admin/knowledgebase' },
						{ label: 'Articles', href: '/admin/knowledgebase/articles' },
						{ label: 'Add New Article', href: '/admin/knowledgebase/articles/new' }
					]
				},
				{
					title: 'Network Status',
					icon: 'fas fa-network-wired',
					links: [
						{ label: 'All Entries', href: '/admin/network-status' },
						{ label: 'Add New Entry', href: '/admin/network-status/new' }
					]
				}
			];
		case 'config':
			return [
				{
					title: 'Configuration',
					icon: 'fas fa-wrench',
					links: [
						{ label: 'General Settings', href: '/admin/settings' },
						{ label: 'Setup Overview', href: '/admin/settings/overview' },
						{ label: 'Email Templates', href: '/admin/email-templates' },
						{ label: 'Client Groups', href: '/admin/settings' }
					]
				},
				{
					title: 'Staff Management',
					icon: 'fas fa-user',
					links: [{ label: 'Administrator Users', href: '/admin/staff' }]
				},
				{
					title: 'Payments',
					icon: 'fas fa-credit-card',
					links: [
						{ label: 'Payment Gateways', href: '/admin/gateways' },
						{ label: 'Promotions / Coupons', href: '/admin/coupons' }
					]
				},
				{
					title: 'Products/Services',
					icon: 'fas fa-box',
					links: [
						{ label: 'Products/Services', href: '/admin/products' },
						{ label: 'Product Groups', href: '/admin/product-groups' },
						{ label: 'Domain Pricing', href: '/admin/domains/pricing' },
						{ label: 'Domain Addons', href: '/admin/domains/addons' },
						{ label: 'Domain Registrars', href: '/admin/registrars' },
						{ label: 'Servers', href: '/admin/servers' }
					]
				}
			];
		default:
			return [
				{
					title: 'Shortcuts',
					icon: 'fas fa-bolt',
					links: [
						{ label: 'Add New Client', href: '/admin/clients/new' },
						{ label: 'Add New Order', href: '/admin/orders' },
						{ label: 'Create New Invoice', href: '/admin/invoices/new' },
						{ label: 'Open New Ticket', href: '/admin/tickets' },
						{ label: 'View Reports', href: '/admin/reports/revenue' },
						{ label: 'Generate Due Invoices', href: '/admin/invoices', danger: true },
						{ label: 'Run Automation', href: '/admin/logs/audit', danger: true }
					]
				}
			];
	}
}

/** Page <h1> title from the pathname (mirrors the design's `titles` map). */
export function pageTitle(pathname: string): string {
	const map: [string, string][] = [
		['/admin/clients/new', 'Add New Client'],
		['/admin/clients', 'View/Search Clients'],
		['/admin/invoices/new', 'Create New Invoice'],
		['/admin/invoices', 'Invoices'],
		['/admin/transactions', 'Transactions'],
		['/admin/tickets', 'Support Tickets'],
		['/admin/departments', 'Support Departments'],
		['/admin/announcements/new', 'Add New Announcement'],
		['/admin/announcements', 'Announcements'],
		['/admin/knowledgebase/articles/new', 'Add New Article'],
		['/admin/knowledgebase/articles', 'Knowledgebase Articles'],
		['/admin/knowledgebase', 'Knowledgebase'],
		['/admin/network-status/new', 'Add Network Status Entry'],
		['/admin/network-status', 'Network Status'],
		['/admin/products/new', 'Create New Product'],
		['/admin/products', 'Products/Services'],
		['/admin/product-groups', 'Product Groups'],
		['/admin/coupons', 'Promotions'],
		['/admin/services/cancellation-requests', 'Cancellation Requests'],
		['/admin/services', 'Products/Services'],
		['/admin/domains/pricing', 'TLD Pricing'],
		['/admin/domains/addons', 'Domain Addons'],
		['/admin/domains', 'Domains/TLDs'],
		['/admin/servers', 'Servers'],
		['/admin/gateways', 'Payment Gateways'],
		['/admin/registrars', 'Domain Registrars'],
		['/admin/settings/overview', 'System Settings'],
		['/admin/settings', 'General Settings'],
		['/admin/email-templates', 'Email Templates'],
		['/admin/staff', 'Administrator Users'],
		['/admin/reports', 'Reports'],
		['/admin/orders', 'Orders'],
		['/admin/logs/email', 'Email Log'],
		['/admin/logs/integration', 'Integration Log'],
		['/admin/logs/queue', 'Pending Module Actions'],
		['/admin/logs/audit', 'Activity Log'],
		['/admin/logs', 'Activity Log']
	];
	for (const [prefix, title] of map) {
		if (
			pathname === prefix ||
			pathname.startsWith(prefix + '/') ||
			pathname.startsWith(prefix + '?')
		)
			return title;
	}
	return 'Dashboard';
}
