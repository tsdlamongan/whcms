<script lang="ts">
	import { enhance } from '$app/forms';
	import DateText from '$lib/components/DateText.svelte';
	import HpModal from '$lib/components/hp/HpModal.svelte';
	import MoneyText from '$lib/components/MoneyText.svelte';
	import { toast } from '$lib/stores/toast.svelte';
	import type { SubmitFunction } from '@sveltejs/kit';
	import { untrack } from 'svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	const domain = $derived(data.domain);

	let syncOpen = $state(false);
	let renewOpen = $state(false);
	let actionLoading = $state<string | null>(null);

	let syncForm = $state<HTMLFormElement>();
	let renewForm = $state<HTMLFormElement>();

	const initialNs = untrack(() => data.domain.nameservers ?? []);
	let ns1 = $state(initialNs[0] ?? '');
	let ns2 = $state(initialNs[1] ?? '');
	let ns3 = $state(initialNs[2] ?? '');
	let ns4 = $state(initialNs[3] ?? '');

	let status = $state(untrack(() => data.domain.status));
	let autoRenew = $state(untrack(() => data.domain.auto_renew));
	let registrationDate = $state(untrack(() => (data.domain.registration_date ?? '').slice(0, 10)));
	let expiryDate = $state(untrack(() => (data.domain.expiry_date ?? '').slice(0, 10)));
	let nextDueDate = $state(untrack(() => (data.domain.next_due_date ?? '').slice(0, 10)));
	let recurringAmount = $state(untrack(() => data.domain.recurring_amount ?? 0));
	let billingCycle = $state(untrack(() => data.domain.billing_cycle));

	const cycleOptions = [
		{ value: 'one_time', label: 'One Time' },
		{ value: 'monthly', label: 'Monthly' },
		{ value: 'quarterly', label: 'Quarterly' },
		{ value: 'semiannually', label: 'Semi-Annually' },
		{ value: 'annually', label: 'Annually' },
		{ value: 'biennially', label: 'Biennially' }
	];

	const statusOptions = [
		{ value: 'pending', label: 'Pending' },
		{ value: 'active', label: 'Active' },
		{ value: 'pending_transfer', label: 'Pending Transfer' },
		{ value: 'expired', label: 'Expired' },
		{ value: 'cancelled', label: 'Cancelled' }
	];

	const ERROR_FALLBACKS: Record<string, string> = {
		'adminops.domains.nsRequired': 'At least 2 nameservers are required.'
	};

	const errorText = $derived(
		form?.errorMessage ??
			(form?.errorKey ? (ERROR_FALLBACKS[form.errorKey] ?? form.errorKey) : null)
	);

	function statusClass(s: string): string {
		if (s === 'active') return 'active';
		if (s === 'pending' || s === 'pending_transfer') return 'pending';
		if (s === 'expired') return 'terminated';
		return 'cancelled';
	}

	function submitHandler(action: string, onDone?: () => void): SubmitFunction {
		return () => {
			actionLoading = action;
			return async ({ result, update }) => {
				actionLoading = null;
				if (result.type === 'success') {
					onDone?.();
					toast.success('Action completed successfully');
				} else if (result.type === 'failure') {
					const d = result.data as { errorKey?: string; errorMessage?: string } | undefined;
					toast.error(
						d?.errorMessage ??
							(d?.errorKey ? (ERROR_FALLBACKS[d.errorKey] ?? d.errorKey) : 'Something went wrong')
					);
				}
				await update();
			};
		};
	}
</script>

<svelte:head>
	<title>{domain.name} — HostPanel Admin</title>
</svelte:head>

<div
	style="display:flex;flex-wrap:wrap;align-items:center;justify-content:space-between;gap:12px;margin-bottom:14px"
>
	<div>
		<h1 class="hp-h1" style="margin-bottom:2px">
			{domain.name}
			<span
				data-testid="domain-status"
				class={`hp-badge ${statusClass(domain.status)}`}
				style="margin-left:8px;vertical-align:middle">{domain.status}</span
			>
		</h1>
		<p style="color:#888;font-size:12px;margin:0">Domain registration details and management</p>
	</div>
	<div style="display:flex;gap:8px">
		<span data-testid="domain-sync-button">
			<button type="button" class="hp-btn" onclick={() => (syncOpen = true)}>
				<i class="fas fa-sync-alt"></i>Sync Status
			</button>
		</span>
		<span data-testid="domain-renew-button">
			<button type="button" class="hp-btn hp-btn-primary" onclick={() => (renewOpen = true)}>
				<i class="fas fa-redo"></i>Renew Domain
			</button>
		</span>
	</div>
</div>

{#if errorText}
	<div class="hp-alert-red" data-testid="domain-action-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{errorText}
	</div>
{/if}

{#if form?.success && form.action === 'renew' && form.invoiceId}
	<div class="hp-alert-yellow" data-testid="domain-renew-invoice-link">
		<i class="fas fa-check-circle" style="margin-right:8px"></i>Domain renewed successfully.
		<a href={`/admin/invoices/${form.invoiceId}`} style="font-weight:600;text-decoration:underline"
			>View Invoice #{form.invoiceId}</a
		>
	</div>
{/if}

<div class="hp-grid2">
	<section class="hp-panel">
		<div class="hp-panel-hd"><span class="title">Domain Overview</span></div>
		<div class="hp-fieldgrid">
			<div>
				<div class="lbl">Client</div>
				<div class="val">
					<a
						class="cell-link"
						href={`/admin/clients/${domain.client_id}`}
						data-testid="domain-client-link">{domain.client_name ?? `#${domain.client_id}`}</a
					>
				</div>
			</div>
			<div>
				<div class="lbl">Registrar</div>
				<div class="val">{domain.registrar_name ?? `#${domain.registrar_id}`}</div>
			</div>
			<div>
				<div class="lbl">Registration Date</div>
				<div class="val"><DateText value={domain.registration_date} /></div>
			</div>
			<div>
				<div class="lbl">Expiry Date</div>
				<div class="val" data-testid="domain-expiry"><DateText value={domain.expiry_date} /></div>
			</div>
			<div>
				<div class="lbl">Next Due Date</div>
				<div class="val"><DateText value={domain.next_due_date} /></div>
			</div>
			<div>
				<div class="lbl">Recurring Amount</div>
				<div class="val">
					<MoneyText amount={domain.recurring_amount ?? 0} />
					<span style="color:#999;font-size:12px">/ {domain.billing_cycle}</span>
				</div>
			</div>
			<div>
				<div class="lbl">Auto Renew</div>
				<div class="val">{domain.auto_renew ? 'Yes' : 'No'}</div>
			</div>
			<div>
				<div class="lbl">ID Protection</div>
				<div class="val">{domain.id_protection ? 'Yes' : 'No'}</div>
			</div>
		</div>
	</section>

	<section class="hp-panel">
		<div class="hp-panel-hd"><span class="title">Status &amp; Settings</span></div>
		<form
			method="POST"
			action="?/settings"
			style="padding:14px 16px"
			data-testid="domain-settings-form"
			use:enhance={submitHandler('settings')}
		>
			<div class="hp-formrow">
				<label for="field-status">Status</label>
				<div class="hp-field">
					<select id="field-status" name="status" class="hp-select" bind:value={status}>
						{#each statusOptions as opt (opt.value)}
							<option value={opt.value}>{opt.label}</option>
						{/each}
					</select>
				</div>
			</div>
			<div class="hp-formrow">
				<label for="field-auto_renew">&nbsp;</label>
				<div class="hp-field" style="flex:1 1 auto">
					<label class="hp-checkline" style="padding:0">
						<input
							id="field-auto_renew"
							name="auto_renew"
							type="checkbox"
							bind:checked={autoRenew}
						/>
						Auto Renew
					</label>
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
						data-testid="domain-settings-expiry"
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
						data-testid="domain-settings-next-due"
					/>
				</div>
			</div>
			<div class="hp-formrow">
				<label for="field-recurring_amount">Renewal Price (IDR)</label>
				<div class="hp-field">
					<input
						id="field-recurring_amount"
						name="recurring_amount"
						type="number"
						min="0"
						step="1"
						class="hp-input"
						bind:value={recurringAmount}
						data-testid="domain-settings-amount"
					/>
				</div>
			</div>
			<div class="hp-formrow">
				<label for="field-billing_cycle">Billing Cycle</label>
				<div class="hp-field">
					<select
						id="field-billing_cycle"
						name="billing_cycle"
						class="hp-select"
						bind:value={billingCycle}
					>
						{#each cycleOptions as opt (opt.value)}
							<option value={opt.value}>{opt.label}</option>
						{/each}
					</select>
				</div>
			</div>
			<div class="hp-form-actions" style="justify-content:flex-end">
				<span data-testid="domain-settings-submit">
					<button
						type="submit"
						class="hp-btn hp-btn-primary"
						disabled={actionLoading === 'settings'}
					>
						{actionLoading === 'settings' ? 'Saving…' : 'Save Changes'}
					</button>
				</span>
			</div>
		</form>
	</section>

	<section class="hp-panel full">
		<div class="hp-panel-hd"><span class="title">Nameservers</span></div>
		<form
			method="POST"
			action="?/nameservers"
			style="padding:14px 16px"
			data-testid="domain-ns-form"
			use:enhance={submitHandler('nameservers')}
		>
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
							required
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
							required
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
				<span data-testid="domain-ns-submit">
					<button
						type="submit"
						class="hp-btn hp-btn-primary"
						disabled={actionLoading === 'nameservers'}
					>
						{actionLoading === 'nameservers' ? 'Saving…' : 'Save Changes'}
					</button>
				</span>
			</div>
		</form>
	</section>
</div>

<!-- Hidden forms for confirm-only actions -->
<form
	method="POST"
	action="?/sync"
	class="hidden"
	bind:this={syncForm}
	use:enhance={submitHandler('sync', () => (syncOpen = false))}
></form>
<form
	method="POST"
	action="?/renew"
	class="hidden"
	bind:this={renewForm}
	use:enhance={submitHandler('renew', () => (renewOpen = false))}
></form>

<HpModal open={syncOpen} title="Sync Domain Status" onClose={() => (syncOpen = false)}>
	<p style="color:#555;margin-bottom:16px">Sync this domain's status with the registrar now?</p>
	<div style="display:flex;justify-content:flex-end;gap:8px">
		<button
			type="button"
			class="hp-btn"
			disabled={actionLoading === 'sync'}
			onclick={() => (syncOpen = false)}
		>
			Cancel
		</button>
		<button
			type="button"
			class="hp-btn hp-btn-primary"
			disabled={actionLoading === 'sync'}
			onclick={() => syncForm?.requestSubmit()}
		>
			{#if actionLoading === 'sync'}<i class="fas fa-spinner fa-spin"></i>{/if}Sync Now
		</button>
	</div>
</HpModal>

<HpModal open={renewOpen} title="Renew Domain" onClose={() => (renewOpen = false)}>
	<p style="color:#555;margin-bottom:16px">
		Renew this domain now? An invoice will be generated for the renewal.
	</p>
	<div style="display:flex;justify-content:flex-end;gap:8px">
		<button
			type="button"
			class="hp-btn"
			disabled={actionLoading === 'renew'}
			onclick={() => (renewOpen = false)}
		>
			Cancel
		</button>
		<button
			type="button"
			class="hp-btn hp-btn-primary"
			disabled={actionLoading === 'renew'}
			onclick={() => renewForm?.requestSubmit()}
		>
			{#if actionLoading === 'renew'}<i class="fas fa-spinner fa-spin"></i>{/if}Renew Now
		</button>
	</div>
</HpModal>

<style>
	.hidden {
		display: none;
	}
	.hp-grid2 {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 14px;
	}
	.hp-grid2 .full {
		grid-column: 1 / -1;
	}
	@media (max-width: 900px) {
		.hp-grid2 {
			grid-template-columns: 1fr;
		}
	}
	.hp-fieldgrid {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
		gap: 14px 20px;
		padding: 14px 16px;
	}
	.hp-fieldgrid .lbl {
		font-size: 11px;
		text-transform: uppercase;
		letter-spacing: 0.3px;
		color: #999;
		font-weight: 600;
		margin-bottom: 3px;
	}
	.hp-fieldgrid .val {
		color: #333;
	}
</style>
