<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import { untrack } from 'svelte';
	import type { PageProps } from './$types';
	import { clientDisplayName, type AdminClientLite } from '../../invoices/types';

	let { data, form }: PageProps = $props();

	let clientId = $state(untrack(() => String(form?.clientId || '')));
	let domain = $state(untrack(() => form?.domain ?? ''));
	let years = $state(untrack(() => form?.years ?? 1));
	let huntFrom = $state(untrack(() => form?.huntFrom ?? ''));
	let huntUntil = $state(untrack(() => form?.huntUntil ?? ''));
	let deadline = $state(untrack(() => form?.deadline ?? ''));
	let windowDays = $state(untrack(() => form?.windowDays || 30));
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
	<title>Start a Domain Hunt — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Start a Domain Hunt</h1>
<p class="hp-lead">
	A free hunt: no invoice, no refund. The domain is registered under the chosen client's profile
	when the registrar accepts it, and the client is emailed. All times are Asia/Jakarta (WIB).
</p>

{#if form?.errorMessage}
	<div class="hp-alert-red" data-testid="snipe-create-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{form.errorMessage}
	</div>
{/if}

<div class="hp-panel" style="padding:16px 20px;max-width:860px">
	<form
		method="GET"
		action="/admin/snipes/new"
		style="display:flex;flex-wrap:wrap;gap:8px;margin-bottom:14px"
	>
		<input
			type="search"
			name="client_search"
			value={data.clientSearch}
			placeholder="Search name, email, company…"
			data-testid="snipe-create-client-search"
			class="hp-input"
			style="flex:1 1 260px"
		/>
		<button type="submit" data-testid="snipe-create-client-search-submit" class="hp-btn">
			<i class="fas fa-search"></i>Search
		</button>
	</form>

	{#if data.clientsError}
		<div class="hp-alert-yellow">{data.clientsError}</div>
	{/if}

	<form
		method="POST"
		data-testid="snipe-create-form"
		use:enhance={() => {
			submitting = true;
			return async ({ update }) => {
				submitting = false;
				await update({ reset: false });
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
					data-testid="snipe-create-client"
					required
				>
					<option value="" disabled>Choose a client…</option>
					{#each clientOptions as opt (opt.value)}
						<option value={opt.value}>{opt.label}</option>
					{/each}
				</select>
				<div class="hp-help" style="margin-top:3px">
					The client's profile must have a full address and phone: the registrar requires them.
				</div>
			</div>
		</div>

		<div class="hp-formrow">
			<label for="field-domain">Domain Name<span style="color:#d9534f"> *</span></label>
			<div class="hp-field">
				<input
					id="field-domain"
					name="domain"
					class="hp-input"
					bind:value={domain}
					placeholder="example.com"
					data-testid="snipe-create-domain"
					required
				/>
				<div class="hp-help" style="margin-top:3px">
					The TLD needs a configured selling price (Domain Pricing).
				</div>
			</div>
		</div>

		<div class="hp-formrow">
			<label for="field-years">Years</label>
			<div class="hp-field">
				<input
					id="field-years"
					name="years"
					type="number"
					min="1"
					max="10"
					class="hp-input"
					bind:value={years}
					data-testid="snipe-create-years"
				/>
			</div>
		</div>

		<div class="hp-formrow">
			<label for="field-hunt_from">Drop Window Start</label>
			<div class="hp-field">
				<input
					id="field-hunt_from"
					name="hunt_from"
					type="datetime-local"
					class="hp-input"
					bind:value={huntFrom}
					data-testid="snipe-create-from"
				/>
				<div class="hp-help" style="margin-top:3px">
					Optional. Register calls only burst inside the window (starting a minute early); otherwise
					the hunt probes availability and bursts once the name looks free.
				</div>
			</div>
		</div>

		<div class="hp-formrow">
			<label for="field-hunt_until">Drop Window End</label>
			<div class="hp-field">
				<input
					id="field-hunt_until"
					name="hunt_until"
					type="datetime-local"
					class="hp-input"
					bind:value={huntUntil}
					data-testid="snipe-create-until"
				/>
			</div>
		</div>

		<div class="hp-formrow">
			<label for="field-deadline_at">Give Up At</label>
			<div class="hp-field">
				<input
					id="field-deadline_at"
					name="deadline_at"
					type="datetime-local"
					class="hp-input"
					bind:value={deadline}
					data-testid="snipe-create-deadline"
				/>
				<div class="hp-help" style="margin-top:3px">
					Leave empty to use the number of days below.
				</div>
			</div>
		</div>

		<div class="hp-formrow">
			<label for="field-window_days">Or Run For (days)</label>
			<div class="hp-field">
				<input
					id="field-window_days"
					name="window_days"
					type="number"
					min="1"
					max="365"
					class="hp-input"
					bind:value={windowDays}
					data-testid="snipe-create-window-days"
				/>
			</div>
		</div>

		<div>
			<div class="hp-formrow">
				<label for="field-ns1">Nameserver 1</label>
				<div class="hp-field">
					<input
						id="field-ns1"
						name="ns1"
						class="hp-input"
						bind:value={ns1}
						placeholder="ns1.example.net"
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
						placeholder="ns2.example.net"
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
		<div class="hp-help" style="margin-bottom:14px">
			Nameservers are optional; empty uses the system default.
		</div>

		<div style="display:flex;gap:8px">
			<button
				type="submit"
				class="hp-btn hp-btn-primary"
				disabled={submitting}
				aria-busy={submitting}
				data-testid="snipe-create-submit"
			>
				{#if submitting}<i class="fas fa-spinner fa-spin"></i>{:else}<i class="fas fa-crosshairs"
					></i>{/if}Start Hunt
			</button>
			<a href="/admin/snipes" class="hp-btn">Cancel</a>
		</div>
	</form>
</div>
