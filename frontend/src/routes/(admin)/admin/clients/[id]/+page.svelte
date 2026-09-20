<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import DateText from '$lib/components/DateText.svelte';
	import FormField from '$lib/components/FormField.svelte';
	import HpPager from '$lib/components/hp/HpPager.svelte';
	import LoadingButton from '$lib/components/LoadingButton.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import MoneyText from '$lib/components/MoneyText.svelte';
	import { toast } from '$lib/stores/toast.svelte';
	import { untrack } from 'svelte';
	import type { SubmitFunction } from '@sveltejs/kit';
	import type { PageProps } from './$types';
	import type { GenerateSelectedInvoicesResult } from './+page.server';

	let { data, form }: PageProps = $props();

	const client = $derived(data.client);
	const base = $derived(`/admin/clients/${data.clientId}`);
	const clientName = $derived(
		client ? [client.first_name, client.last_name].filter(Boolean).join(' ') || data.email : ''
	);

	const tabs = [
		{ id: 'summary', label: 'Summary' },
		{ id: 'services', label: 'Services' },
		{ id: 'domains', label: 'Domains' },
		{ id: 'invoices', label: 'Invoices' },
		{ id: 'tickets', label: 'Tickets' },
		{ id: 'notes', label: 'Notes' },
		{ id: 'contacts', label: 'Contacts' }
	] as const;

	// profile form state
	let firstName = $state('');
	let lastName = $state('');
	let company = $state('');
	let address1 = $state('');
	let address2 = $state('');
	let city = $state('');
	let stateProv = $state('');
	let postcode = $state('');
	let country = $state('ID');
	let phone = $state('');
	let status = $state('active');
	let notesAdmin = $state('');

	function resetFormsFromClient() {
		const cl = data.client;
		firstName = cl?.first_name ?? '';
		lastName = cl?.last_name ?? '';
		company = cl?.company ?? '';
		address1 = cl?.address1 ?? '';
		address2 = cl?.address2 ?? '';
		city = cl?.city ?? '';
		stateProv = cl?.state ?? '';
		postcode = cl?.postcode ?? '';
		country = cl?.country || 'ID';
		phone = cl?.phone ?? '';
		status = cl?.status ?? 'active';
		notesAdmin = cl?.notes_admin ?? '';
		creditOpen = false;
		contactOpen = false;
		deleteOpen = false;
	}

	let profileSubmitting = $state(false);
	let notesSubmitting = $state(false);

	// credit modal
	let creditOpen = $state(false);
	let creditMode = $state('add');
	let creditAmount = $state(0);
	let creditReason = $state('');
	let creditSubmitting = $state(false);

	// contact modal / delete
	let contactOpen = $state(false);
	let contactEditId = $state<number | null>(null);
	let contactFirstName = $state('');
	let contactLastName = $state('');
	let contactEmail = $state('');
	let contactPhone = $state('');
	let contactSubmitting = $state(false);

	let deleteOpen = $state(false);
	let deleteContactId = $state<number | null>(null);
	let deleteSubmitting = $state(false);
	let deleteFormEl: HTMLFormElement | undefined = $state();

	// The component instance is reused when navigating between /admin/clients/[id] pages -
	// resync the editable form state whenever the client id changes (before paint).
	// Must come after every $state this reads/resets (creditOpen/contactOpen/deleteOpen
	// etc.) - $effect.pre's callback can run during the same init pass as this script,
	// and reading a `let` before its own declaration line throws a TDZ ReferenceError.
	$effect.pre(() => {
		void data.clientId;
		untrack(resetFormsFromClient);
	});

	function openContactCreate() {
		contactEditId = null;
		contactFirstName = '';
		contactLastName = '';
		contactEmail = '';
		contactPhone = '';
		contactOpen = true;
	}

	function openContactEdit(contact: NonNullable<typeof data.contacts>[number]) {
		contactEditId = contact.id;
		contactFirstName = contact.first_name;
		contactLastName = contact.last_name;
		contactEmail = contact.email;
		contactPhone = contact.phone;
		contactOpen = true;
	}

	function askDeleteContact(id: number) {
		deleteContactId = id;
		deleteOpen = true;
	}

	function makeEnhance(setBusy: (v: boolean) => void, onSuccess?: () => void): SubmitFunction {
		return () => {
			setBusy(true);
			return async ({ result, update }) => {
				setBusy(false);
				if (result.type === 'success') {
					toast.success('Changes saved successfully.');
					onSuccess?.();
				}
				await update({ reset: false });
			};
		};
	}

	// Known backend `errorKey`s this page's actions can return - mapped to the same
	// English copy the id/en i18n dictionaries used, since admin pages no longer call t().
	const ERROR_TEXT: Record<string, string> = {
		'admincore.clients.fillRequired': 'Fill in all required fields.',
		'admincore.clients.invalidAmount': 'Amount must be greater than 0.',
		'admincore.clients.reasonRequired': 'Reason is required.',
		'admincore.clients.saveFailed': 'Failed to save changes.'
	};

	const formError = $derived(
		form && 'errorKey' in form && form.errorKey
			? (ERROR_TEXT[form.errorKey] ?? form.errorKey)
			: form && 'errorMessage' in form && form.errorMessage
				? form.errorMessage
				: null
	);
	const profileFields = $derived<Record<string, string>>(
		form?.action === 'profile' && 'fields' in form && form.fields ? form.fields : {}
	);

	// tab list pagination (services/domains/invoices/tickets)
	const curPage = $derived(data.tabMeta?.page ?? data.listPage);
	const perPageV = $derived(data.tabMeta?.per_page ?? data.perPage);
	const totalRows = $derived(data.tabMeta?.total ?? 0);
	const fromRow = $derived(totalRows === 0 ? 0 : (curPage - 1) * perPageV + 1);
	const toRow = $derived(Math.min(curPage * perPageV, totalRows));

	const statusOptions = [
		{ value: 'active', label: 'Active' },
		{ value: 'inactive', label: 'Inactive' },
		{ value: 'closed', label: 'Closed' }
	];

	// status to hp-badge / hp-stext helpers
	function titleCase(s: string): string {
		return s.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
	}
	/** client.status: active|inactive|closed (CONTRACTS §4). */
	function clientBadgeClass(s: string): string {
		return s === 'active' ? 'active' : s === 'inactive' ? 'inactive' : 'cancelled';
	}
	/** service.status: pending|active|suspended|terminated|cancelled - matches hp-badge 1:1. */
	function serviceBadgeClass(s: string): string {
		return ['pending', 'active', 'suspended', 'terminated', 'cancelled'].includes(s)
			? s
			: 'cancelled';
	}
	/** domain.status: pending|active|pending_transfer|expired|cancelled. */
	function domainBadgeClass(s: string): string {
		if (s === 'pending_transfer') return 'pending';
		if (s === 'expired') return 'terminated';
		return ['pending', 'active', 'cancelled'].includes(s) ? s : 'cancelled';
	}
	/** invoice.status: draft|unpaid|paid|overdue|cancelled|refunded. */
	function invoiceStextClass(s: string): string {
		if (s === 'paid') return 'green';
		if (s === 'unpaid' || s === 'overdue') return 'red';
		return 'gray';
	}
	/** ticket.status: open|answered|customer_reply|on_hold|closed. */
	function ticketStextClass(s: string): string {
		if (s === 'open' || s === 'answered') return 'green';
		if (s === 'customer_reply' || s === 'on_hold') return 'orange';
		return 'gray';
	}

	// "Invoice Selected Items" (services/domains tabs)
	let selectedServices = $state<Set<number>>(new Set());
	let selectedDomains = $state<Set<number>>(new Set());
	let invoiceSelectedSubmitting = $state(false);

	const allServicesChecked = $derived(
		(data.services?.length ?? 0) > 0 &&
			(data.services ?? []).every((r) => selectedServices.has(r.id))
	);
	const allDomainsChecked = $derived(
		(data.domains?.length ?? 0) > 0 && (data.domains ?? []).every((r) => selectedDomains.has(r.id))
	);

	function toggleService(id: number) {
		const next = new Set(selectedServices);
		if (next.has(id)) next.delete(id);
		else next.add(id);
		selectedServices = next;
	}
	function toggleAllServices() {
		selectedServices = allServicesChecked
			? new Set()
			: new Set((data.services ?? []).map((r) => r.id));
	}
	function toggleDomain(id: number) {
		const next = new Set(selectedDomains);
		if (next.has(id)) next.delete(id);
		else next.add(id);
		selectedDomains = next;
	}
	function toggleAllDomains() {
		selectedDomains = allDomainsChecked
			? new Set()
			: new Set((data.domains ?? []).map((r) => r.id));
	}

	function invoiceSelectedSummary(result: GenerateSelectedInvoicesResult): string {
		const created = result.created ?? [];
		const skipped = result.skipped ?? [];
		const parts: string[] = [];
		if (created.length) parts.push(`${created.length} invoice(s) created`);
		if (skipped.length) parts.push(`${skipped.length} skipped (already invoiced)`);
		return parts.length ? parts.join(', ') : 'Nothing selected was eligible to invoice.';
	}

	function makeInvoiceSelectedEnhance(onSuccess: () => void): SubmitFunction {
		return () => {
			invoiceSelectedSubmitting = true;
			return async ({ result, update }) => {
				invoiceSelectedSubmitting = false;
				if (
					result.type === 'success' &&
					result.data &&
					'action' in result.data &&
					result.data.action === 'invoiceSelected' &&
					'result' in result.data
				) {
					toast.success(
						invoiceSelectedSummary(result.data.result as GenerateSelectedInvoicesResult)
					);
					onSuccess();
				}
				await update({ reset: false });
			};
		};
	}
</script>

<svelte:head>
	<title>{clientName || 'Clients'} — {appName} Admin</title>
</svelte:head>

{#if !client}
	<h1 class="hp-h1">Client Profile</h1>
	<div class="hp-alert-red" data-testid="client-detail-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.loadError ??
			'Failed to load client.'}
	</div>
	<a class="hp-btn" href="/admin/clients" style="margin-top:14px;display:inline-flex">
		<i class="fas fa-arrow-left"></i>Back
	</a>
{:else}
	<div
		style="display:flex;flex-wrap:wrap;align-items:flex-start;justify-content:space-between;gap:12px;margin-bottom:14px"
	>
		<div>
			<h1 class="hp-h1" style="margin-bottom:4px;display:flex;align-items:center;gap:10px">
				<span data-testid="client-name">{clientName}</span>
				<span data-testid="client-status">
					<span class={`hp-badge ${clientBadgeClass(client.status)}`}
						>{titleCase(client.status)}</span
					>
				</span>
			</h1>
			<p style="color:#666;font-size:13px;margin:0">
				{#if data.email}{data.email} ·
				{/if}Credit Balance:
				<span style="font-weight:600;color:#333" data-testid="client-credit-balance">
					<MoneyText amount={client.credit_balance ?? 0} />
				</span>
			</p>
		</div>
		{#if data.user.role === 'admin'}
			<form method="POST" action={`${base}/impersonate`}>
				<button
					type="submit"
					class="hp-btn hp-btn-primary"
					title="Opens the client area as this user."
					data-testid="client-impersonate"
				>
					<i class="fas fa-sign-in-alt"></i>Login as Client
				</button>
			</form>
		{/if}
	</div>

	<div class="hp-tabs" data-testid="client-tabs">
		{#each tabs as tab (tab.id)}
			<a
				href={`${base}?tab=${tab.id}`}
				class="hp-tab"
				class:active={data.tab === tab.id}
				aria-current={data.tab === tab.id ? 'page' : undefined}
			>
				{tab.label}
			</a>
		{/each}
	</div>

	<div class="hp-tabpanel">
		{#if data.tab === 'summary'}
			<section class="hp-panel">
				<div class="hp-panel-hd"><span class="title">Client Information</span></div>
				<div style="padding:16px 18px">
					{#if form?.action === 'profile' && formError}
						<div class="hp-alert-red" style="margin-bottom:14px">{formError}</div>
					{/if}
					<form
						method="POST"
						action="?/profile"
						data-testid="client-profile-form"
						use:enhance={makeEnhance((v) => (profileSubmitting = v))}
					>
						<div class="hp-formrow">
							<label for="cf-first-name">First Name <span style="color:#d9534f">*</span></label>
							<div class="hp-field">
								<input
									id="cf-first-name"
									class="hp-input"
									class:hp-input-error={!!profileFields.first_name}
									type="text"
									name="first_name"
									bind:value={firstName}
									required
								/>
								{#if profileFields.first_name}
									<div style="color:#d9534f;font-size:12px;margin-top:3px">
										{profileFields.first_name}
									</div>
								{/if}
							</div>
						</div>
						<div class="hp-formrow">
							<label for="cf-last-name">Last Name <span style="color:#d9534f">*</span></label>
							<div class="hp-field">
								<input
									id="cf-last-name"
									class="hp-input"
									class:hp-input-error={!!profileFields.last_name}
									type="text"
									name="last_name"
									bind:value={lastName}
									required
								/>
								{#if profileFields.last_name}
									<div style="color:#d9534f;font-size:12px;margin-top:3px">
										{profileFields.last_name}
									</div>
								{/if}
							</div>
						</div>
						<div class="hp-formrow">
							<label for="cf-company">Company</label>
							<div class="hp-field">
								<input
									id="cf-company"
									class="hp-input"
									class:hp-input-error={!!profileFields.company}
									type="text"
									name="company"
									bind:value={company}
								/>
								{#if profileFields.company}
									<div style="color:#d9534f;font-size:12px;margin-top:3px">
										{profileFields.company}
									</div>
								{/if}
							</div>
						</div>
						<div class="hp-formrow">
							<label for="cf-phone">Phone</label>
							<div class="hp-field">
								<input
									id="cf-phone"
									class="hp-input"
									class:hp-input-error={!!profileFields.phone}
									type="text"
									name="phone"
									bind:value={phone}
								/>
								{#if profileFields.phone}
									<div style="color:#d9534f;font-size:12px;margin-top:3px">
										{profileFields.phone}
									</div>
								{/if}
							</div>
						</div>
						<div class="hp-formrow">
							<label for="cf-address1">Address 1</label>
							<div class="hp-field">
								<input
									id="cf-address1"
									class="hp-input"
									class:hp-input-error={!!profileFields.address1}
									type="text"
									name="address1"
									bind:value={address1}
								/>
								{#if profileFields.address1}
									<div style="color:#d9534f;font-size:12px;margin-top:3px">
										{profileFields.address1}
									</div>
								{/if}
							</div>
						</div>
						<div class="hp-formrow">
							<label for="cf-address2">Address 2</label>
							<div class="hp-field">
								<input
									id="cf-address2"
									class="hp-input"
									class:hp-input-error={!!profileFields.address2}
									type="text"
									name="address2"
									bind:value={address2}
								/>
								{#if profileFields.address2}
									<div style="color:#d9534f;font-size:12px;margin-top:3px">
										{profileFields.address2}
									</div>
								{/if}
							</div>
						</div>
						<div class="hp-formrow">
							<label for="cf-city">City</label>
							<div class="hp-field">
								<input
									id="cf-city"
									class="hp-input"
									class:hp-input-error={!!profileFields.city}
									type="text"
									name="city"
									bind:value={city}
								/>
								{#if profileFields.city}
									<div style="color:#d9534f;font-size:12px;margin-top:3px">
										{profileFields.city}
									</div>
								{/if}
							</div>
						</div>
						<div class="hp-formrow">
							<label for="cf-state">State/Province</label>
							<div class="hp-field">
								<input
									id="cf-state"
									class="hp-input"
									class:hp-input-error={!!profileFields.state}
									type="text"
									name="state"
									bind:value={stateProv}
								/>
								{#if profileFields.state}
									<div style="color:#d9534f;font-size:12px;margin-top:3px">
										{profileFields.state}
									</div>
								{/if}
							</div>
						</div>
						<div class="hp-formrow">
							<label for="cf-postcode">Postcode</label>
							<div class="hp-field">
								<input
									id="cf-postcode"
									class="hp-input"
									class:hp-input-error={!!profileFields.postcode}
									type="text"
									name="postcode"
									bind:value={postcode}
								/>
								{#if profileFields.postcode}
									<div style="color:#d9534f;font-size:12px;margin-top:3px">
										{profileFields.postcode}
									</div>
								{/if}
							</div>
						</div>
						<div class="hp-formrow">
							<label for="cf-country">Country (ISO code)</label>
							<div class="hp-field">
								<input
									id="cf-country"
									class="hp-input"
									class:hp-input-error={!!profileFields.country}
									type="text"
									name="country"
									bind:value={country}
								/>
								{#if profileFields.country}
									<div style="color:#d9534f;font-size:12px;margin-top:3px">
										{profileFields.country}
									</div>
								{/if}
							</div>
						</div>
						<div class="hp-formrow">
							<label for="cf-status">Status</label>
							<div class="hp-field">
								<select
									id="cf-status"
									class="hp-select"
									class:hp-input-error={!!profileFields.status}
									name="status"
									bind:value={status}
								>
									{#each statusOptions as opt (opt.value)}
										<option value={opt.value}>{opt.label}</option>
									{/each}
								</select>
								{#if profileFields.status}
									<div style="color:#d9534f;font-size:12px;margin-top:3px">
										{profileFields.status}
									</div>
								{/if}
							</div>
						</div>

						<div style="margin-top:8px">
							<button
								type="submit"
								class="hp-btn hp-btn-primary"
								disabled={profileSubmitting}
								data-testid="client-profile-submit"
							>
								{profileSubmitting ? 'Saving…' : 'Save Changes'}
							</button>
						</div>
					</form>
				</div>
			</section>

			<div class="hp-summary-grid">
				<section class="hp-panel">
					<div class="hp-panel-hd"><span class="title">Credit Balance</span></div>
					<div style="padding:18px 14px;text-align:center">
						<div style="font-size:26px;font-weight:700;color:#333">
							<MoneyText amount={client.credit_balance ?? 0} />
						</div>
						<button
							type="button"
							class="hp-btn hp-btn-primary"
							style="margin-top:12px"
							data-testid="client-credit-open"
							onclick={() => (creditOpen = true)}
						>
							Adjust Credit
						</button>
					</div>
				</section>

				<section class="hp-panel">
					<div class="hp-panel-hd"><span class="title">Account Information</span></div>
					<div style="padding:12px 14px;font-size:13px">
						<div style="display:flex;justify-content:space-between;gap:8px;padding:4px 0">
							<span style="color:#888">Email</span>
							<span style="font-weight:600;color:#333;text-align:right">{data.email || '—'}</span>
						</div>
						<div style="display:flex;justify-content:space-between;gap:8px;padding:4px 0">
							<span style="color:#888">Registered</span>
							<span style="font-weight:600;color:#333"><DateText value={client.created_at} /></span>
						</div>
						{#if data.userInfo}
							<div style="display:flex;justify-content:space-between;gap:8px;padding:4px 0">
								<span style="color:#888">Two-Factor Auth</span>
								<span style="font-weight:600;color:#333">
									{data.userInfo.twofa_enabled ? 'Yes' : 'No'}
								</span>
							</div>
							<div style="display:flex;justify-content:space-between;gap:8px;padding:4px 0">
								<span style="color:#888">Last Login</span>
								<span style="font-weight:600;color:#333">
									<DateText value={data.userInfo.last_login_at} mode="datetime" />
								</span>
							</div>
						{/if}
					</div>
				</section>

				<section class="hp-panel">
					<div class="hp-panel-hd">
						<span class="title">Admin Notes</span>
						<a href={`${base}?tab=notes`} style="color:#337ab7;font-size:12px">Edit</a>
					</div>
					<div style="padding:12px 14px" data-testid="client-notes-preview">
						{#if client.notes_admin}
							<p
								style="color:#555;font-size:13px;white-space:pre-wrap;max-height:110px;overflow:hidden;margin:0"
							>
								{client.notes_admin}
							</p>
						{:else}
							<p style="color:#999;font-size:13px;font-style:italic;margin:0">No notes yet.</p>
						{/if}
					</div>
				</section>

				<section class="hp-panel">
					<div class="hp-panel-hd"><span class="title">Related</span></div>
					<div style="padding:10px 14px;display:flex;flex-direction:column;gap:8px;font-size:13px">
						<a href={`${base}?tab=services`} style="color:#337ab7">
							<i class="fas fa-box" style="width:16px;color:#999"></i> Services
						</a>
						<a href={`${base}?tab=domains`} style="color:#337ab7">
							<i class="fas fa-globe" style="width:16px;color:#999"></i> Domains
						</a>
						<a href={`${base}?tab=invoices`} style="color:#337ab7">
							<i class="fas fa-file-invoice" style="width:16px;color:#999"></i> Invoices
						</a>
						<a href={`${base}?tab=tickets`} style="color:#337ab7">
							<i class="fas fa-comment" style="width:16px;color:#999"></i> Tickets
						</a>
						<a href={`${base}?tab=contacts`} style="color:#337ab7">
							<i class="fas fa-address-book" style="width:16px;color:#999"></i> Contacts
						</a>
					</div>
				</section>
			</div>
		{:else if data.tab === 'services'}
			{#if data.tabError}
				<div class="hp-alert-yellow" style="margin-bottom:14px">
					<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.tabError}
				</div>
			{/if}
			{#if form?.action === 'invoiceSelected' && formError}
				<div class="hp-alert-red" style="margin-bottom:14px" data-testid="invoice-selected-error">
					{formError}
				</div>
			{/if}
			<form
				method="POST"
				action="?/invoiceSelected"
				use:enhance={makeInvoiceSelectedEnhance(() => (selectedServices = new Set()))}
			>
				<input type="hidden" name="service_ids" value={JSON.stringify([...selectedServices])} />
				<div class="hp-scroll">
					<table class="hp-table">
						<thead>
							<tr>
								<th class="c" style="width:34px">
									<input
										type="checkbox"
										checked={allServicesChecked}
										onchange={toggleAllServices}
										aria-label="Select all services"
										data-testid="service-select-all"
									/>
								</th>
								<th style="width:70px">ID</th>
								<th>Product</th>
								<th>Domain</th>
								<th class="r">Recurring Amount</th>
								<th class="c" style="width:130px">Next Due</th>
								<th class="c" style="width:110px">Status</th>
							</tr>
						</thead>
						<tbody>
							{#each data.services ?? [] as row (row.id)}
								<tr class="hp-row">
									<td class="c">
										<input
											type="checkbox"
											checked={selectedServices.has(row.id)}
											onchange={() => toggleService(row.id)}
											aria-label={`Select service #${row.id}`}
											data-testid={`service-select-${row.id}`}
										/>
									</td>
									<td>{row.id}</td>
									<td>
										<a
											class="cell-link"
											href={`/admin/services/${row.id}`}
											data-testid={`row-service-${row.id}`}
										>
											{row.product_name ?? `#${row.product_id}`}
										</a>
									</td>
									<td>{row.domain || '—'}</td>
									<td class="r"><MoneyText amount={row.recurring_amount ?? 0} /></td>
									<td class="c"><DateText value={row.next_due_date} /></td>
									<td class="c">
										<span class={`hp-badge ${serviceBadgeClass(row.status)}`}
											>{titleCase(row.status)}</span
										>
									</td>
								</tr>
							{:else}
								<tr>
									<td colspan="7" style="text-align:center;padding:28px;color:#999">
										This client has no services.
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
				<div class="hp-bulk">
					<span>With Selected:</span>
					<button
						type="submit"
						class="hp-btn hp-btn-primary"
						disabled={selectedServices.size === 0 || invoiceSelectedSubmitting}
						data-testid="invoice-selected-btn"
					>
						{invoiceSelectedSubmitting ? 'Invoicing…' : 'Invoice Selected Items'}
					</button>
				</div>
			</form>
			{@render pager()}
		{:else if data.tab === 'domains'}
			{#if data.tabError}
				<div class="hp-alert-yellow" style="margin-bottom:14px">
					<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.tabError}
				</div>
			{/if}
			{#if form?.action === 'invoiceSelected' && formError}
				<div class="hp-alert-red" style="margin-bottom:14px" data-testid="invoice-selected-error">
					{formError}
				</div>
			{/if}
			<form
				method="POST"
				action="?/invoiceSelected"
				use:enhance={makeInvoiceSelectedEnhance(() => (selectedDomains = new Set()))}
			>
				<input type="hidden" name="domain_ids" value={JSON.stringify([...selectedDomains])} />
				<div class="hp-scroll">
					<table class="hp-table">
						<thead>
							<tr>
								<th class="c" style="width:34px">
									<input
										type="checkbox"
										checked={allDomainsChecked}
										onchange={toggleAllDomains}
										aria-label="Select all domains"
										data-testid="domain-select-all"
									/>
								</th>
								<th>Domain</th>
								<th class="c" style="width:110px">Status</th>
								<th class="c" style="width:130px">Expiry</th>
								<th class="c" style="width:100px">Auto Renew</th>
								<th class="r">Recurring Amount</th>
							</tr>
						</thead>
						<tbody>
							{#each data.domains ?? [] as row (row.id)}
								<tr class="hp-row">
									<td class="c">
										<input
											type="checkbox"
											checked={selectedDomains.has(row.id)}
											onchange={() => toggleDomain(row.id)}
											aria-label={`Select domain #${row.id}`}
											data-testid={`domain-select-${row.id}`}
										/>
									</td>
									<td>
										<a
											class="cell-link"
											href={`/admin/domains/${row.id}`}
											data-testid={`row-domain-${row.id}`}
										>
											{row.name}
										</a>
									</td>
									<td class="c">
										<span class={`hp-badge ${domainBadgeClass(row.status)}`}
											>{titleCase(row.status)}</span
										>
									</td>
									<td class="c"><DateText value={row.expiry_date} /></td>
									<td class="c">{row.auto_renew ? 'Yes' : 'No'}</td>
									<td class="r"><MoneyText amount={row.recurring_amount ?? 0} /></td>
								</tr>
							{:else}
								<tr>
									<td colspan="6" style="text-align:center;padding:28px;color:#999">
										This client has no domains.
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
				<div class="hp-bulk">
					<span>With Selected:</span>
					<button
						type="submit"
						class="hp-btn hp-btn-primary"
						disabled={selectedDomains.size === 0 || invoiceSelectedSubmitting}
						data-testid="invoice-selected-btn"
					>
						{invoiceSelectedSubmitting ? 'Invoicing…' : 'Invoice Selected Items'}
					</button>
				</div>
			</form>
			{@render pager()}
		{:else if data.tab === 'invoices'}
			{#if data.tabError}
				<div class="hp-alert-yellow" style="margin-bottom:14px">
					<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.tabError}
				</div>
			{/if}
			<div class="hp-scroll">
				<table class="hp-table">
					<thead>
						<tr>
							<th>Invoice #</th>
							<th class="c" style="width:130px">Due Date</th>
							<th class="r" style="width:150px">Total</th>
							<th class="c" style="width:100px">Status</th>
						</tr>
					</thead>
					<tbody>
						{#each data.invoices ?? [] as row (row.id)}
							<tr class="hp-row">
								<td>
									<a
										class="cell-link"
										href={`/admin/invoices/${row.id}`}
										data-testid={`row-invoice-${row.id}`}
									>
										{row.invoice_number}
									</a>
								</td>
								<td class="c"><DateText value={row.due_date} /></td>
								<td class="r"><MoneyText amount={row.total ?? 0} /></td>
								<td class="c">
									<span class={`hp-stext ${invoiceStextClass(row.status)}`}
										>{titleCase(row.status)}</span
									>
								</td>
							</tr>
						{:else}
							<tr>
								<td colspan="4" style="text-align:center;padding:28px;color:#999">
									This client has no invoices.
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
			{@render pager()}
		{:else if data.tab === 'tickets'}
			{#if data.tabError}
				<div class="hp-alert-yellow" style="margin-bottom:14px">
					<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.tabError}
				</div>
			{/if}
			<div class="hp-scroll">
				<table class="hp-table">
					<thead>
						<tr>
							<th style="width:110px">No.</th>
							<th>Subject</th>
							<th class="c" style="width:100px">Priority</th>
							<th class="c" style="width:120px">Status</th>
							<th class="c" style="width:150px">Last Reply</th>
						</tr>
					</thead>
					<tbody>
						{#each data.tickets ?? [] as row (row.id)}
							<tr class="hp-row">
								<td>
									<a
										class="cell-link"
										href={`/admin/tickets/${row.id}`}
										data-testid={`row-ticket-${row.id}`}
									>
										{row.ticket_number}
									</a>
								</td>
								<td>{row.subject}</td>
								<td class="c">{titleCase(row.priority)}</td>
								<td class="c">
									<span class={`hp-stext ${ticketStextClass(row.status)}`}
										>{titleCase(row.status)}</span
									>
								</td>
								<td class="c"
									><DateText value={row.last_reply_at ?? row.created_at} mode="datetime" /></td
								>
							</tr>
						{:else}
							<tr>
								<td colspan="5" style="text-align:center;padding:28px;color:#999">
									This client has no tickets.
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
			{@render pager()}
		{:else if data.tab === 'notes'}
			<section class="hp-panel" style="max-width:760px">
				<div class="hp-panel-hd"><span class="title">Admin Notes</span></div>
				<div style="padding:16px 18px">
					{#if form?.action === 'notes' && formError}
						<div class="hp-alert-red" style="margin-bottom:14px">{formError}</div>
					{/if}
					<form
						method="POST"
						action="?/notes"
						data-testid="client-notes-form"
						use:enhance={makeEnhance((v) => (notesSubmitting = v))}
					>
						<textarea
							class="hp-textarea"
							name="notes_admin"
							rows="8"
							bind:value={notesAdmin}
							placeholder="Internal notes about this client…"></textarea>
						<div style="margin-top:12px">
							<button
								type="submit"
								class="hp-btn hp-btn-primary"
								disabled={notesSubmitting}
								data-testid="client-notes-submit"
							>
								{notesSubmitting ? 'Saving…' : 'Save Changes'}
							</button>
						</div>
					</form>
				</div>
			</section>
		{:else if data.tab === 'contacts'}
			{#if data.tabError}
				<div class="hp-alert-yellow" style="margin-bottom:14px">
					<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.tabError}
				</div>
			{/if}
			{#if form?.action === 'contactDelete' && formError}
				<div class="hp-alert-red" style="margin-bottom:14px">{formError}</div>
			{/if}
			<div style="display:flex;justify-content:flex-end;margin-bottom:12px">
				<button
					type="button"
					class="hp-btn hp-btn-primary"
					data-testid="contact-add"
					onclick={openContactCreate}
				>
					<i class="fas fa-plus"></i>Add Contact
				</button>
			</div>
			<div class="hp-scroll">
				<table class="hp-table">
					<thead>
						<tr>
							<th>Name</th>
							<th>Email</th>
							<th>Phone</th>
							<th style="width:150px"></th>
						</tr>
					</thead>
					<tbody>
						{#each data.contacts ?? [] as row (row.id)}
							<tr class="hp-row">
								<td>
									<span style="font-weight:600;color:#333" data-testid={`row-contact-${row.id}`}>
										{[row.first_name, row.last_name].filter(Boolean).join(' ') || '—'}
									</span>
								</td>
								<td>{row.email || '—'}</td>
								<td>{row.phone || '—'}</td>
								<td class="c" style="white-space:nowrap">
									<button
										type="button"
										class="hp-btn"
										style="padding:5px 10px"
										data-testid={`contact-edit-${row.id}`}
										onclick={() => openContactEdit(row)}
									>
										Edit
									</button>
									<button
										type="button"
										class="hp-btn hp-btn-danger"
										style="padding:5px 10px"
										data-testid={`contact-delete-${row.id}`}
										onclick={() => askDeleteContact(row.id)}
									>
										Delete
									</button>
								</td>
							</tr>
						{:else}
							<tr>
								<td colspan="4" style="text-align:center;padding:28px;color:#999">
									No additional contacts.
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>

			<!-- Hidden delete form, submitted from the ConfirmDialog -->
			<form
				method="POST"
				action="?/contactDelete"
				class="hidden"
				bind:this={deleteFormEl}
				use:enhance={makeEnhance(
					(v) => (deleteSubmitting = v),
					() => (deleteOpen = false)
				)}
			>
				<input type="hidden" name="contact_id" value={deleteContactId ?? ''} />
			</form>

			<ConfirmDialog
				bind:open={deleteOpen}
				title="Delete Contact"
				message="Delete this contact? This cannot be undone."
				confirmLabel="Delete"
				danger
				loading={deleteSubmitting}
				onConfirm={() => deleteFormEl?.requestSubmit()}
			/>
		{/if}
	</div>

	<!-- Credit adjust modal -->
	<Modal bind:open={creditOpen} title="Adjust Credit" size="sm">
		{#if form?.action === 'credit' && formError}
			<div class="hp-alert-red" style="margin-bottom:14px">{formError}</div>
		{/if}
		<form
			method="POST"
			action="?/credit"
			data-testid="client-credit-form"
			use:enhance={makeEnhance(
				(v) => (creditSubmitting = v),
				() => {
					creditOpen = false;
					creditAmount = 0;
					creditReason = '';
				}
			)}
		>
			<FormField
				label="Adjustment Type"
				name="mode"
				type="select"
				bind:value={creditMode}
				options={[
					{ value: 'add', label: 'Add credit' },
					{ value: 'deduct', label: 'Deduct credit' }
				]}
			/>
			<FormField
				label="Amount (IDR)"
				name="amount"
				type="number"
				bind:value={creditAmount}
				required
			/>
			<FormField label="Reason" name="reason" bind:value={creditReason} required />
			<div class="flex justify-end gap-2">
				<LoadingButton variant="secondary" onclick={() => (creditOpen = false)}>
					Cancel
				</LoadingButton>
				<span data-testid="client-credit-submit">
					<LoadingButton type="submit" loading={creditSubmitting}>Save</LoadingButton>
				</span>
			</div>
		</form>
	</Modal>

	<!-- Contact create/edit modal -->
	<Modal bind:open={contactOpen} title={contactEditId ? 'Edit Contact' : 'Add Contact'} size="sm">
		{#if form?.action === 'contact' && formError}
			<div class="hp-alert-red" style="margin-bottom:14px">{formError}</div>
		{/if}
		<form
			method="POST"
			action={contactEditId ? '?/contactUpdate' : '?/contactCreate'}
			data-testid="contact-form"
			use:enhance={makeEnhance(
				(v) => (contactSubmitting = v),
				() => (contactOpen = false)
			)}
		>
			{#if contactEditId}
				<input type="hidden" name="contact_id" value={contactEditId} />
			{/if}
			<FormField label="First Name" name="first_name" bind:value={contactFirstName} required />
			<FormField label="Last Name" name="last_name" bind:value={contactLastName} />
			<FormField label="Email" name="email" type="email" bind:value={contactEmail} required />
			<FormField label="Phone" name="phone" bind:value={contactPhone} />
			<div class="flex justify-end gap-2">
				<LoadingButton variant="secondary" onclick={() => (contactOpen = false)}>
					Cancel
				</LoadingButton>
				<span data-testid="contact-submit">
					<LoadingButton type="submit" loading={contactSubmitting}>Save</LoadingButton>
				</span>
			</div>
		</form>
	</Modal>
{/if}

{#snippet pager()}
	<div class="hp-count" style="margin:10px 0">
		{totalRows} Records Found, Showing {fromRow} to {toRow}
	</div>
	<HpPager page={curPage} perPage={perPageV} total={totalRows} />
{/snippet}

<style>
	.hp-summary-grid {
		display: grid;
		grid-template-columns: repeat(4, minmax(0, 1fr));
		gap: 16px;
		margin-top: 16px;
	}
	@media (max-width: 860px) {
		.hp-summary-grid {
			grid-template-columns: repeat(2, minmax(0, 1fr));
		}
	}
	@media (max-width: 560px) {
		.hp-summary-grid {
			grid-template-columns: 1fr;
		}
	}
	.hidden {
		display: none;
	}
</style>
