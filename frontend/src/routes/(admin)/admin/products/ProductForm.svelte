<script lang="ts">
	import { enhance } from '$app/forms';
	import type { Snippet } from 'svelte';
	import { untrack } from 'svelte';
	import { slugify } from '$lib/slug';
	import {
		AUTO_SETUPS,
		CYCLES,
		PRODUCT_TYPES,
		TAB_FOR_ERROR_KEY,
		type Cycle,
		type PricingRow,
		type ProductGroupRow,
		type ProductRow,
		type ServerGroupRow
	} from './catalog';

	interface Props {
		/** null -> create mode. */
		product?: ProductRow | null;
		pricing?: PricingRow[];
		groups: ProductGroupRow[];
		serverGroups: ServerGroupRow[];
		templateKeys: string[];
		/** Form action, e.g. '?/save'. Omit for the default action. */
		action?: string;
		/** Error text from the failed action (already translated). */
		errorText?: string | null;
		/** Raw i18n key behind errorText - used to auto-switch to the tab that
		 *  holds the fix (see TAB_FOR_ERROR_KEY), since Details/Module/Pricing
		 *  share one physical <form> and a hidden tab's problem is otherwise
		 *  invisible until the user happens to click over to it. */
		errorKey?: string | null;
		submitLabel: string;
		onSuccess?: () => void;
		/** Extra tab panel (configurable options editor) rendered outside the form. */
		optionsTab?: Snippet;
	}

	let {
		product = null,
		pricing = [],
		groups,
		serverGroups,
		templateKeys,
		action,
		errorText = null,
		errorKey = null,
		submitLabel,
		onSuccess,
		optionsTab
	}: Props = $props();

	// form state (initialized once from props)
	let name = $state(untrack(() => product?.name ?? ''));
	let slug = $state(untrack(() => product?.slug ?? ''));
	let description = $state(untrack(() => product?.description ?? ''));
	let type = $state(untrack(() => product?.type ?? 'shared_hosting'));
	let groupId = $state(untrack(() => (product ? String(product.group_id) : '')));
	let hidden = $state(untrack(() => product?.hidden ?? false));
	let sort = $state(untrack(() => product?.sort ?? 0));
	let welcomeTemplate = $state(untrack(() => product?.welcome_email_template ?? ''));

	let module_ = $state(untrack(() => product?.module ?? 'none'));
	let packageName = $state(untrack(() => product?.package_name ?? ''));
	let serverGroupId = $state(
		untrack(() => (product?.server_group_id ? String(product.server_group_id) : ''))
	);
	let autoSetup = $state(untrack(() => product?.auto_setup ?? 'on_payment'));
	let configurable = $state(untrack(() => product?.configurable ?? false));
	let shellAccess = $state(untrack(() => product?.shell_access ?? false));
	let cgiAccess = $state(untrack(() => product?.cgi_access ?? false));
	let featureList = $state(untrack(() => product?.feature_list ?? ''));
	let templatePackage = $state(untrack(() => product?.template_package ?? ''));

	let stockEnabled = $state(untrack(() => product?.stock_enabled ?? false));
	let stockQty = $state(untrack(() => product?.stock_qty ?? 0));

	interface CycleState {
		enabled: boolean;
		price: number;
		setup: number;
	}
	let cycleState = $state<Record<Cycle, CycleState>>(
		untrack(() => {
			const init = {} as Record<Cycle, CycleState>;
			for (const c of CYCLES) {
				const row = pricing.find((p) => p.cycle === c);
				init[c] = {
					enabled: row !== undefined,
					price: row?.price ?? 0,
					setup: row?.setup_fee ?? 0
				};
			}
			return init;
		})
	);

	let slugTouched = $state(untrack(() => product !== null));
	$effect(() => {
		if (!slugTouched) slug = slugify(name);
	});

	// labels
	const typeLabels: Record<string, string> = {
		shared_hosting: 'Shared Hosting',
		reseller_hosting: 'Reseller Hosting',
		domain: 'Domain',
		other: 'Other'
	};
	const autoSetupLabels: Record<string, string> = {
		on_payment: 'After payment is received',
		on_order: 'As soon as the order is placed',
		manual: 'Manually by admin'
	};
	const cycleLabels: Record<Cycle, string> = {
		one_time: 'One time',
		monthly: 'Monthly',
		quarterly: 'Quarterly',
		semiannually: 'Semi-annually',
		annually: 'Annually',
		biennially: 'Biennially'
	};

	// tabs
	interface TabDef {
		id: string;
		label: string;
	}
	let active = $state('details');
	const tabs = $derived<TabDef[]>([
		{ id: 'details', label: 'Details' },
		{ id: 'module', label: 'Module' },
		{ id: 'pricing', label: 'Pricing' },
		...(optionsTab ? [{ id: 'options', label: 'Configurable Options' }] : [])
	]);

	// Jump to whichever tab holds the fix when a submit comes back with a
	// validation error - otherwise a Pricing-tab problem raised while the user
	// is still on Details is invisible (the tabs are just CSS-hidden, not
	// separate steps) and the banner alone doesn't say where to look.
	$effect(() => {
		if (errorKey && TAB_FOR_ERROR_KEY[errorKey]) active = TAB_FOR_ERROR_KEY[errorKey];
	});

	let submitting = $state(false);

	// package picker (Module tab)
	let availablePackages = $state<string[]>([]);
	let loadingPackages = $state(false);
	let packageLoadError = $state<string | null>(null);

	// Stale results from a different group/module shouldn't linger when either
	// changes - otherwise switching groups can silently offer packages from the
	// group the admin just left.
	$effect(() => {
		serverGroupId;
		module_;
		availablePackages = [];
		packageLoadError = null;
	});

	async function loadPackages() {
		if (!serverGroupId) return;
		loadingPackages = true;
		packageLoadError = null;
		try {
			const res = await fetch(
				`/admin/products/module-packages?server_group_id=${encodeURIComponent(serverGroupId)}`
			);
			const body: { ok: boolean; message?: string; packages?: string[] } = await res.json();
			if (!body.ok) {
				availablePackages = [];
				packageLoadError = body.message ?? 'Could not load packages from the server.';
				return;
			}
			availablePackages = body.packages ?? [];
			if (availablePackages.length === 0) {
				packageLoadError = 'No packages found on the server — enter the name manually.';
			}
		} catch {
			availablePackages = [];
			packageLoadError = 'Could not reach the server to load packages.';
		} finally {
			loadingPackages = false;
		}
	}
</script>

<div class="hp-tabs" data-testid="product-form-tabs" role="tablist">
	{#each tabs as tab (tab.id)}
		<button
			type="button"
			role="tab"
			aria-selected={active === tab.id}
			class="hp-tab"
			class:active={active === tab.id}
			onclick={() => (active = tab.id)}
		>
			{tab.label}
		</button>
	{/each}
</div>

<div class="hp-tabpanel" class:hidden={active === 'options'}>
	<form
		method="POST"
		{action}
		data-testid="product-form"
		use:enhance={() => {
			submitting = true;
			return async ({ result, update }) => {
				submitting = false;
				if (result.type === 'success') onSuccess?.();
				// reset:false - the fields are Svelte-bound $state, not native form
				// values, so a form reset would blank every input (they have no
				// `value` attribute to reset to) while the state keeps its value,
				// leaving the form visually empty until a full refresh.
				await update({ reset: false });
			};
		}}
	>
		{#if errorText}
			<div class="hp-alert-red" data-testid="product-form-error">
				<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{errorText}
			</div>
		{/if}

		<!-- Details tab -->
		<div class:hidden={active !== 'details'}>
			<div class="hp-formrow" data-testid="product-name-field">
				<label for="field-name">Product Name<span style="color:#d9534f"> *</span></label>
				<div class="hp-field">
					<input id="field-name" name="name" bind:value={name} required class="hp-input" />
				</div>
			</div>
			<div class="hp-formrow" data-testid="product-slug-field">
				<label for="field-slug">Slug<span style="color:#d9534f"> *</span></label>
				<div class="hp-field">
					<input
						id="field-slug"
						name="slug"
						bind:value={slug}
						required
						class="hp-input"
						oninput={() => (slugTouched = true)}
					/>
					<div class="hp-help" style="margin-top:3px">
						Lowercase letters, numbers and dashes. Used in the order URL.
					</div>
				</div>
			</div>
			<div class="hp-formrow" data-testid="product-group-field">
				<label for="field-group_id">Product Group<span style="color:#d9534f"> *</span></label>
				<div class="hp-field">
					<select
						id="field-group_id"
						name="group_id"
						bind:value={groupId}
						required
						class="hp-select"
					>
						<option value="" disabled>Select a group…</option>
						{#each groups as g (g.id)}
							<option value={String(g.id)}>{g.name}</option>
						{/each}
					</select>
				</div>
			</div>
			<div class="hp-formrow" data-testid="product-type-field">
				<label for="field-type">Product Type<span style="color:#d9534f"> *</span></label>
				<div class="hp-field">
					<select id="field-type" name="type" bind:value={type} required class="hp-select">
						{#each PRODUCT_TYPES as pt (pt)}
							<option value={pt}>{typeLabels[pt]}</option>
						{/each}
					</select>
				</div>
			</div>
			<div class="hp-formrow top">
				<label for="field-description">Description</label>
				<div class="hp-field" style="flex:1 1 520px">
					<textarea
						id="field-description"
						name="description"
						rows="5"
						bind:value={description}
						class="hp-textarea"></textarea>
				</div>
			</div>
			<div class="hp-formrow">
				<label for="field-sort">Sort Order</label>
				<div class="hp-field">
					<input id="field-sort" name="sort" type="number" bind:value={sort} class="hp-input" />
				</div>
			</div>
			<div class="hp-formrow" data-testid="product-welcome-template-field">
				<label for="field-welcome_email_template">Welcome Email Template</label>
				<div class="hp-field">
					{#if templateKeys.length > 0}
						<select
							id="field-welcome_email_template"
							name="welcome_email_template"
							bind:value={welcomeTemplate}
							class="hp-select"
						>
							<option value="">— No template —</option>
							{#each templateKeys as k (k)}
								<option value={k}>{k}</option>
							{/each}
						</select>
					{:else}
						<input
							id="field-welcome_email_template"
							name="welcome_email_template"
							bind:value={welcomeTemplate}
							class="hp-input"
						/>
					{/if}
					<div class="hp-help" style="margin-top:3px">
						Email template key sent after activation.
					</div>
				</div>
			</div>
			<div class="hp-formrow" data-testid="product-hidden-field">
				<label for="field-hidden">&nbsp;</label>
				<div class="hp-field" style="flex:1 1 auto">
					<label class="hp-checkline" style="padding:0">
						<input id="field-hidden" name="hidden" type="checkbox" bind:checked={hidden} />
						Hide from catalog
					</label>
				</div>
			</div>
		</div>

		<!-- Module tab -->
		<div class:hidden={active !== 'module'}>
			<div class="hp-formrow" data-testid="product-module-field">
				<label for="field-module">Module</label>
				<div class="hp-field">
					<select id="field-module" name="module" bind:value={module_} class="hp-select">
						<option value="none">No module (manual)</option>
						<option value="cpanel">cPanel/WHM</option>
						<option value="directadmin">DirectAdmin</option>
					</select>
				</div>
			</div>
			<div class="hp-formrow" data-testid="product-package-field">
				<label for="field-package_name">Panel Package Name</label>
				<div class="hp-field">
					{#if configurable}
						<input
							id="field-package_name"
							value="Auto-generated per service"
							disabled
							class="hp-input"
						/>
						<div class="hp-help" style="margin-top:3px">
							Configurable products manage their own per-service package automatically at
							provisioning time — this field is not used.
						</div>
					{:else}
						<div style="display:flex;gap:8px">
							<input
								id="field-package_name"
								name="package_name"
								bind:value={packageName}
								disabled={module_ === 'none'}
								class="hp-input"
								style="flex:1 1 auto"
							/>
							<button
								type="button"
								class="hp-btn"
								data-testid="product-package-load-button"
								disabled={module_ === 'none' || !serverGroupId || loadingPackages}
								onclick={loadPackages}
							>
								{loadingPackages ? 'Loading…' : 'Load from Server'}
							</button>
						</div>
						{#if availablePackages.length > 0}
							<select
								class="hp-select"
								style="margin-top:6px"
								data-testid="product-package-select"
								onchange={(e) => (packageName = e.currentTarget.value)}
							>
								<option value="" disabled selected>— Pick a package from the server —</option>
								{#each availablePackages as p (p)}
									<option value={p}>{p}</option>
								{/each}
							</select>
						{/if}
						{#if packageLoadError}
							<div
								class="hp-help"
								style="margin-top:3px;color:#c43c35"
								data-testid="product-package-load-error"
							>
								{packageLoadError}
							</div>
						{/if}
						<div class="hp-help" style="margin-top:3px">
							Package/plan name on the server (e.g. WHM package). Select a Server Group below, then
							"Load from Server" to pick from packages that already exist there instead of typing.
						</div>
					{/if}
				</div>
			</div>
			<div class="hp-formrow" data-testid="product-server-group-field">
				<label for="field-server_group_id">Server Group</label>
				<div class="hp-field">
					<select
						id="field-server_group_id"
						name="server_group_id"
						bind:value={serverGroupId}
						disabled={module_ === 'none'}
						class="hp-select"
					>
						<option value="">— No server group —</option>
						{#each serverGroups as g (g.id)}
							<option value={String(g.id)}>{g.name}</option>
						{/each}
					</select>
					<div class="hp-help" style="margin-top:3px">
						A server is picked from this group during provisioning.
					</div>
				</div>
			</div>
			<div class="hp-formrow" data-testid="product-configurable-field">
				<label for="field-configurable">Configurable</label>
				<div class="hp-field">
					<label class="hp-checkline" style="padding:0">
						<input
							id="field-configurable"
							name="configurable"
							type="checkbox"
							bind:checked={configurable}
							disabled={module_ === 'none'}
						/>
						Customer sets their own specs (dynamic product)
					</label>
					<div class="hp-help" style="margin-top:3px">
						When on, define spec knobs + per-unit pricing in the Dynamic Specs section below. A
						per-service package is created on the panel at provisioning time.
					</div>
				</div>
			</div>
			{#if configurable}
				<div class="hp-formrow" data-testid="product-shell-cgi-field">
					<label for="field-shell_access">Shell / CGI Access</label>
					<div class="hp-field">
						<label class="hp-checkline" style="padding:0">
							<input
								id="field-shell_access"
								name="shell_access"
								type="checkbox"
								bind:checked={shellAccess}
							/>
							Shell Access
						</label>
						<label class="hp-checkline" style="padding:0">
							<input
								id="field-cgi_access"
								name="cgi_access"
								type="checkbox"
								bind:checked={cgiAccess}
							/>
							CGI Access
						</label>
					</div>
				</div>
				{#if module_ === 'cpanel'}
					<div class="hp-formrow" data-testid="product-feature-list-field">
						<label for="field-feature_list">WHM Feature List</label>
						<div class="hp-field">
							<input
								id="field-feature_list"
								name="feature_list"
								bind:value={featureList}
								class="hp-input"
							/>
							<div class="hp-help" style="margin-top:3px">
								Name of an existing WHM Feature List you manage directly in WHM (blank = WHM's
								"default").
							</div>
						</div>
					</div>
				{:else if module_ === 'directadmin'}
					<div class="hp-formrow" data-testid="product-template-package-field">
						<label for="field-template_package">Template Package</label>
						<div class="hp-field">
							<div style="display:flex;gap:8px">
								<input
									id="field-template_package"
									name="template_package"
									bind:value={templatePackage}
									class="hp-input"
									style="flex:1 1 auto"
								/>
								<button
									type="button"
									class="hp-btn"
									data-testid="product-template-package-load-button"
									disabled={!serverGroupId || loadingPackages}
									onclick={loadPackages}
								>
									{loadingPackages ? 'Loading…' : 'Load from Server'}
								</button>
							</div>
							{#if availablePackages.length > 0}
								<select
									class="hp-select"
									style="margin-top:6px"
									data-testid="product-template-package-select"
									onchange={(e) => (templatePackage = e.currentTarget.value)}
								>
									<option value="" disabled selected>— Pick a package to clone from —</option>
									{#each availablePackages as p (p)}
										<option value={p}>{p}</option>
									{/each}
								</select>
							{/if}
							<div class="hp-help" style="margin-top:3px">
								An existing DirectAdmin package whose Git/WordPress/ClamAV/etc. settings are cloned
								into this product's per-service package at provisioning time.
							</div>
						</div>
					</div>
				{/if}
			{/if}
			<div class="hp-formrow top" data-testid="product-auto-setup-field">
				<label for="field-auto_setup">Automatic Setup</label>
				<div class="hp-field">
					{#each AUTO_SETUPS as setup, i (setup)}
						<label class="hp-checkline" style="padding:4px 0">
							<input
								id={i === 0 ? 'field-auto_setup' : undefined}
								type="radio"
								name="auto_setup"
								value={setup}
								bind:group={autoSetup}
							/>
							{autoSetupLabels[setup]}
						</label>
					{/each}
				</div>
			</div>
		</div>

		<!-- Pricing tab -->
		<div class:hidden={active !== 'pricing'}>
			<p class="hp-help" style="margin-bottom:12px">
				Tick the available cycles and enter prices in Rupiah (no decimals).
			</p>
			<div class="hp-scroll">
				<table class="hp-table" data-testid="product-pricing-table">
					<thead>
						<tr>
							<th class="c" style="width:70px">Enabled</th>
							<th>Billing Cycle</th>
							<th style="width:160px">Price (Rp)</th>
							<th style="width:160px">Setup Fee (Rp)</th>
						</tr>
					</thead>
					<tbody>
						{#each CYCLES as cycle (cycle)}
							<tr>
								<td class="c">
									<input
										type="checkbox"
										name={`cycle_${cycle}`}
										bind:checked={cycleState[cycle].enabled}
										data-testid={`cycle-${cycle}-enabled`}
									/>
								</td>
								<td style="font-weight:600;color:#444">{cycleLabels[cycle]}</td>
								<td>
									<input
										type="number"
										name={`price_${cycle}`}
										min="0"
										step="1"
										bind:value={cycleState[cycle].price}
										disabled={!cycleState[cycle].enabled}
										data-testid={`price-${cycle}-input`}
										class="hp-input"
									/>
								</td>
								<td>
									<input
										type="number"
										name={`setup_${cycle}`}
										min="0"
										step="1"
										bind:value={cycleState[cycle].setup}
										disabled={!cycleState[cycle].enabled}
										data-testid={`setup-${cycle}-input`}
										class="hp-input"
									/>
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>

			<div style="margin-top:16px;border-top:1px solid #eee;padding-top:14px">
				<div class="hp-formrow" data-testid="product-stock-enabled-field">
					<label for="field-stock_enabled">&nbsp;</label>
					<div class="hp-field" style="flex:1 1 auto">
						<label class="hp-checkline" style="padding:0">
							<input
								id="field-stock_enabled"
								name="stock_enabled"
								type="checkbox"
								bind:checked={stockEnabled}
							/>
							Enable stock control
						</label>
					</div>
				</div>
				{#if stockEnabled}
					<div class="hp-formrow" data-testid="product-stock-qty-field">
						<label for="field-stock_qty">Stock Quantity</label>
						<div class="hp-field">
							<input
								id="field-stock_qty"
								name="stock_qty"
								type="number"
								bind:value={stockQty}
								class="hp-input"
							/>
						</div>
					</div>
				{/if}
			</div>
		</div>

		<div class="hp-form-actions">
			<span data-testid="product-form-submit">
				<button type="submit" class="hp-btn hp-btn-primary" disabled={submitting}>
					{submitLabel}
				</button>
			</span>
		</div>
	</form>
</div>

{#if optionsTab}
	<div class="hp-tabpanel" class:hidden={active !== 'options'}>
		{@render optionsTab()}
	</div>
{/if}

<style>
	.hidden {
		display: none;
	}
</style>
