<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import { toast } from '$lib/stores/toast.svelte';
	import { untrack } from 'svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	const duitkuForm = $derived(form?.action === 'duitku' ? form : undefined);
	const manualForm = $derived(form?.action === 'manual' ? form : undefined);

	let merchantCode = $state(untrack(() => duitkuForm?.merchantCode ?? data.duitku.merchantCode));
	let mode = $state(untrack(() => duitkuForm?.mode ?? data.duitku.mode));
	let baseUrl = $state(untrack(() => duitkuForm?.baseUrl ?? data.duitku.baseUrl));
	let apiKey = $state('');
	let clearApiKey = $state(false);
	let submittingDuitku = $state(false);

	const configured = $derived(merchantCode.trim().length > 0);

	const duitkuErrorText = $derived(
		duitkuForm?.errorKey
			? duitkuForm.errorKey === 'adminBilling.gateways.errMerchantCode'
				? 'Merchant code is required'
				: 'Invalid mode'
			: (duitkuForm?.errorMessage ?? null)
	);

	interface AccountRow {
		bankName: string;
		accountNumber: string;
		accountHolder: string;
	}

	function initialAccounts(): AccountRow[] {
		const rows = data.manual.accounts.map((a) => ({
			bankName: a.bank_name,
			accountNumber: a.account_number,
			accountHolder: a.account_holder
		}));
		return rows.length > 0 ? rows : [{ bankName: '', accountNumber: '', accountHolder: '' }];
	}

	let manualEnabled = $state(untrack(() => data.manual.enabled));
	let manualInstructions = $state(untrack(() => data.manual.instructions));
	let accounts = $state<AccountRow[]>(untrack(initialAccounts));
	let submittingManual = $state(false);

	function addAccount() {
		accounts.push({ bankName: '', accountNumber: '', accountHolder: '' });
	}

	function removeAccount(index: number) {
		accounts.splice(index, 1);
		if (accounts.length === 0) addAccount();
	}
</script>

<svelte:head>
	<title>Payment Gateways — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Payment Gateways</h1>

<div class="hp-info" style="display:flex;align-items:center;justify-content:space-between;gap:12px">
	<span
		>Looking to activate a new payment gateway? Visit <b>Apps &amp; Integrations</b> for a full list of
		gateway integrations.</span
	>
	<a class="hp-btn" href="/admin/settings"
		><i class="fas fa-arrow-right"></i>Visit Apps &amp; Integrations</a
	>
</div>

{#if data.errorMessage}
	<div class="hp-alert-red">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.errorMessage}
	</div>
{/if}
{#if duitkuErrorText}
	<div class="hp-alert-red">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{duitkuErrorText}
	</div>
{/if}

<div class="hp-panel" style="max-width:680px">
	<div class="hp-panel-hd">
		<span class="title" style="display:flex;align-items:center;gap:10px">
			<i class="fas fa-arrows-alt" style="color:#bbb"></i>Duitku
			<span class={`hp-badge ${configured ? 'active' : 'inactive'}`}
				>{configured ? 'Configured' : 'Not Configured'}</span
			>
		</span>
		<span data-testid="gateway-apikey-indicator">
			{#if data.duitku.apiKeySet}
				<span class="hp-stext green" style="font-size:12px"
					><i class="fas fa-check" style="margin-right:4px"></i>API Key Set</span
				>
			{:else}
				<span class="hp-stext orange" style="font-size:12px"
					><i class="fas fa-exclamation-triangle" style="margin-right:4px"></i>API Key Missing</span
				>
			{/if}
		</span>
	</div>

	<div style="padding:18px">
		<form
			method="POST"
			action="?/duitku"
			use:enhance={() => {
				submittingDuitku = true;
				return async ({ result, update }) => {
					submittingDuitku = false;
					if (result.type === 'success') toast.success('Changes saved successfully');
					await update({ reset: false });
				};
			}}
		>
			<div class="hp-formrow" data-testid="gateway-merchant-code">
				<label for="gw-merchant">Merchant Code <span style="color:#d9534f">*</span></label>
				<div class="hp-field">
					<input
						id="gw-merchant"
						class="hp-input"
						type="text"
						name="merchant_code"
						bind:value={merchantCode}
						placeholder="DXXXX"
						required
					/>
					<div class="hp-help" style="margin-top:3px">Your Duitku merchant code</div>
				</div>
			</div>

			<div class="hp-formrow" data-testid="gateway-mode">
				<label for="gw-mode">Mode</label>
				<div class="hp-field">
					<select id="gw-mode" class="hp-select" name="mode" bind:value={mode} required>
						<option value="sandbox">Sandbox</option>
						<option value="production">Production</option>
					</select>
				</div>
			</div>

			<div class="hp-formrow" data-testid="gateway-base-url">
				<label for="gw-base-url">Base URL</label>
				<div class="hp-field">
					<input
						id="gw-base-url"
						class="hp-input"
						type="text"
						name="base_url"
						bind:value={baseUrl}
						placeholder="Auto (derived from Mode)"
					/>
					<div class="hp-help" style="margin-top:3px">
						Custom endpoint — point this gateway at a different base URL (e.g. the local mock
						server) without restarting anything. Leave blank to derive it from Mode
						(sandbox.duitku.com / passport.duitku.com).
					</div>
				</div>
			</div>

			<div class="hp-formrow" data-testid="gateway-api-key">
				<label for="gw-api-key">API Key</label>
				<div class="hp-field">
					<input
						id="gw-api-key"
						class="hp-input"
						type="password"
						name="api_key"
						autocomplete="off"
						bind:value={apiKey}
						disabled={clearApiKey}
						placeholder={data.duitku.apiKeySet
							? '•••••••• (leave blank to keep unchanged)'
							: 'Not set'}
					/>
					<div class="hp-help" style="margin-top:3px">
						Stored encrypted; the DUITKU_API_KEY environment variable is the fallback when left
						blank.
					</div>
					{#if data.duitku.apiKeySet}
						<label class="hp-checkline" style="margin-top:6px">
							<input
								type="checkbox"
								name="clear_api_key"
								bind:checked={clearApiKey}
								data-testid="gateway-clear-api-key"
							/>
							Clear stored key
						</label>
					{/if}
				</div>
			</div>

			<div class="hp-form-actions" style="justify-content:flex-end">
				<button type="submit" class="hp-btn hp-btn-primary" disabled={submittingDuitku}>
					<span data-testid="gateway-save">Save Changes</span>
				</button>
			</div>
		</form>
	</div>
</div>

<div class="hp-panel" style="max-width:680px;margin-top:18px">
	<div class="hp-panel-hd">
		<span class="title" style="display:flex;align-items:center;gap:10px">
			<i class="fas fa-university" style="color:#bbb"></i>Manual / Bank Transfer
			<span class={`hp-badge ${manualEnabled ? 'active' : 'inactive'}`}
				>{manualEnabled ? 'Enabled' : 'Disabled'}</span
			>
		</span>
	</div>

	{#if manualForm?.errorMessage}
		<div class="hp-alert-red" style="margin:12px 18px 0">
			<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{manualForm.errorMessage}
		</div>
	{/if}

	<div style="padding:18px">
		<form
			method="POST"
			action="?/manual"
			use:enhance={() => {
				submittingManual = true;
				return async ({ result, update }) => {
					submittingManual = false;
					if (result.type === 'success') toast.success('Changes saved successfully');
					await update({ reset: false });
				};
			}}
		>
			<div class="hp-formrow" data-testid="gateway-manual-enabled">
				<label for="gw-manual-enabled">Enabled</label>
				<div class="hp-field">
					<label class="hp-checkline">
						<input
							id="gw-manual-enabled"
							type="checkbox"
							name="enabled"
							bind:checked={manualEnabled}
						/>
						Offer Bank Transfer as a payment method at checkout
					</label>
					<div class="hp-help" style="margin-top:3px">
						Requires at least one bank account below to actually appear at checkout.
					</div>
				</div>
			</div>

			<div
				style="font-size:12px;font-weight:700;color:#666;text-transform:uppercase;margin:14px 0 6px"
			>
				Bank Accounts
			</div>
			<div data-testid="gateway-manual-accounts">
				{#each accounts as account, i (i)}
					<div
						class="hp-formrow"
						style="border:1px solid #e5e5e5;border-radius:3px;padding:10px;margin-bottom:8px"
						data-testid={`gateway-manual-account-${i}`}
					>
						<div
							style="display:grid;grid-template-columns:1fr 1fr 1fr auto;gap:8px;align-items:end"
						>
							<div class="hp-field">
								<label for={`gw-manual-bank-${i}`}>Bank Name</label>
								<input
									id={`gw-manual-bank-${i}`}
									class="hp-input"
									type="text"
									name={`bank_name_${i}`}
									bind:value={account.bankName}
									placeholder="BCA"
									data-testid={`gateway-manual-bank-name-${i}`}
								/>
							</div>
							<div class="hp-field">
								<label for={`gw-manual-acct-${i}`}>Account Number</label>
								<input
									id={`gw-manual-acct-${i}`}
									class="hp-input"
									type="text"
									name={`account_number_${i}`}
									bind:value={account.accountNumber}
									placeholder="1234567890"
									data-testid={`gateway-manual-account-number-${i}`}
								/>
							</div>
							<div class="hp-field">
								<label for={`gw-manual-holder-${i}`}>Account Holder</label>
								<input
									id={`gw-manual-holder-${i}`}
									class="hp-input"
									type="text"
									name={`account_holder_${i}`}
									bind:value={account.accountHolder}
									placeholder="PT WHCMS Hosting"
									data-testid={`gateway-manual-account-holder-${i}`}
								/>
							</div>
							<button
								type="button"
								class="hp-btn"
								onclick={() => removeAccount(i)}
								aria-label="Remove account"
								data-testid={`gateway-manual-remove-account-${i}`}
							>
								<i class="fas fa-trash"></i>
							</button>
						</div>
					</div>
				{/each}
			</div>
			<div class="hp-form-actions" style="justify-content:flex-start;margin-bottom:14px">
				<button
					type="button"
					class="hp-btn"
					onclick={addAccount}
					data-testid="gateway-manual-add-account"
				>
					<i class="fas fa-plus" style="margin-right:4px"></i>Add Account
				</button>
			</div>

			<div class="hp-formrow" data-testid="gateway-manual-instructions">
				<label for="gw-manual-instructions">Instructions</label>
				<div class="hp-field">
					<textarea
						id="gw-manual-instructions"
						class="hp-input"
						name="instructions"
						rows="3"
						bind:value={manualInstructions}
						placeholder="Include the invoice number in your transfer note."></textarea>
				</div>
			</div>

			<div class="hp-form-actions" style="justify-content:flex-end">
				<button type="submit" class="hp-btn hp-btn-primary" disabled={submittingManual}>
					<span data-testid="gateway-manual-save">Save Changes</span>
				</button>
			</div>
		</form>
	</div>
</div>
