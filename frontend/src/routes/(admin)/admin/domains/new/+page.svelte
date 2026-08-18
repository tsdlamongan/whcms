<script lang="ts">
	import { enhance } from '$app/forms';
	import { untrack } from 'svelte';
	import type { PageProps } from './$types';
	import { clientDisplayName, type AdminClientLite } from '../../invoices/types';

	let { data, form }: PageProps = $props();

	let clientId = $state(untrack(() => String(form?.clientId || '')));
	let name = $state(untrack(() => form?.name ?? ''));
	let registrationDate = $state(untrack(() => form?.registrationDate ?? ''));
	let expiryDate = $state(untrack(() => form?.expiryDate ?? ''));
	let nextDueDate = $state(untrack(() => form?.nextDueDate ?? ''));
	let recurringAmount = $state(untrack(() => form?.recurringAmount ?? 0));
	let autoRenew = $state(untrack(() => form?.autoRenew ?? true));
	let ns1 = $state(untrack(() => form?.ns1 ?? ''));
	let ns2 = $state(untrack(() => form?.ns2 ?? ''));
	let ns3 = $state(untrack(() => form?.ns3 ?? ''));
	let ns4 = $state(untrack(() => form?.ns4 ?? ''));
	let submitting = $state(false);

	const clientOptions = $derived(
		data.clients.map((c: AdminClientLite) => ({
			value: String(c.id),
			label: `${clientDisplayName(c)}${c.email ? ` (${c.email})` : ''}`
		}))
	);
</script>

<svelte:head>
	<title>Add Existing Domain — HostPanel Admin</title>
</svelte:head>

<h1 class="hp-h1">Add Existing Domain</h1>
<p class="hp-lead">
	Record a domain that is already registered directly on a client — no order, no payment, and no
	registrar call is made. Use “Sync Status” on the domain afterwards to pull the live
	status/expiry/nameservers from the registrar.
</p>

{#if form?.errorMessage}
	<div class="hp-alert-red" data-testid="domain-create-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{form.errorMessage}
	</div>
{/if}

<div class="hp-panel" style="padding:16px 20px;max-width:860px">
	<!-- Querystring-driven client search (separate GET form; forms must not nest). -->
	<form
		method="GET"
		action="/admin/domains/new"
		style="display:flex;flex-wrap:wrap;gap:8px;margin-bottom:14px"
	>
		<input
			type="search"
			name="client_search"
			value={data.clientSearch}
			placeholder="Search name, email, company…"
			data-testid="domain-create-client-search"
			class="hp-input"
			style="flex:1 1 260px"
		/>
		<button type="submit" data-testid="domain-create-client-search-submit" class="hp-btn">
			<i class="fas fa-search"></i>Search
		</button>
	</form>

	{#if data.clientsError}
		<div class="hp-alert-yellow">{data.clientsError}</div>
	{/if}

	<form
		method="POST"
		data-testid="domain-create-form"
		use:enhance={() => {
			submitting = true;
			return async ({ update }) => {
				submitting = false;
				await update();
			};
		}}
	>
		<div class="hp-formrow">
			<label for="field-client_id">Client<span style="color:#d9534f"> *</span></label>
			<div class="hp-field" style="flex:1 1 auto">
				<select
					id="field-client_id"
					name="client_id"
					bind:value={clientId}
					class="hp-select"
					data-testid="domain-create-client"
					required
				>
					<option value="" disabled>Choose a client…</option>
					{#each clientOptions as opt (opt.value)}
						<option value={opt.value}>{opt.label}</option>
					{/each}
				</select>
				{#if data.clients.length === 0}
					<div class="hp-help" style="margin-top:3px">
						No matching clients — use the search above.
					</div>
				{/if}
			</div>
		</div>

		<div class="hp-formrow">
			<label for="field-name">Domain Name<span style="color:#d9534f"> *</span></label>
			<div class="hp-field">
				<input
					id="field-name"
					name="name"
					class="hp-input"
					bind:value={name}
					placeholder="example.com"
					data-testid="domain-create-name"
					required
				/>
			</div>
		</div>

		<div class="hp-formrow">
			<label for="field-registration_date">Registration Date</label>
			<div class="hp-field">
				<input
					id="field-registration_date"
					name="registration_date"
					type="date"
					class="hp-input"
					bind:value={registrationDate}
				/>
			</div>
		</div>

		<div class="hp-formrow">
			<label for="field-expiry_date">Expiry Date</label>
			<div class="hp-field">
				<input
					id="field-expiry_date"
					name="expiry_date"
					type="date"
					class="hp-input"
					bind:value={expiryDate}
					data-testid="domain-create-expiry"
				/>
			</div>
		</div>

		<div class="hp-formrow">
			<label for="field-next_due_date">Next Due Date<span style="color:#d9534f"> *</span></label>
			<div class="hp-field">
				<input
					id="field-next_due_date"
					name="next_due_date"
					type="date"
					class="hp-input"
					bind:value={nextDueDate}
					data-testid="domain-create-next-due"
					required
				/>
				<div class="hp-help" style="margin-top:3px">
					Renewal invoices are generated from this date.
				</div>
			</div>
		</div>

		<div class="hp-formrow">
			<label for="field-recurring_amount">Renewal Price (IDR / year)</label>
			<div class="hp-field">
				<input
					id="field-recurring_amount"
					name="recurring_amount"
					type="number"
					min="0"
					step="1"
					class="hp-input"
					bind:value={recurringAmount}
					data-testid="domain-create-amount"
				/>
			</div>
		</div>

		<div class="hp-formrow">
			<label for="field-auto_renew">&nbsp;</label>
			<div class="hp-field" style="flex:1 1 auto">
				<label class="hp-checkline" style="padding:0">
					<input id="field-auto_renew" name="auto_renew" type="checkbox" bind:checked={autoRenew} />
					Auto Renew
				</label>
			</div>
		</div>

		<div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(240px,1fr));gap:0 24px">
			<div class="hp-formrow">
				<label for="field-ns1">Nameserver 1</label>
				<div class="hp-field">
					<input
						id="field-ns1"
						name="ns1"
						class="hp-input"
						bind:value={ns1}
						placeholder="ns1.example.com"
					/>
				</div>
			</div>
			<div class="hp-formrow">
				<label for="field-ns2">Nameserver 2</label>
				<div class="hp-field">
					<input
						id="field-ns2"
						name="ns2"
						class="hp-input"
						bind:value={ns2}
						placeholder="ns2.example.com"
					/>
				</div>
			</div>
			<div class="hp-formrow">
				<label for="field-ns3">Nameserver 3</label>
				<div class="hp-field">
					<input id="field-ns3" name="ns3" class="hp-input" bind:value={ns3} />
				</div>
			</div>
			<div class="hp-formrow">
				<label for="field-ns4">Nameserver 4</label>
				<div class="hp-field">
					<input id="field-ns4" name="ns4" class="hp-input" bind:value={ns4} />
				</div>
			</div>
		</div>

		<div class="hp-form-actions" style="justify-content:flex-end">
			<a href="/admin/domains" class="hp-btn">Cancel</a>
			<button
				type="submit"
				class="hp-btn hp-btn-primary"
				disabled={submitting}
				data-testid="domain-create-submit"
			>
				<i class="fas fa-plus"></i>{submitting ? 'Adding…' : 'Add Domain'}
			</button>
		</div>
	</form>
</div>
