<script lang="ts">
	import { enhance } from '$app/forms';
	import { untrack } from 'svelte';
	import type { PageProps } from './$types';
	import { clientDisplayName, type AdminClientLite } from '../../invoices/types';

	let { data, form }: PageProps = $props();

	let clientId = $state(untrack(() => String(form?.clientId || '')));
	let productId = $state(untrack(() => String(form?.productId || '')));
	let serverId = $state(untrack(() => String(form?.serverId || '')));
	let domain = $state(untrack(() => form?.domain ?? ''));
	let username = $state(untrack(() => form?.username ?? ''));
	let password = $state('');
	let billingCycle = $state(untrack(() => form?.billingCycle || 'monthly'));
	let recurringAmount = $state(untrack(() => form?.recurringAmount ?? 0));
	let nextDueDate = $state(untrack(() => form?.nextDueDate ?? ''));
	let registrationDate = $state(untrack(() => form?.registrationDate ?? ''));
	let notes = $state(untrack(() => form?.notes ?? ''));
	let submitting = $state(false);

	const cycles = [
		{ value: 'one_time', label: 'One Time' },
		{ value: 'monthly', label: 'Monthly' },
		{ value: 'quarterly', label: 'Quarterly' },
		{ value: 'semiannually', label: 'Semi-Annually' },
		{ value: 'annually', label: 'Annually' },
		{ value: 'biennially', label: 'Biennially' }
	];

	const clientOptions = $derived(
		data.clients.map((c: AdminClientLite) => ({
			value: String(c.id),
			label: `${clientDisplayName(c)}${c.email ? ` (${c.email})` : ''}`
		}))
	);
</script>

<svelte:head>
	<title>Add Existing Service — HostPanel Admin</title>
</svelte:head>

<h1 class="hp-h1">Add Existing Service</h1>
<p class="hp-lead">
	Record a hosting service that already exists on a server (or elsewhere) directly on a client — no
	order, no payment, and no provisioning call is made. The service is created Active.
</p>

{#if form?.errorMessage}
	<div class="hp-alert-red" data-testid="service-create-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{form.errorMessage}
	</div>
{/if}

<div class="hp-panel" style="padding:16px 20px;max-width:860px">
	<!-- Querystring-driven client search (separate GET form; forms must not nest). -->
	<form
		method="GET"
		action="/admin/services/new"
		style="display:flex;flex-wrap:wrap;gap:8px;margin-bottom:14px"
	>
		<input
			type="search"
			name="client_search"
			value={data.clientSearch}
			placeholder="Search name, email, company…"
			data-testid="service-create-client-search"
			class="hp-input"
			style="flex:1 1 260px"
		/>
		<button type="submit" data-testid="service-create-client-search-submit" class="hp-btn">
			<i class="fas fa-search"></i>Search
		</button>
	</form>

	{#if data.clientsError}
		<div class="hp-alert-yellow">{data.clientsError}</div>
	{/if}

	<form
		method="POST"
		data-testid="service-create-form"
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
					data-testid="service-create-client"
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
			<label for="field-product_id">Product<span style="color:#d9534f"> *</span></label>
			<div class="hp-field" style="flex:1 1 auto">
				<select
					id="field-product_id"
					name="product_id"
					bind:value={productId}
					class="hp-select"
					data-testid="service-create-product"
					required
				>
					<option value="" disabled>Choose a product…</option>
					{#each data.products as p (p.id)}
						<option value={String(p.id)}>{p.name}{p.module ? ` — ${p.module}` : ''}</option>
					{/each}
				</select>
			</div>
		</div>

		<div class="hp-formrow">
			<label for="field-server_id">Server</label>
			<div class="hp-field" style="flex:1 1 auto">
				<select
					id="field-server_id"
					name="server_id"
					bind:value={serverId}
					class="hp-select"
					data-testid="service-create-server"
				>
					<option value="">None</option>
					{#each data.servers as s (s.id)}
						<option value={String(s.id)}>{s.name}{s.module ? ` — ${s.module}` : ''}</option>
					{/each}
				</select>
				<div class="hp-help" style="margin-top:3px">
					Pick the server the existing account lives on so suspend/terminate/SSO work against it.
				</div>
			</div>
		</div>

		<div class="hp-formrow">
			<label for="field-domain">Domain</label>
			<div class="hp-field">
				<input
					id="field-domain"
					name="domain"
					class="hp-input"
					bind:value={domain}
					placeholder="example.com"
					data-testid="service-create-domain"
				/>
			</div>
		</div>

		<div class="hp-formrow">
			<label for="field-username">Panel Username</label>
			<div class="hp-field">
				<input
					id="field-username"
					name="username"
					class="hp-input"
					bind:value={username}
					placeholder="the existing control-panel username"
					data-testid="service-create-username"
				/>
			</div>
		</div>

		<div class="hp-formrow">
			<label for="field-password">Panel Password</label>
			<div class="hp-field">
				<input
					id="field-password"
					name="password"
					type="password"
					class="hp-input"
					bind:value={password}
					placeholder="optional — stored encrypted"
					autocomplete="new-password"
				/>
			</div>
		</div>

		<div class="hp-formrow">
			<label for="field-billing_cycle">Billing Cycle<span style="color:#d9534f"> *</span></label>
			<div class="hp-field">
				<select
					id="field-billing_cycle"
					name="billing_cycle"
					bind:value={billingCycle}
					class="hp-select"
					data-testid="service-create-cycle"
				>
					{#each cycles as c (c.value)}
						<option value={c.value}>{c.label}</option>
					{/each}
				</select>
			</div>
		</div>

		<div class="hp-formrow">
			<label for="field-recurring_amount">Recurring Amount (IDR)</label>
			<div class="hp-field">
				<input
					id="field-recurring_amount"
					name="recurring_amount"
					type="number"
					min="0"
					step="1"
					class="hp-input"
					bind:value={recurringAmount}
					data-testid="service-create-amount"
				/>
			</div>
		</div>

		<div class="hp-formrow">
			<label for="field-next_due_date">Next Due Date</label>
			<div class="hp-field">
				<input
					id="field-next_due_date"
					name="next_due_date"
					type="date"
					class="hp-input"
					bind:value={nextDueDate}
					data-testid="service-create-next-due"
				/>
				<div class="hp-help" style="margin-top:3px">
					Required for recurring cycles — renewal invoices are generated from this date.
				</div>
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
				<div class="hp-help" style="margin-top:3px">Defaults to today when left empty.</div>
			</div>
		</div>

		<div class="hp-formrow">
			<label for="field-notes">Admin Notes</label>
			<div class="hp-field">
				<input id="field-notes" name="notes" class="hp-input" bind:value={notes} />
			</div>
		</div>

		<div class="hp-form-actions" style="justify-content:flex-end">
			<a href="/admin/services" class="hp-btn">Cancel</a>
			<button
				type="submit"
				class="hp-btn hp-btn-primary"
				disabled={submitting}
				data-testid="service-create-submit"
			>
				<i class="fas fa-plus"></i>{submitting ? 'Adding…' : 'Add Service'}
			</button>
		</div>
	</form>
</div>
