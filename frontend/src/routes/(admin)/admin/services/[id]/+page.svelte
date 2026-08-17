<script lang="ts">
	import { enhance } from '$app/forms';
	import HpModal from '$lib/components/hp/HpModal.svelte';
	import { formatIDR } from '$lib/money';
	import { toast } from '$lib/stores/toast.svelte';
	import type { SubmitFunction } from '@sveltejs/kit';
	import { untrack } from 'svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	const service = $derived(data.service);

	const CANCELLATION_MODE_LABEL: Record<string, string> = {
		immediate: 'immediate',
		end_of_term: 'end-of-term'
	};

	// Confirm-only dialogs.
	let provisionOpen = $state(false);
	let unsuspendOpen = $state(false);
	let terminateOpen = $state(false);
	// Input modals.
	let suspendOpen = $state(false);
	let changePackageOpen = $state(false);
	let upgradeOpen = $state(false);
	let changePasswordOpen = $state(false);

	let suspendReason = $state('');
	let newProductId = $state('');
	let upgradeProductId = $state('');
	let upgradeCycle = $state('');
	let newPassword = $state('');

	// Plain-field editor state (domain, username, server, billing cycle,
	// recurring amount, dates, suspend reason, notes). Product/password/status
	// stay out of this form - they go through the dedicated Change Package /
	// Change Password / module-action flows below, which also sync the panel.
	let domainField = $state(untrack(() => data.service.domain ?? ''));
	let usernameField = $state(untrack(() => data.service.username ?? ''));
	let serverIdField = $state(
		untrack(() => (data.service.server_id ? String(data.service.server_id) : ''))
	);
	let billingCycleField = $state(untrack(() => data.service.billing_cycle));
	let recurringAmountField = $state(untrack(() => data.service.recurring_amount ?? 0));
	let registrationDateField = $state(
		untrack(() => data.service.registration_date?.slice(0, 10) ?? '')
	);
	let nextDue = $state(untrack(() => data.service.next_due_date?.slice(0, 10) ?? ''));
	let terminatedAtField = $state(untrack(() => data.service.terminated_at?.slice(0, 10) ?? ''));
	let suspendReasonField = $state(untrack(() => data.service.suspend_reason ?? ''));
	let notes = $state(untrack(() => data.service.notes ?? ''));

	let actionLoading = $state<string | null>(null);

	let provisionForm = $state<HTMLFormElement>();
	let unsuspendForm = $state<HTMLFormElement>();
	let terminateForm = $state<HTMLFormElement>();

	const ERROR_FALLBACKS: Record<string, string> = {
		'adminops.services.reasonRequired': 'A suspend reason is required.',
		'adminops.services.productRequired': 'Please select a product.',
		'adminops.services.cycleRequired': 'Please select a billing cycle.',
		'adminops.services.passwordRequired': 'A new password is required.'
	};

	const errorText = $derived(
		form?.errorMessage ??
			(form?.errorKey ? (ERROR_FALLBACKS[form.errorKey] ?? form.errorKey) : null)
	);

	interface UpgradeResultView {
		applied: boolean;
		prorated_diff: number;
		credit_issued: number;
		invoice?: { id: number; invoice_number: string } | null;
	}

	function upgradeToastMessage(r: UpgradeResultView): string {
		if (r.applied) {
			return r.credit_issued > 0
				? `Package changed immediately. ${formatIDR(r.credit_issued)} credited to the client.`
				: 'Package changed immediately.';
		}
		const inv = r.invoice ? ` #${r.invoice.invoice_number}` : '';
		return `Prorated upgrade invoice${inv} created (${formatIDR(r.prorated_diff)}) — awaiting payment.`;
	}

	function submitHandler(action: string, onDone?: () => void): SubmitFunction {
		return () => {
			actionLoading = action;
			return async ({ result, update }) => {
				actionLoading = null;
				if (result.type === 'success') {
					onDone?.();
					const data = result.data as { result?: UpgradeResultView } | undefined;
					if (action === 'upgrade' && data?.result) {
						toast.success(upgradeToastMessage(data.result));
					} else {
						toast.success('Action completed successfully');
					}
				} else if (result.type === 'failure') {
					const d = result.data as { errorKey?: string; errorMessage?: string } | undefined;
					toast.error(
						d?.errorMessage ??
							(d?.errorKey ? (ERROR_FALLBACKS[d.errorKey] ?? d.errorKey) : 'Something went wrong')
					);
				}
				// reset:false - otherwise SvelteKit's default post-submit behavior
				// calls the underlying <form>'s native reset(), which blanks the
				// bound overview fields in the DOM even though their $state values
				// (only synced from `data` once at mount) still hold the just-saved
				// values - the fields would visibly go empty until a manual reload.
				await update({ reset: false });
			};
		};
	}

	// The backend Upgrade endpoint accepts spec selections for custom-spec
	// (configurable) products, but this admin form has no spec-knob UI yet -
	// keep them out of this picker so there's no dead-end (the client-area
	// upgrade flow covers them); Change Package still lists everything.
	const upgradeableProducts = $derived(data.products.filter((p) => !p.configurable));

	const CYCLE_LABELS: Record<string, string> = {
		one_time: 'One-Time',
		monthly: 'Monthly',
		quarterly: 'Quarterly',
		semiannually: 'Semi-Annually',
		annually: 'Annually',
		biennially: 'Biennially'
	};
</script>

<svelte:head>
	<title>{service.product_name ?? `Service #${service.id}`} — HostPanel Admin</title>
</svelte:head>

<div
	style="display:flex;flex-wrap:wrap;align-items:center;justify-content:space-between;gap:12px;margin-bottom:14px"
>
	<div>
		<h1 class="hp-h1" style="margin-bottom:2px">
			{service.product_name ?? `Product #${service.product_id}`}
			<span
				data-testid="service-status"
				class={`hp-badge ${service.status}`}
				style="margin-left:8px;vertical-align:middle">{service.status}</span
			>
		</h1>
		<p style="color:#888;font-size:12px;margin:0">{service.domain || `#${service.id}`}</p>
	</div>
	<a
		href={`/admin/clients/${service.client_id}`}
		data-testid="service-client-link"
		style="color:#337ab7;font-weight:600;font-size:13px"
	>
		View Client <i class="fas fa-arrow-right"></i>
	</a>
</div>

{#if errorText}
	<div class="hp-alert-red" data-testid="service-action-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{errorText}
	</div>
{/if}

{#if service.pending_upgrade}
	<div class="hp-alert-yellow" data-testid="service-pending-upgrade-banner">
		<i class="fas fa-info-circle" style="margin-right:8px"></i>
		A package change is pending, awaiting payment of invoice #{service.pending_upgrade.invoice_id}.
		<a
			href={`/admin/invoices/${service.pending_upgrade.invoice_id}`}
			style="font-weight:600;text-decoration:underline;margin-left:4px">View Invoice</a
		>
	</div>
{/if}

{#if service.pending_cancellation}
	<div class="hp-alert-yellow" data-testid="service-pending-cancellation-banner">
		<i class="fas fa-info-circle" style="margin-right:8px"></i>
		{#if service.pending_cancellation.mode === 'immediate'}
			The client's immediate cancellation is being processed - no action needed, it will complete
			automatically once the worker picks it up.
		{:else}
			A client cancellation request ({CANCELLATION_MODE_LABEL[service.pending_cancellation.mode] ??
				service.pending_cancellation.mode}) is pending review.
		{/if}
		<a
			href="/admin/services/cancellation-requests"
			style="font-weight:600;text-decoration:underline;margin-left:4px">Review Requests</a
		>
	</div>
{/if}

<div class="hp-grid3">
	<!-- Overview / edit form -->
	<section class="hp-panel">
		<div class="hp-panel-hd"><span class="title">Service Overview</span></div>
		{#key service.id}
			<form
				method="POST"
				action="?/update"
				style="padding:14px 16px"
				data-testid="service-update-form"
				use:enhance={submitHandler('update')}
			>
				<div class="hp-formrow">
					<label for="field-product">Product</label>
					<div class="hp-field">
						<div class="val">{service.product_name ?? `#${service.product_id}`}</div>
					</div>
				</div>
				<div class="hp-formrow">
					<label for="field-domain">Domain</label>
					<div class="hp-field">
						<input
							id="field-domain"
							name="domain"
							class="hp-input"
							bind:value={domainField}
							data-testid="service-field-domain"
						/>
					</div>
				</div>
				<div class="hp-formrow">
					<label for="field-server_id">Server</label>
					<div class="hp-field">
						<select
							id="field-server_id"
							name="server_id"
							class="hp-select"
							bind:value={serverIdField}
							data-testid="service-field-server_id"
						>
							<option value="">No server assigned</option>
							{#each data.servers as s (s.id)}
								<option value={String(s.id)}>{s.name}</option>
							{/each}
						</select>
					</div>
				</div>
				<div class="hp-formrow">
					<label for="field-username">Username</label>
					<div class="hp-field">
						<input
							id="field-username"
							name="username"
							class="hp-input"
							bind:value={usernameField}
							data-testid="service-field-username"
						/>
						<div class="hp-help" style="margin-top:3px">
							Real control panels can reject usernames containing words like "test" as reserved,
							regardless of numeric suffixes — pick something that avoids them.
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
							bind:value={registrationDateField}
							data-testid="service-field-registration_date"
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
							bind:value={nextDue}
							data-testid="service-field-next_due_date"
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
							bind:value={billingCycleField}
							data-testid="service-field-billing_cycle"
						>
							{#each Object.entries(CYCLE_LABELS) as [value, label] (value)}
								<option {value}>{label}</option>
							{/each}
						</select>
					</div>
				</div>
				<div class="hp-formrow">
					<label for="field-recurring_amount">Recurring Amount</label>
					<div class="hp-field">
						<input
							id="field-recurring_amount"
							name="recurring_amount"
							type="number"
							class="hp-input"
							bind:value={recurringAmountField}
							data-testid="service-field-recurring_amount"
						/>
						<div class="hp-help" style="margin-top:3px">Rupiah, whole numbers only.</div>
					</div>
				</div>
				<div class="hp-formrow">
					<label for="field-terminated_at">Termination Date</label>
					<div class="hp-field">
						<input
							id="field-terminated_at"
							name="terminated_at"
							type="date"
							class="hp-input"
							bind:value={terminatedAtField}
							data-testid="service-field-terminated_at"
						/>
					</div>
				</div>
				<div class="hp-formrow">
					<label for="field-suspend_reason">Suspend Reason</label>
					<div class="hp-field">
						<input
							id="field-suspend_reason"
							name="suspend_reason"
							class="hp-input"
							bind:value={suspendReasonField}
							data-testid="service-field-suspend_reason"
						/>
						{#if service.suspend_reason}
							<div
								class="hp-help"
								style="color:#d9534f;margin-top:3px"
								data-testid="service-suspend-reason-text"
							>
								Current: {service.suspend_reason}
							</div>
						{/if}
					</div>
				</div>
				<div class="hp-formrow top">
					<label for="field-notes">Notes</label>
					<div class="hp-field" style="flex:1 1 520px">
						<textarea
							id="field-notes"
							name="notes"
							class="hp-textarea"
							rows="3"
							bind:value={notes}
							placeholder="Internal notes about this service"></textarea>
					</div>
				</div>
				<div class="hp-form-actions" style="justify-content:flex-end">
					<span data-testid="service-update-submit">
						<button
							type="submit"
							class="hp-btn hp-btn-primary"
							disabled={actionLoading === 'update'}
						>
							{actionLoading === 'update' ? 'Saving…' : 'Save Changes'}
						</button>
					</span>
				</div>
			</form>
		{/key}
	</section>

	<!-- Module actions -->
	<section class="hp-panel">
		<div class="hp-panel-hd"><span class="title">Module Actions</span></div>
		<div style="display:flex;flex-direction:column;gap:8px;padding:14px 16px">
			{#if service.status === 'pending'}
				<span data-testid="service-action-provision">
					<button
						type="button"
						class="hp-btn hp-btn-green"
						style="width:100%"
						onclick={() => (provisionOpen = true)}
					>
						<i class="fas fa-play"></i>Create Service
					</button>
				</span>
			{/if}
			{#if service.status === 'active'}
				<span data-testid="service-action-suspend">
					<button
						type="button"
						class="hp-btn hp-btn-orange"
						style="width:100%"
						onclick={() => (suspendOpen = true)}
					>
						<i class="fas fa-pause"></i>Suspend
					</button>
				</span>
				<span data-testid="service-action-change-package">
					<button
						type="button"
						class="hp-btn"
						style="width:100%"
						onclick={() => (changePackageOpen = true)}
					>
						<i class="fas fa-exchange-alt"></i>Change Package
					</button>
				</span>
				<span data-testid="service-action-upgrade">
					<button
						type="button"
						class="hp-btn"
						style="width:100%"
						onclick={() => (upgradeOpen = true)}
					>
						<i class="fas fa-arrow-up"></i>Upgrade (Billed)
					</button>
				</span>
				<span data-testid="service-action-change-password">
					<button
						type="button"
						class="hp-btn"
						style="width:100%"
						onclick={() => (changePasswordOpen = true)}
					>
						<i class="fas fa-key"></i>Change Password
					</button>
				</span>
			{/if}
			{#if service.status === 'suspended'}
				<span data-testid="service-action-unsuspend">
					<button
						type="button"
						class="hp-btn hp-btn-green"
						style="width:100%"
						onclick={() => (unsuspendOpen = true)}
					>
						<i class="fas fa-play"></i>Unsuspend
					</button>
				</span>
			{/if}
			{#if service.status === 'active' || service.status === 'suspended'}
				<span data-testid="service-action-terminate">
					<button
						type="button"
						class="hp-btn hp-btn-danger"
						style="width:100%"
						onclick={() => (terminateOpen = true)}
					>
						<i class="fas fa-trash-alt"></i>Terminate
					</button>
				</span>
			{/if}
			{#if service.status === 'terminated' || service.status === 'cancelled'}
				<p style="color:#999;font-size:13px;margin:0">No actions available.</p>
			{/if}
		</div>
	</section>
</div>

<!-- Hidden forms for confirm-only module actions -->
<form
	method="POST"
	action="?/provision"
	class="hidden"
	bind:this={provisionForm}
	use:enhance={submitHandler('provision', () => (provisionOpen = false))}
></form>
<form
	method="POST"
	action="?/unsuspend"
	class="hidden"
	bind:this={unsuspendForm}
	use:enhance={submitHandler('unsuspend', () => (unsuspendOpen = false))}
></form>
<form
	method="POST"
	action="?/terminate"
	class="hidden"
	bind:this={terminateForm}
	use:enhance={submitHandler('terminate', () => (terminateOpen = false))}
></form>

<HpModal open={provisionOpen} title="Create Service" onClose={() => (provisionOpen = false)}>
	<p style="color:#555;margin-bottom:16px">
		Provision this service now? This runs the module's create action.
	</p>
	<div style="display:flex;justify-content:flex-end;gap:8px">
		<button
			type="button"
			class="hp-btn"
			disabled={actionLoading === 'provision'}
			onclick={() => (provisionOpen = false)}
		>
			Cancel
		</button>
		<button
			type="button"
			class="hp-btn hp-btn-primary"
			disabled={actionLoading === 'provision'}
			onclick={() => provisionForm?.requestSubmit()}
		>
			{#if actionLoading === 'provision'}<i class="fas fa-spinner fa-spin"></i>{/if}Create Service
		</button>
	</div>
</HpModal>

<HpModal open={unsuspendOpen} title="Unsuspend Service" onClose={() => (unsuspendOpen = false)}>
	<p style="color:#555;margin-bottom:16px">Unsuspend this service and restore access?</p>
	<div style="display:flex;justify-content:flex-end;gap:8px">
		<button
			type="button"
			class="hp-btn"
			disabled={actionLoading === 'unsuspend'}
			onclick={() => (unsuspendOpen = false)}
		>
			Cancel
		</button>
		<button
			type="button"
			class="hp-btn hp-btn-primary"
			disabled={actionLoading === 'unsuspend'}
			onclick={() => unsuspendForm?.requestSubmit()}
		>
			{#if actionLoading === 'unsuspend'}<i class="fas fa-spinner fa-spin"></i>{/if}Unsuspend
		</button>
	</div>
</HpModal>

<HpModal open={terminateOpen} title="Terminate Service" onClose={() => (terminateOpen = false)}>
	<p style="color:#555;margin-bottom:16px">
		Terminate this service? This action is permanent and cannot be undone.
	</p>
	<div style="display:flex;justify-content:flex-end;gap:8px">
		<button
			type="button"
			class="hp-btn"
			disabled={actionLoading === 'terminate'}
			onclick={() => (terminateOpen = false)}
		>
			Cancel
		</button>
		<button
			type="button"
			class="hp-btn hp-btn-danger"
			disabled={actionLoading === 'terminate'}
			onclick={() => terminateForm?.requestSubmit()}
		>
			{#if actionLoading === 'terminate'}<i class="fas fa-spinner fa-spin"></i>{/if}Terminate
		</button>
	</div>
</HpModal>

<!-- Suspend modal (reason) -->
<HpModal open={suspendOpen} title="Suspend Service" onClose={() => (suspendOpen = false)}>
	<form
		class="hp-modalform"
		method="POST"
		action="?/suspend"
		use:enhance={submitHandler('suspend', () => (suspendOpen = false))}
	>
		<div class="hp-formrow top">
			<label for="field-reason">Suspend Reason</label>
			<div class="hp-field">
				<textarea
					id="field-reason"
					name="reason"
					class="hp-textarea"
					rows="3"
					bind:value={suspendReason}
					placeholder="Reason for suspending this service"
					required></textarea>
			</div>
		</div>
		<div style="display:flex;justify-content:flex-end;gap:8px;margin-top:8px">
			<button type="button" class="hp-btn" onclick={() => (suspendOpen = false)}>Cancel</button>
			<span data-testid="service-suspend-submit">
				<button type="submit" class="hp-btn hp-btn-danger" disabled={actionLoading === 'suspend'}>
					{actionLoading === 'suspend' ? 'Suspending…' : 'Suspend'}
				</button>
			</span>
		</div>
	</form>
</HpModal>

<!-- Change package modal -->
<HpModal
	open={changePackageOpen}
	title="Change Package"
	onClose={() => (changePackageOpen = false)}
>
	<form
		class="hp-modalform"
		method="POST"
		action="?/changePackage"
		use:enhance={submitHandler('changePackage', () => (changePackageOpen = false))}
	>
		<div class="hp-formrow">
			<label for="field-product_id">New Product</label>
			<div class="hp-field">
				<select
					id="field-product_id"
					name="product_id"
					class="hp-select"
					bind:value={newProductId}
					required
				>
					<option value="" disabled>Select a product…</option>
					{#each data.products as p (p.id)}
						<option value={String(p.id)}>{p.name}</option>
					{/each}
				</select>
			</div>
		</div>
		<div style="display:flex;justify-content:flex-end;gap:8px;margin-top:8px">
			<button type="button" class="hp-btn" onclick={() => (changePackageOpen = false)}
				>Cancel</button
			>
			<span data-testid="service-change-package-submit">
				<button
					type="submit"
					class="hp-btn hp-btn-primary"
					disabled={actionLoading === 'changePackage'}
				>
					{actionLoading === 'changePackage' ? 'Changing…' : 'Change Package'}
				</button>
			</span>
		</div>
	</form>
</HpModal>

<!-- Upgrade (billed) modal -->
<HpModal open={upgradeOpen} title="Upgrade Package (Billed)" onClose={() => (upgradeOpen = false)}>
	<p style="color:#666;font-size:13px;margin:0 0 12px">
		Unlike Change Package, this bills the client: the prorated difference for the remainder
		of the current cycle is invoiced (upgrade) or credited to their account (downgrade) —
		the same flow the client's own "Upgrade" button uses.
	</p>
	<form
		class="hp-modalform"
		method="POST"
		action="?/upgrade"
		use:enhance={submitHandler('upgrade', () => (upgradeOpen = false))}
	>
		<div class="hp-formrow">
			<label for="field-upgrade_product_id">New Product</label>
			<div class="hp-field">
				<select
					id="field-upgrade_product_id"
					name="product_id"
					class="hp-select"
					bind:value={upgradeProductId}
					data-testid="service-upgrade-field-product"
					required
				>
					<option value="" disabled>Select a product…</option>
					{#each upgradeableProducts as p (p.id)}
						<option value={String(p.id)}>{p.name}</option>
					{/each}
				</select>
				<div class="hp-help" style="margin-top:3px">
					Custom-spec products aren't listed here — clients configure those via the client-area upgrade flow.
				</div>
			</div>
		</div>
		<div class="hp-formrow">
			<label for="field-upgrade_cycle">Billing Cycle</label>
			<div class="hp-field">
				<select
					id="field-upgrade_cycle"
					name="cycle"
					class="hp-select"
					bind:value={upgradeCycle}
					data-testid="service-upgrade-field-cycle"
					required
				>
					<option value="" disabled>Select a cycle…</option>
					{#each Object.entries(CYCLE_LABELS).filter(([v]) => v !== 'one_time') as [value, label] (value)}
						<option {value}>{label}</option>
					{/each}
				</select>
			</div>
		</div>
		<div style="display:flex;justify-content:flex-end;gap:8px;margin-top:8px">
			<button type="button" class="hp-btn" onclick={() => (upgradeOpen = false)}>Cancel</button>
			<span data-testid="service-upgrade-submit">
				<button type="submit" class="hp-btn hp-btn-primary" disabled={actionLoading === 'upgrade'}>
					{actionLoading === 'upgrade' ? 'Submitting…' : 'Upgrade'}
				</button>
			</span>
		</div>
	</form>
</HpModal>

<!-- Change password modal -->
<HpModal
	open={changePasswordOpen}
	title="Change Password"
	onClose={() => (changePasswordOpen = false)}
>
	<form
		class="hp-modalform"
		method="POST"
		action="?/changePassword"
		use:enhance={submitHandler('changePassword', () => {
			changePasswordOpen = false;
			newPassword = '';
		})}
	>
		<div class="hp-formrow">
			<label for="field-password">New Password</label>
			<div class="hp-field">
				<input
					id="field-password"
					name="password"
					type="password"
					class="hp-input"
					bind:value={newPassword}
					autocomplete="new-password"
					required
				/>
				<div class="hp-help" style="margin-top:3px">
					The client will need this new password to log in to their control panel.
				</div>
			</div>
		</div>
		<div style="display:flex;justify-content:flex-end;gap:8px;margin-top:8px">
			<button type="button" class="hp-btn" onclick={() => (changePasswordOpen = false)}
				>Cancel</button
			>
			<span data-testid="service-change-password-submit">
				<button
					type="submit"
					class="hp-btn hp-btn-primary"
					disabled={actionLoading === 'changePassword'}
				>
					{actionLoading === 'changePassword' ? 'Saving…' : 'Change Password'}
				</button>
			</span>
		</div>
	</form>
</HpModal>

<style>
	.hidden {
		display: none;
	}
	.hp-grid3 {
		display: grid;
		grid-template-columns: minmax(0, 2fr) minmax(0, 1fr);
		gap: 14px;
	}
	@media (max-width: 900px) {
		.hp-grid3 {
			grid-template-columns: 1fr;
		}
	}
	.val {
		color: #333;
	}
	.hp-btn-orange {
		background: #f89406;
		border-color: #f89406;
		color: #fff;
	}
	.hp-modalform .hp-formrow {
		flex-direction: column;
		align-items: flex-start;
		gap: 4px;
		padding: 6px 0;
	}
	.hp-modalform .hp-formrow > label {
		width: auto;
		text-align: left;
		font-weight: 600;
		color: #555;
	}
	.hp-modalform .hp-field {
		flex: 1 1 auto;
		width: 100%;
	}
</style>
