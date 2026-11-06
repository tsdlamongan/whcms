<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import FormField from '$lib/components/FormField.svelte';
	import LoadingButton from '$lib/components/LoadingButton.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import type { SelectOption } from '$lib/components/types';
	import { formatIDR } from '$lib/money';
	import { toast } from '$lib/stores/toast.svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	type TLDRow = (typeof data.tldPricing)[number];
	type PremiumRow = (typeof data.premiumPricing)[number];
	type LengthTierRow = (typeof data.lengthPricing)[number];

	const YEARS = Array.from({ length: 10 }, (_, i) => i + 1);
	const idr = (n: number) => formatIDR(n);

	const registrarNames = $derived(new Map(data.registrars.map((r) => [r.id, r.name])));
	function registrarName(id: number): string {
		return registrarNames.get(id) ?? `#${id}`;
	}
	const registrarOptions = $derived<SelectOption[]>(
		data.registrars.map((r) => ({ value: String(r.id), label: r.name }))
	);

	// tabs
	interface TabDef {
		id: string;
		label: string;
	}
	let active = $state('tld');
	const tabs: TabDef[] = [
		{ id: 'tld', label: 'TLD Pricing' },
		{ id: 'premium', label: 'Premium Domain Pricing' },
		{ id: 'length', label: 'Premium Length Pricing' }
	];

	// TLD pricing: create/edit modal
	let tldModalOpen = $state(false);
	let editingTld = $state<TLDRow | null>(null);
	let tldSaving = $state(false);

	let fTld = $state('');
	let fRegistrarId = $state('');
	let fActive = $state(true);
	let fMinYears = $state(1);
	let fMaxYears = $state(10);
	let fTransferPrice = $state(0);
	let fRestorePrice = $state(0);
	let fRegisterPrices = $state<Record<number, number>>({});
	let fRenewPrices = $state<Record<number, number>>({});

	function openCreateTld() {
		editingTld = null;
		fTld = '';
		fRegistrarId = data.registrars[0] ? String(data.registrars[0].id) : '';
		fActive = true;
		fMinYears = 1;
		fMaxYears = 10;
		fTransferPrice = 0;
		fRestorePrice = 0;
		fRegisterPrices = {};
		fRenewPrices = {};
		tldModalOpen = true;
	}

	function openEditTld(row: TLDRow) {
		editingTld = row;
		fTld = row.tld;
		fRegistrarId = String(row.registrar_id);
		fActive = row.active;
		fMinYears = row.min_years;
		fMaxYears = row.max_years;
		fTransferPrice = row.transfer_price;
		fRestorePrice = row.restore_price;
		// A year absent from register_prices/renew_prices means "no override
		// configured" (falls back to the registrar's live quote) - leave its
		// input blank, not 0, so a genuinely-typed 0 (free-domain promo) stays
		// distinguishable from "never configured" on save.
		const registerPrices: Record<number, number> = {};
		const renewPrices: Record<number, number> = {};
		for (const y of YEARS) {
			const reg = row.register_prices[String(y)];
			if (reg !== undefined) registerPrices[y] = reg;
			const renew = row.renew_prices[String(y)];
			if (renew !== undefined) renewPrices[y] = renew;
		}
		fRegisterPrices = registerPrices;
		fRenewPrices = renewPrices;
		tldModalOpen = true;
	}

	const tldModalError = $derived(
		tldModalOpen && form?.op === 'tld-save' && !form?.success ? (form?.errorMessage ?? null) : null
	);
	const tldDeleteError = $derived(
		form?.op === 'tld-delete' && !form?.success ? (form?.errorMessage ?? null) : null
	);

	// TLD pricing: delete confirm
	let tldConfirmOpen = $state(false);
	let tldDeleting = $state(false);
	let tldDeleteTarget = $state<TLDRow | null>(null);
	let tldDeleteFormEl = $state<HTMLFormElement | null>(null);

	function askDeleteTld(row: TLDRow) {
		tldDeleteTarget = row;
		tldConfirmOpen = true;
	}

	// TLD pricing: import from registrar
	interface RegistrarCatalogRow {
		tld: string;
		currency: string;
		register_prices: Record<string, number>;
		renew_prices: Record<string, number>;
		transfer_price: number;
		restore_price: number;
		already_configured: boolean;
	}

	let importModalOpen = $state(false);
	let importLoading = $state(false);
	let importError = $state<string | null>(null);
	let importCatalog = $state<RegistrarCatalogRow[]>([]);
	let importSearch = $state('');
	let importMarkupPercent = $state(0);
	let importRegistrarId = $state('');
	let importSelected = $state<Set<string>>(new Set());
	let importSubmitting = $state(false);

	const importFilteredCatalog = $derived(
		importCatalog.filter((row) => row.tld.includes(importSearch.trim().toLowerCase()))
	);
	const importSelectableCatalog = $derived(
		importFilteredCatalog.filter((row) => !row.already_configured)
	);
	const importSelectedCount = $derived(importSelected.size);
	const importAllSelected = $derived(
		importSelectableCatalog.length > 0 &&
			importSelectableCatalog.every((row) => importSelected.has(row.tld))
	);

	function importSellPrice(cost: number): number {
		return Math.round(cost * (1 + importMarkupPercent / 100));
	}

	function toggleImportTld(row: RegistrarCatalogRow) {
		if (row.already_configured) return;
		const next = new Set(importSelected);
		if (next.has(row.tld)) next.delete(row.tld);
		else next.add(row.tld);
		importSelected = next;
	}

	function toggleSelectAllImport() {
		if (importAllSelected) {
			importSelected = new Set();
			return;
		}
		importSelected = new Set(importSelectableCatalog.map((row) => row.tld));
	}

	async function loadImportCatalog() {
		if (!importRegistrarId) {
			importCatalog = [];
			return;
		}
		importLoading = true;
		importError = null;
		importSelected = new Set();
		try {
			const res = await fetch(
				`/admin/domains/registrar-catalog?registrar_id=${encodeURIComponent(importRegistrarId)}`
			);
			const body = (await res.json()) as {
				ok: boolean;
				message?: string;
				items?: RegistrarCatalogRow[];
			};
			if (!body.ok) {
				importError = body.message ?? 'Failed to load the registrar catalog.';
				importCatalog = [];
			} else {
				importCatalog = body.items ?? [];
			}
		} catch {
			importError = 'Failed to load the registrar catalog.';
			importCatalog = [];
		} finally {
			importLoading = false;
		}
	}

	// Reloads the catalog whenever the modal opens or the admin picks a
	// different registrar source - importLoadedFor is reset on every open so
	// re-opening with the same registrar still fetches fresh prices (avoids
	// showing a stale catalog snapshot from an earlier session).
	let importLoadedFor = $state('');
	$effect(() => {
		if (!importModalOpen || !importRegistrarId || importRegistrarId === importLoadedFor) return;
		importLoadedFor = importRegistrarId;
		void loadImportCatalog();
	});

	function openImportModal() {
		importRegistrarId = data.registrars[0] ? String(data.registrars[0].id) : '';
		importLoadedFor = '';
		importSearch = '';
		importMarkupPercent = 0;
		importSelected = new Set();
		importCatalog = [];
		importModalOpen = true;
	}

	const importModalError = $derived(
		importModalOpen && form?.op === 'tld-import' && !form?.success
			? (form?.errorMessage ?? null)
			: null
	);

	// Premium domain pricing: create/edit modal
	let premiumModalOpen = $state(false);
	let editingPremium = $state<PremiumRow | null>(null);
	let premiumSaving = $state(false);

	let fDomainName = $state('');
	let fPremiumRegisterPrice = $state(0);
	let fPremiumRenewPrice = $state(0);
	let fPremiumTransferPrice = $state(0);

	function openCreatePremium() {
		editingPremium = null;
		fDomainName = '';
		fPremiumRegisterPrice = 0;
		fPremiumRenewPrice = 0;
		fPremiumTransferPrice = 0;
		premiumModalOpen = true;
	}

	function openEditPremium(row: PremiumRow) {
		editingPremium = row;
		fDomainName = row.domain_name;
		fPremiumRegisterPrice = row.register_price;
		fPremiumRenewPrice = row.renew_price;
		fPremiumTransferPrice = row.transfer_price;
		premiumModalOpen = true;
	}

	const premiumModalError = $derived(
		premiumModalOpen && form?.op === 'premium-save' && !form?.success
			? (form?.errorMessage ?? null)
			: null
	);
	const premiumDeleteError = $derived(
		form?.op === 'premium-delete' && !form?.success ? (form?.errorMessage ?? null) : null
	);

	// Premium domain pricing: delete confirm
	let premiumConfirmOpen = $state(false);
	let premiumDeleting = $state(false);
	let premiumDeleteTarget = $state<PremiumRow | null>(null);
	let premiumDeleteFormEl = $state<HTMLFormElement | null>(null);

	function askDeletePremium(row: PremiumRow) {
		premiumDeleteTarget = row;
		premiumConfirmOpen = true;
	}

	// Premium length-tier pricing: create/edit modal
	let lengthModalOpen = $state(false);
	let editingLengthTier = $state<LengthTierRow | null>(null);
	let lengthSaving = $state(false);

	let fLengthTld = $state('');
	let fCharLength = $state(2);
	let fLengthPrice = $state(0);

	function openCreateLengthTier() {
		editingLengthTier = null;
		fLengthTld = '';
		fCharLength = 2;
		fLengthPrice = 0;
		lengthModalOpen = true;
	}

	function openEditLengthTier(row: LengthTierRow) {
		editingLengthTier = row;
		fLengthTld = row.tld;
		fCharLength = row.char_length;
		fLengthPrice = row.price;
		lengthModalOpen = true;
	}

	const lengthModalError = $derived(
		lengthModalOpen && form?.op === 'length-save' && !form?.success
			? (form?.errorMessage ?? null)
			: null
	);
	const lengthDeleteError = $derived(
		form?.op === 'length-delete' && !form?.success ? (form?.errorMessage ?? null) : null
	);

	// Premium length-tier pricing: delete confirm
	let lengthConfirmOpen = $state(false);
	let lengthDeleting = $state(false);
	let lengthDeleteTarget = $state<LengthTierRow | null>(null);
	let lengthDeleteFormEl = $state<HTMLFormElement | null>(null);

	function askDeleteLengthTier(row: LengthTierRow) {
		lengthDeleteTarget = row;
		lengthConfirmOpen = true;
	}
</script>

<svelte:head>
	<title>Domain Pricing — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Domain Pricing</h1>

<p class="hp-lead">
	Configure per-TLD registration/renewal pricing and manually-curated premium domain overrides.
</p>

<div class="hp-tabs" data-testid="domain-pricing-tabs" role="tablist">
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

<!-- ============================== TLD Pricing ============================== -->
<div class="tabpanel" class:hidden={active !== 'tld'}>
	{#if data.tldListError}
		<div class="hp-alert-yellow" data-testid="tld-pricing-list-error">
			<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.tldListError}
		</div>
	{/if}
	{#if tldDeleteError}
		<div class="hp-alert-red" data-testid="tld-pricing-delete-error">
			<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{tldDeleteError}
		</div>
	{/if}

	<div class="hp-listbar">
		<div class="hp-count">{data.tldPricing.length} Records Found</div>
		<div style="display:flex;gap:8px">
			<span data-testid="tld-pricing-import-button">
				<button type="button" class="hp-btn" onclick={openImportModal}>
					<i class="fas fa-cloud-download-alt"></i>Import from Registrar
				</button>
			</span>
			<span data-testid="tld-pricing-create-button">
				<button type="button" class="hp-btn hp-btn-primary" onclick={openCreateTld}>
					<i class="fas fa-plus"></i>New TLD
				</button>
			</span>
		</div>
	</div>

	<div class="hp-scroll">
		<table class="hp-table">
			<thead>
				<tr>
					<th>TLD</th>
					<th>Registrar</th>
					<th class="c" style="width:90px">Status</th>
					<th class="c" style="width:100px">Years</th>
					<th class="r" style="width:140px">Register (1yr)</th>
					<th class="r" style="width:140px">Renew (1yr)</th>
					<th class="r" style="width:120px">Transfer</th>
					<th class="r" style="width:120px">Restore</th>
					<th style="width:160px"></th>
				</tr>
			</thead>
			<tbody>
				{#each data.tldPricing as row (row.id)}
					<tr class="hp-row">
						<td>
							<button
								type="button"
								class="hp-rowbtn"
								data-testid={`row-tld-pricing-${row.id}`}
								onclick={() => openEditTld(row)}
							>
								.{row.tld}
							</button>
						</td>
						<td style="color:#555">{registrarName(row.registrar_id)}</td>
						<td class="c">
							<span class={`hp-badge ${row.active ? 'active' : 'inactive'}`}>
								{row.active ? 'Active' : 'Inactive'}
							</span>
						</td>
						<td class="c" style="color:#666">{row.min_years}–{row.max_years}</td>
						<td class="r">{idr(row.register_prices['1'] ?? 0)}</td>
						<td class="r">{idr(row.renew_prices['1'] ?? 0)}</td>
						<td class="r">{idr(row.transfer_price)}</td>
						<td class="r">{idr(row.restore_price)}</td>
						<td style="white-space:nowrap">
							<button
								type="button"
								class="hp-btn"
								style="padding:5px 10px"
								data-testid={`tld-pricing-edit-${row.id}`}
								onclick={() => openEditTld(row)}
							>
								Edit
							</button>
							<button
								type="button"
								class="hp-btn hp-btn-danger"
								style="padding:5px 10px"
								data-testid={`tld-pricing-delete-${row.id}`}
								onclick={() => askDeleteTld(row)}
							>
								Delete
							</button>
						</td>
					</tr>
				{:else}
					<tr
						><td colspan="9" style="text-align:center;padding:28px;color:#999"
							>No TLD pricing configured</td
						></tr
					>
				{/each}
			</tbody>
		</table>
	</div>
</div>

<!-- ======================== Premium Domain Pricing =========================== -->
<div class="tabpanel" class:hidden={active !== 'premium'}>
	{#if data.premiumListError}
		<div class="hp-alert-yellow" data-testid="premium-pricing-list-error">
			<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.premiumListError}
		</div>
	{/if}
	{#if premiumDeleteError}
		<div class="hp-alert-red" data-testid="premium-pricing-delete-error">
			<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{premiumDeleteError}
		</div>
	{/if}

	<div class="hp-listbar">
		<div class="hp-count">{data.premiumPricing.length} Records Found</div>
		<span data-testid="premium-pricing-create-button">
			<button type="button" class="hp-btn hp-btn-primary" onclick={openCreatePremium}>
				<i class="fas fa-plus"></i>New Premium Domain
			</button>
		</span>
	</div>

	<div class="hp-scroll">
		<table class="hp-table">
			<thead>
				<tr>
					<th>Domain</th>
					<th class="r" style="width:160px">Register</th>
					<th class="r" style="width:160px">Renew</th>
					<th class="r" style="width:160px">Transfer</th>
					<th style="width:160px"></th>
				</tr>
			</thead>
			<tbody>
				{#each data.premiumPricing as row (row.id)}
					<tr class="hp-row">
						<td>
							<button
								type="button"
								class="hp-rowbtn"
								data-testid={`row-premium-pricing-${row.id}`}
								onclick={() => openEditPremium(row)}
							>
								{row.domain_name}
							</button>
						</td>
						<td class="r">{idr(row.register_price)}</td>
						<td class="r">{idr(row.renew_price)}</td>
						<td class="r">{idr(row.transfer_price)}</td>
						<td style="white-space:nowrap">
							<button
								type="button"
								class="hp-btn"
								style="padding:5px 10px"
								data-testid={`premium-pricing-edit-${row.id}`}
								onclick={() => openEditPremium(row)}
							>
								Edit
							</button>
							<button
								type="button"
								class="hp-btn hp-btn-danger"
								style="padding:5px 10px"
								data-testid={`premium-pricing-delete-${row.id}`}
								onclick={() => askDeletePremium(row)}
							>
								Delete
							</button>
						</td>
					</tr>
				{:else}
					<tr
						><td colspan="5" style="text-align:center;padding:28px;color:#999"
							>No premium domain pricing configured</td
						></tr
					>
				{/each}
			</tbody>
		</table>
	</div>
</div>

<!-- ======================== Premium Length Pricing ============================ -->
<div class="tabpanel" class:hidden={active !== 'length'}>
	<p class="hp-help" style="margin:0 0 12px">
		Premium price tiers by TLD + registrable-label character count (e.g. Dewabiz's "Limited
		Character" table: .id 2-char/3-char/4-char each carry a distinct price). One flat price covers
		register, renew and transfer for the tier.
	</p>

	{#if data.lengthListError}
		<div class="hp-alert-yellow" data-testid="length-pricing-list-error">
			<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.lengthListError}
		</div>
	{/if}
	{#if lengthDeleteError}
		<div class="hp-alert-red" data-testid="length-pricing-delete-error">
			<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{lengthDeleteError}
		</div>
	{/if}

	<div class="hp-listbar">
		<div class="hp-count">{data.lengthPricing.length} Records Found</div>
		<span data-testid="length-pricing-create-button">
			<button type="button" class="hp-btn hp-btn-primary" onclick={openCreateLengthTier}>
				<i class="fas fa-plus"></i>New Length Tier
			</button>
		</span>
	</div>

	<div class="hp-scroll">
		<table class="hp-table">
			<thead>
				<tr>
					<th>TLD</th>
					<th class="c" style="width:140px">Character Length</th>
					<th class="r" style="width:160px">Price</th>
					<th style="width:160px"></th>
				</tr>
			</thead>
			<tbody>
				{#each data.lengthPricing as row (row.id)}
					<tr class="hp-row">
						<td>
							<button
								type="button"
								class="hp-rowbtn"
								data-testid={`row-length-pricing-${row.id}`}
								onclick={() => openEditLengthTier(row)}
							>
								.{row.tld}
							</button>
						</td>
						<td class="c" style="color:#666">{row.char_length}</td>
						<td class="r">{idr(row.price)}</td>
						<td style="white-space:nowrap">
							<button
								type="button"
								class="hp-btn"
								style="padding:5px 10px"
								data-testid={`length-pricing-edit-${row.id}`}
								onclick={() => openEditLengthTier(row)}
							>
								Edit
							</button>
							<button
								type="button"
								class="hp-btn hp-btn-danger"
								style="padding:5px 10px"
								data-testid={`length-pricing-delete-${row.id}`}
								onclick={() => askDeleteLengthTier(row)}
							>
								Delete
							</button>
						</td>
					</tr>
				{:else}
					<tr
						><td colspan="4" style="text-align:center;padding:28px;color:#999"
							>No premium length-tier pricing configured</td
						></tr
					>
				{/each}
			</tbody>
		</table>
	</div>
</div>

<!-- ============================ Import from Registrar modal ============================ -->
<Modal bind:open={importModalOpen} title="Import from Registrar" size="xl">
	<form
		method="POST"
		action="?/importTld"
		data-testid="tld-pricing-import-form"
		use:enhance={() => {
			importSubmitting = true;
			return async ({ result, update }) => {
				importSubmitting = false;
				if (result.type === 'success') {
					importModalOpen = false;
					const importedCount = (result.data?.imported as string[] | undefined)?.length ?? 0;
					const skippedCount = (result.data?.skipped as string[] | undefined)?.length ?? 0;
					toast.success(`Imported ${importedCount} TLD(s), skipped ${skippedCount}.`);
				}
				await update();
			};
		}}
	>
		<input type="hidden" name="registrar_id" value={importRegistrarId} />
		{#each Array.from(importSelected) as tld (tld)}
			<input type="hidden" name="tlds" value={tld} />
		{/each}

		{#if importModalError}
			<div class="hp-alert-red" style="margin-bottom:14px" data-testid="tld-pricing-import-error">
				{importModalError}
			</div>
		{/if}
		{#if importError}
			<div
				class="hp-alert-yellow"
				style="margin-bottom:14px"
				data-testid="tld-pricing-import-load-error"
			>
				<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{importError}
			</div>
		{/if}

		<div class="grid gap-x-4 sm:grid-cols-3" style="margin-bottom:10px">
			<FormField
				label="Registrar Source"
				name="_registrar_select"
				type="select"
				bind:value={importRegistrarId}
				options={registrarOptions}
				required
			/>
			<FormField
				label="Search TLD"
				name="search"
				bind:value={importSearch}
				placeholder="e.g. co.id"
			/>
			<FormField
				label="Markup %"
				name="markup_percent"
				type="number"
				bind:value={importMarkupPercent}
				hint="Applied to the registrar's cost for register/renew/transfer/restore."
			/>
		</div>

		<div class="hp-listbar" style="padding:0 0 8px">
			<span class="hp-help">{importSelectedCount} selected</span>
			<button
				type="button"
				class="hp-btn"
				style="padding:5px 10px"
				disabled={importSelectableCatalog.length === 0}
				onclick={toggleSelectAllImport}
				data-testid="tld-pricing-import-select-all"
			>
				{importAllSelected ? 'Deselect All' : 'Select All'}
			</button>
		</div>

		<div class="hp-scroll" style="max-height:420px">
			<table class="hp-table" data-testid="tld-pricing-import-catalog">
				<thead>
					<tr>
						<th class="c" style="width:36px">
							<input
								type="checkbox"
								checked={importAllSelected}
								disabled={importSelectableCatalog.length === 0}
								onchange={toggleSelectAllImport}
								aria-label="Select all"
							/>
						</th>
						<th>TLD</th>
						<th class="r" style="width:140px">Cost (1yr)</th>
						<th class="r" style="width:140px">Sell (1yr)</th>
						<th style="width:140px"></th>
					</tr>
				</thead>
				<tbody>
					{#if importLoading}
						<tr
							><td colspan="5" style="text-align:center;padding:28px;color:#999"
								>Loading registrar catalog…</td
							></tr
						>
					{:else}
						{#each importFilteredCatalog as row (row.tld)}
							<tr class="hp-row">
								<td class="c">
									<input
										type="checkbox"
										checked={importSelected.has(row.tld)}
										disabled={row.already_configured}
										onchange={() => toggleImportTld(row)}
										data-testid={`tld-pricing-import-check-${row.tld}`}
									/>
								</td>
								<td>.{row.tld}</td>
								<td class="r">{idr(row.register_prices['1'] ?? 0)}</td>
								<td class="r">{idr(importSellPrice(row.register_prices['1'] ?? 0))}</td>
								<td>
									{#if row.already_configured}
										<span class="hp-badge inactive">Already configured</span>
									{/if}
								</td>
							</tr>
						{:else}
							<tr
								><td colspan="5" style="text-align:center;padding:28px;color:#999">No TLDs found</td
								></tr
							>
						{/each}
					{/if}
				</tbody>
			</table>
		</div>

		<div style="display:flex;justify-content:flex-end;gap:8px;margin-top:16px">
			<LoadingButton
				variant="secondary"
				onclick={() => (importModalOpen = false)}
				disabled={importSubmitting}
			>
				Cancel
			</LoadingButton>
			<span data-testid="tld-pricing-import-submit">
				<LoadingButton
					type="submit"
					loading={importSubmitting}
					disabled={importSelectedCount === 0}
				>
					Import {importSelectedCount} selected TLD{importSelectedCount === 1 ? '' : 's'}
				</LoadingButton>
			</span>
		</div>
	</form>
</Modal>

<!-- ============================ TLD create/edit modal ============================ -->
<Modal
	bind:open={tldModalOpen}
	title={editingTld ? 'Edit TLD Pricing' : 'Create TLD Pricing'}
	size="xl"
>
	<form
		method="POST"
		action={editingTld ? '?/updateTld' : '?/createTld'}
		data-testid="tld-pricing-form"
		use:enhance={() => {
			tldSaving = true;
			return async ({ result, update }) => {
				tldSaving = false;
				if (result.type === 'success') {
					tldModalOpen = false;
					toast.success('TLD pricing saved successfully.');
				}
				await update();
			};
		}}
	>
		{#if editingTld}
			<input type="hidden" name="id" value={editingTld.id} />
		{/if}

		{#if tldModalError}
			<div class="hp-alert-red" style="margin-bottom:14px" data-testid="tld-pricing-form-error">
				{tldModalError}
			</div>
		{/if}

		<div class="grid gap-x-4 sm:grid-cols-2">
			<FormField label="TLD" name="tld" bind:value={fTld} placeholder="e.g. com" required />
			<FormField
				label="Registrar"
				name="registrar_id"
				type="select"
				bind:value={fRegistrarId}
				options={registrarOptions}
				required
			/>
		</div>
		<div class="grid gap-x-4 sm:grid-cols-2">
			<FormField label="Min Years" name="min_years" type="number" bind:value={fMinYears} required />
			<FormField label="Max Years" name="max_years" type="number" bind:value={fMaxYears} required />
		</div>
		<div class="grid gap-x-4 sm:grid-cols-2">
			<FormField
				label="Transfer Price (Rp)"
				name="transfer_price"
				type="number"
				bind:value={fTransferPrice}
			/>
			<FormField
				label="Restore Price (Rp)"
				name="restore_price"
				type="number"
				bind:value={fRestorePrice}
				hint="Redemption fee for reactivating an expired domain (RDash/Dewabiz's 'redemption' fee)."
			/>
		</div>
		<FormField label="Active" name="active" type="checkbox" bind:value={fActive} />

		<p class="hp-help" style="margin:6px 0 10px">
			Per-year prices in Rupiah (whole numbers). Leave a year blank or 0 to skip it.
		</p>
		<div class="hp-scroll">
			<table class="hp-table" data-testid="tld-pricing-year-grid" style="min-width:1240px">
				<thead>
					<tr>
						<th style="min-width:110px">Price</th>
						{#each YEARS as y (y)}
							<th class="c" style="width:110px">{y}yr</th>
						{/each}
					</tr>
				</thead>
				<tbody>
					<tr>
						<td style="font-weight:600;color:#444">Register (Rp)</td>
						{#each YEARS as y (y)}
							<td>
								<input
									type="number"
									min="0"
									step="1"
									class="hp-input"
									style="min-width:96px"
									name={`register_price_${y}`}
									bind:value={fRegisterPrices[y]}
									data-testid={`register-price-${y}-input`}
								/>
							</td>
						{/each}
					</tr>
					<tr>
						<td style="font-weight:600;color:#444">Renew (Rp)</td>
						{#each YEARS as y (y)}
							<td>
								<input
									type="number"
									min="0"
									step="1"
									class="hp-input"
									style="min-width:96px"
									name={`renew_price_${y}`}
									bind:value={fRenewPrices[y]}
									data-testid={`renew-price-${y}-input`}
								/>
							</td>
						{/each}
					</tr>
				</tbody>
			</table>
		</div>

		<div class="mt-5 flex justify-end gap-2">
			<LoadingButton
				variant="secondary"
				onclick={() => (tldModalOpen = false)}
				disabled={tldSaving}
			>
				Cancel
			</LoadingButton>
			<span data-testid="tld-pricing-form-submit">
				<LoadingButton type="submit" loading={tldSaving}>Save</LoadingButton>
			</span>
		</div>
	</form>
</Modal>

<!-- TLD delete confirm -->
<form
	method="POST"
	action="?/deleteTld"
	class="hidden"
	bind:this={tldDeleteFormEl}
	use:enhance={() => {
		tldDeleting = true;
		return async ({ result, update }) => {
			tldDeleting = false;
			tldConfirmOpen = false;
			if (result.type === 'success') toast.success('TLD pricing deleted successfully.');
			await update();
		};
	}}
>
	<input type="hidden" name="id" value={tldDeleteTarget?.id ?? ''} />
</form>

<ConfirmDialog
	bind:open={tldConfirmOpen}
	danger
	loading={tldDeleting}
	title="Delete"
	message={`Delete pricing for ".${tldDeleteTarget?.tld ?? ''}"? Clients will no longer be able to register or renew this TLD.`}
	confirmLabel="Delete"
	onConfirm={() => tldDeleteFormEl?.requestSubmit()}
/>

<!-- ========================= Premium create/edit modal ========================= -->
<Modal
	bind:open={premiumModalOpen}
	title={editingPremium ? 'Edit Premium Domain Pricing' : 'Create Premium Domain Pricing'}
	size="lg"
>
	<form
		method="POST"
		action={editingPremium ? '?/updatePremium' : '?/createPremium'}
		data-testid="premium-pricing-form"
		use:enhance={() => {
			premiumSaving = true;
			return async ({ result, update }) => {
				premiumSaving = false;
				if (result.type === 'success') {
					premiumModalOpen = false;
					toast.success('Premium domain pricing saved successfully.');
				}
				await update();
			};
		}}
	>
		{#if editingPremium}
			<input type="hidden" name="id" value={editingPremium.id} />
		{/if}

		{#if premiumModalError}
			<div class="hp-alert-red" style="margin-bottom:14px" data-testid="premium-pricing-form-error">
				{premiumModalError}
			</div>
		{/if}

		<FormField
			label="Domain Name"
			name="domain_name"
			bind:value={fDomainName}
			placeholder="e.g. shop.id"
			required
		/>
		<div class="grid gap-x-4 sm:grid-cols-3">
			<FormField
				label="Register Price (Rp)"
				name="register_price"
				type="number"
				bind:value={fPremiumRegisterPrice}
			/>
			<FormField
				label="Renew Price (Rp)"
				name="renew_price"
				type="number"
				bind:value={fPremiumRenewPrice}
			/>
			<FormField
				label="Transfer Price (Rp)"
				name="transfer_price"
				type="number"
				bind:value={fPremiumTransferPrice}
			/>
		</div>

		<div class="mt-5 flex justify-end gap-2">
			<LoadingButton
				variant="secondary"
				onclick={() => (premiumModalOpen = false)}
				disabled={premiumSaving}
			>
				Cancel
			</LoadingButton>
			<span data-testid="premium-pricing-form-submit">
				<LoadingButton type="submit" loading={premiumSaving}>Save</LoadingButton>
			</span>
		</div>
	</form>
</Modal>

<!-- Premium delete confirm -->
<form
	method="POST"
	action="?/deletePremium"
	class="hidden"
	bind:this={premiumDeleteFormEl}
	use:enhance={() => {
		premiumDeleting = true;
		return async ({ result, update }) => {
			premiumDeleting = false;
			premiumConfirmOpen = false;
			if (result.type === 'success') toast.success('Premium domain pricing deleted successfully.');
			await update();
		};
	}}
>
	<input type="hidden" name="id" value={premiumDeleteTarget?.id ?? ''} />
</form>

<ConfirmDialog
	bind:open={premiumConfirmOpen}
	danger
	loading={premiumDeleting}
	title="Delete"
	message={`Delete premium pricing for "${premiumDeleteTarget?.domain_name ?? ''}"? It will fall back to standard TLD pricing.`}
	confirmLabel="Delete"
	onConfirm={() => premiumDeleteFormEl?.requestSubmit()}
/>

<!-- ===================== Premium length-tier create/edit modal ==================== -->
<Modal
	bind:open={lengthModalOpen}
	title={editingLengthTier ? 'Edit Premium Length Pricing' : 'Create Premium Length Pricing'}
	size="lg"
>
	<form
		method="POST"
		action={editingLengthTier ? '?/updateLengthTier' : '?/createLengthTier'}
		data-testid="length-pricing-form"
		use:enhance={() => {
			lengthSaving = true;
			return async ({ result, update }) => {
				lengthSaving = false;
				if (result.type === 'success') {
					lengthModalOpen = false;
					toast.success('Premium length pricing saved successfully.');
				}
				await update();
			};
		}}
	>
		{#if editingLengthTier}
			<input type="hidden" name="id" value={editingLengthTier.id} />
		{/if}

		{#if lengthModalError}
			<div class="hp-alert-red" style="margin-bottom:14px" data-testid="length-pricing-form-error">
				{lengthModalError}
			</div>
		{/if}

		<div class="grid gap-x-4 sm:grid-cols-3">
			<FormField label="TLD" name="tld" bind:value={fLengthTld} placeholder="e.g. id" required />
			<FormField
				label="Character Length"
				name="char_length"
				type="number"
				bind:value={fCharLength}
				required
			/>
			<FormField label="Price (Rp)" name="price" type="number" bind:value={fLengthPrice} />
		</div>

		<div class="mt-5 flex justify-end gap-2">
			<LoadingButton
				variant="secondary"
				onclick={() => (lengthModalOpen = false)}
				disabled={lengthSaving}
			>
				Cancel
			</LoadingButton>
			<span data-testid="length-pricing-form-submit">
				<LoadingButton type="submit" loading={lengthSaving}>Save</LoadingButton>
			</span>
		</div>
	</form>
</Modal>

<!-- Premium length-tier delete confirm -->
<form
	method="POST"
	action="?/deleteLengthTier"
	class="hidden"
	bind:this={lengthDeleteFormEl}
	use:enhance={() => {
		lengthDeleting = true;
		return async ({ result, update }) => {
			lengthDeleting = false;
			lengthConfirmOpen = false;
			if (result.type === 'success') toast.success('Premium length pricing deleted successfully.');
			await update();
		};
	}}
>
	<input type="hidden" name="id" value={lengthDeleteTarget?.id ?? ''} />
</form>

<ConfirmDialog
	bind:open={lengthConfirmOpen}
	danger
	loading={lengthDeleting}
	title="Delete"
	message={`Delete the ${lengthDeleteTarget?.char_length ?? ''}-character pricing tier for ".${lengthDeleteTarget?.tld ?? ''}"?`}
	confirmLabel="Delete"
	onConfirm={() => lengthDeleteFormEl?.requestSubmit()}
/>

<style>
	.hp-rowbtn {
		background: none;
		border: none;
		padding: 0;
		font: inherit;
		font-weight: 600;
		color: var(--hp-link, #337ab7);
		cursor: pointer;
	}
	.hp-rowbtn:hover {
		text-decoration: underline;
	}
	.hidden {
		display: none;
	}
	.tabpanel {
		padding-top: 16px;
	}
</style>
