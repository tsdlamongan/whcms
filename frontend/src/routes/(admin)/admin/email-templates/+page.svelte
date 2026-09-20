<script lang="ts">
	import { appName } from '$lib/appName';
	import { toast } from '$lib/stores/toast.svelte';
	import type { PageProps } from './$types';
	import type { EmailTemplateRow } from './+page.server';

	let { data }: PageProps = $props();

	interface Category {
		name: string;
		keys: string[];
	}

	// Grouping per docs/DESIGN.md §8.5 (WHMCS Email Templates page: two columns of
	// per-category mini-tables). Keys sourced from backend/migrations/000002_seed_core.up.sql -
	// every seeded template key is accounted for below; anything unrecognised still
	// renders (see `otherRows`) so nothing is ever silently dropped.
	const leftCategories: Category[] = [
		// The global layout wraps every other template's body - surfaced first
		// because editing it restyles all outbound email at once.
		{ name: 'Global Layout', keys: ['_layout'] },
		{ name: 'Account', keys: ['verify_email', 'reset_password'] },
		{
			name: 'Invoice Messages',
			keys: ['invoice_created', 'invoice_reminder', 'invoice_overdue', 'payment_received']
		},
		{ name: 'System', keys: ['admin_alert'] }
	];

	const rightCategories: Category[] = [
		{
			name: 'Product/Service Messages',
			keys: [
				'service_activated',
				'service_suspended',
				'service_unsuspended',
				'service_terminated',
				'service_renewed'
			]
		},
		{ name: 'Domain Messages', keys: ['domain_registered', 'domain_renewed'] },
		{ name: 'Support', keys: ['ticket_opened', 'ticket_replied'] }
	];

	const knownKeys = $derived(
		new Set([...leftCategories, ...rightCategories].flatMap((c) => c.keys))
	);

	function rowsFor(keys: string[]): EmailTemplateRow[] {
		return data.templates
			.filter((t) => keys.includes(t.key))
			.slice()
			.sort((a, b) => {
				const ai = keys.indexOf(a.key);
				const bi = keys.indexOf(b.key);
				return ai !== bi ? ai - bi : a.locale.localeCompare(b.locale);
			});
	}

	const leftGroups = $derived(
		leftCategories
			.map((c) => ({ name: c.name, rows: rowsFor(c.keys) }))
			.filter((g) => g.rows.length > 0)
	);
	const rightGroups = $derived(
		rightCategories
			.map((c) => ({ name: c.name, rows: rowsFor(c.keys) }))
			.filter((g) => g.rows.length > 0)
	);
	// Any template key not covered by the map above (e.g. a future addition) still shows up here.
	const otherRows = $derived(data.templates.filter((t) => !knownKeys.has(t.key)));

	const isEmpty = $derived(data.templates.length === 0);
</script>

{#snippet categoryPanel(name: string, rows: EmailTemplateRow[])}
	<div class="hp-panel">
		<div class="hp-panel-hd"><span class="title">{name}</span></div>
		<div class="hp-scroll">
			<table class="hp-table">
				<thead>
					<tr>
						<th class="c" style="width:50px">Status</th>
						<th>Template Name</th>
						<th class="c" style="width:56px"></th>
					</tr>
				</thead>
				<tbody>
					{#each rows as row (row.id)}
						<tr class="hp-row">
							<td class="c">
								<i
									class="fas fa-check-circle"
									style="color:#5cb85c;font-size:15px"
									title="Configured"
								></i>
							</td>
							<td>
								<a
									class="cell-link"
									href={`/admin/email-templates/${row.key}/${row.locale}`}
									data-testid={`row-template-${row.key}-${row.locale}`}
								>
									{row.key}
								</a>
								<span
									style="display:inline-block;margin-left:6px;padding:1px 6px;border-radius:3px;font-size:10px;font-weight:700;background:#e8f2fb;color:#31708f;text-transform:uppercase"
								>
									{row.locale}
								</span>
							</td>
							<td class="c">
								<a
									class="cell-link"
									href={`/admin/email-templates/${row.key}/${row.locale}`}
									title="Edit"
								>
									<i class="far fa-edit" style="color:#5b9bd5"></i>
								</a>
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	</div>
{/snippet}

<svelte:head>
	<title>Email Templates — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Email Templates</h1>

<p class="hp-lead">
	Manage the automated emails sent to clients and staff. Templates are grouped by category below —
	click a template name to edit its subject and body for that locale.
</p>

{#if data.listError}
	<div class="hp-alert-yellow" data-testid="template-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

<div style="display:flex;gap:8px;margin-bottom:16px;flex-wrap:wrap">
	<button
		type="button"
		class="hp-btn"
		onclick={() =>
			toast.info('Template keys are fixed by the system — edit one of the templates below.')}
	>
		<i class="fas fa-plus-circle" style="color:#5cb85c"></i>Create New Email Template
	</button>
	<button
		type="button"
		class="hp-btn"
		onclick={() =>
			toast.info(
				'Language is set per template row — edit a template to change its locale content.'
			)}
	>
		<i class="fas fa-language" style="color:#5b9bd5"></i>Manage Languages
	</button>
</div>

<form method="get" class="hp-filter" data-testid="template-filter-form">
	<div class="grow">
		<div class="hp-field-label">Search</div>
		<input
			class="hp-input"
			type="search"
			name="search"
			value={data.search}
			placeholder="Template key or subject…"
			data-testid="template-search-input"
		/>
	</div>
	<button class="hp-btn hp-btn-primary" type="submit" data-testid="template-filter-submit">
		<i class="fas fa-search"></i>Filter
	</button>
</form>

{#if isEmpty}
	<div class="hp-panel" style="padding:28px;text-align:center;color:#999">No templates found</div>
{:else}
	<div class="hp-tpl-grid">
		<div style="display:flex;flex-direction:column;gap:16px">
			{#each leftGroups as g (g.name)}
				{@render categoryPanel(g.name, g.rows)}
			{/each}
		</div>
		<div style="display:flex;flex-direction:column;gap:16px">
			{#each rightGroups as g (g.name)}
				{@render categoryPanel(g.name, g.rows)}
			{/each}
		</div>
	</div>

	{#if otherRows.length > 0}
		<div style="margin-top:16px">
			{@render categoryPanel('Other Templates', otherRows)}
		</div>
	{/if}
{/if}

<style>
	.hp-tpl-grid {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 16px;
		align-items: start;
	}
	@media (max-width: 900px) {
		.hp-tpl-grid {
			grid-template-columns: 1fr;
		}
	}
</style>
