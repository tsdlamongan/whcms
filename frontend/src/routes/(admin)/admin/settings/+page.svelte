<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import { invalidateAll } from '$app/navigation';
	import { toast } from '$lib/stores/toast.svelte';
	import type { SubmitFunction } from '@sveltejs/kit';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	type TabId = 'general' | 'billing' | 'automation' | 'mail' | 'tickets' | 'security';
	const tabs: { id: TabId; label: string }[] = [
		{ id: 'general', label: 'General' },
		{ id: 'billing', label: 'Billing' },
		{ id: 'automation', label: 'Automation' },
		{ id: 'mail', label: 'Mail' },
		{ id: 'tickets', label: 'Tickets' },
		{ id: 'security', label: 'Security' }
	];

	let active = $state<TabId>('general');
	let saving = $state<Record<string, boolean>>({});

	const fieldErrors = $derived<Record<string, string>>(form?.fieldErrors ?? {});
	const errorText = $derived(form?.errorMessage ?? null);
	const errorTab = $derived(form?.tab ?? null);

	function mk(tab: string): SubmitFunction {
		return () => {
			saving[tab] = true;
			return async ({ result, update }) => {
				saving[tab] = false;
				if (result.type === 'success') {
					toast.success('Changes saved successfully');
					await invalidateAll();
				}
				await update({ reset: false });
			};
		};
	}

	// The test send is a diagnostic, not a settings write: keep the result in
	// the inline banner (it carries the relay's error text) and never reload
	// the page, which would drop it.
	const testEmailSubmit: SubmitFunction = () => {
		saving.testEmail = true;
		return async ({ result, update }) => {
			saving.testEmail = false;
			if (result.type === 'success') toast.success('Test email sent');
			await update({ reset: false });
		};
	};
</script>

{#snippet textField(
	label: string,
	key: string,
	type: string,
	value: string | number,
	required = false,
	hint = ''
)}
	<div class="hp-formrow">
		<label for={`field-${key}`}
			>{label}{#if required}<span style="color:#d9534f"> *</span>{/if}</label
		>
		<div class="hp-field">
			<input
				id={`field-${key}`}
				name={key}
				{type}
				{value}
				{required}
				class="hp-input"
				class:hp-input-error={!!fieldErrors[key]}
				data-testid={`settings-field-${key}`}
			/>
			{#if fieldErrors[key]}
				<div style="color:#d9534f;font-size:12px;margin-top:3px">{fieldErrors[key]}</div>
			{:else if hint}
				<div class="hp-help" style="margin-top:3px">{hint}</div>
			{/if}
		</div>
	</div>
{/snippet}

{#snippet checkField(label: string, key: string, checked: boolean, hint = '')}
	<div class="hp-formrow">
		<label for={`field-${key}`}>&nbsp;</label>
		<div class="hp-field" style="flex:1 1 auto">
			<label class="hp-checkline" style="padding:0">
				<input
					id={`field-${key}`}
					name={key}
					type="checkbox"
					{checked}
					data-testid={`settings-field-${key}`}
				/>
				{label}
			</label>
			{#if hint}<div class="hp-help" style="margin-top:3px">{hint}</div>{/if}
		</div>
	</div>
{/snippet}

<svelte:head>
	<title>General Settings — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">General Settings</h1>

{#if data.listError}
	<div class="hp-alert-red" data-testid="settings-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

<div class="hp-tabs">
	{#each tabs as tab (tab.id)}
		<button
			type="button"
			class="hp-tab"
			class:active={active === tab.id}
			data-testid={`settings-tab-${tab.id}`}
			onclick={() => (active = tab.id)}
		>
			{tab.label}
		</button>
	{/each}
</div>

<div class="hp-tabpanel">
	<div class:hidden={active !== 'general'}>
		{#if errorText && errorTab === 'general'}
			<div class="hp-alert-red" data-testid="settings-error-general">{errorText}</div>
		{/if}
		<form
			method="POST"
			action="?/saveGeneral"
			data-testid="settings-form-general"
			use:enhance={mk('general')}
		>
			<div style="max-width:1000px">
				{@render textField(
					'Company Name',
					'company.name',
					'text',
					data.settings.company.name,
					true,
					'Your company name as it appears throughout the system'
				)}
				{@render textField(
					'Email Address',
					'company.email',
					'email',
					data.settings.company.email,
					true,
					'The default sender address used for emails'
				)}
				<div class="hp-formrow top">
					<label for="field-company.address">Pay To Text</label>
					<div class="hp-field" style="flex:0 0 520px">
						<textarea
							id="field-company.address"
							name="company.address"
							rows="3"
							class="hp-textarea"
							data-testid="settings-field-company.address">{data.settings.company.address}</textarea
						>
					</div>
				</div>
				{@render textField(
					'Logo Key',
					'company.logo_key',
					'text',
					data.settings.company.logo_key,
					false,
					'Object storage key of your company logo'
				)}
				<div class="hp-form-actions">
					<button
						type="submit"
						class="hp-btn hp-btn-primary"
						disabled={saving.general}
						data-testid="settings-save-general">Save Changes</button
					>
				</div>
			</div>
		</form>
	</div>

	<div class:hidden={active !== 'billing'}>
		{#if errorText && errorTab === 'billing'}
			<div class="hp-alert-red" data-testid="settings-error-billing">{errorText}</div>
		{/if}
		<form
			method="POST"
			action="?/saveBilling"
			data-testid="settings-form-billing"
			use:enhance={mk('billing')}
		>
			<div style="max-width:1000px">
				{@render checkField('Enable Tax', 'billing.tax_enabled', data.settings.billing.tax_enabled)}
				{@render textField(
					'Tax Rate (%)',
					'billing.tax_rate',
					'number',
					data.settings.billing.tax_rate,
					false,
					'Applied to taxable invoice items'
				)}
				{@render checkField(
					'Prices are tax-inclusive',
					'billing.tax_inclusive',
					data.settings.billing.tax_inclusive
				)}
				{@render textField(
					'Invoice Due Days',
					'billing.invoice_due_days',
					'number',
					data.settings.billing.invoice_due_days
				)}
				{@render textField(
					'Renewal Lead Days',
					'billing.renewal_lead_days',
					'number',
					data.settings.billing.renewal_lead_days
				)}
				{@render checkField(
					'Enable Late Fees',
					'billing.late_fee_enabled',
					data.settings.billing.late_fee_enabled
				)}
				{@render textField(
					'Late Fee Amount',
					'billing.late_fee_amount',
					'number',
					data.settings.billing.late_fee_amount
				)}
				{@render textField(
					'Reminder Days',
					'billing.reminder_days',
					'text',
					data.settings.billing.reminder_days.join(','),
					false,
					'Comma-separated days before due date'
				)}
				{@render textField(
					'Overdue Reminder Days',
					'billing.overdue_reminder_days',
					'text',
					data.settings.billing.overdue_reminder_days.join(','),
					false,
					'Comma-separated days after due date'
				)}
				<div class="hp-form-actions">
					<button
						type="submit"
						class="hp-btn hp-btn-primary"
						disabled={saving.billing}
						data-testid="settings-save-billing">Save Changes</button
					>
				</div>
			</div>
		</form>
	</div>

	<div class:hidden={active !== 'automation'}>
		{#if errorText && errorTab === 'automation'}
			<div class="hp-alert-red" data-testid="settings-error-automation">{errorText}</div>
		{/if}
		<form
			method="POST"
			action="?/saveAutomation"
			data-testid="settings-form-automation"
			use:enhance={mk('automation')}
		>
			<div style="max-width:1000px">
				{@render textField(
					'Suspend After Days',
					'automation.suspend_after_days',
					'number',
					data.settings.automation.suspend_after_days,
					false,
					'Days overdue before a service is suspended'
				)}
				{@render textField(
					'Terminate After Days',
					'automation.terminate_after_days',
					'number',
					data.settings.automation.terminate_after_days,
					false,
					'Days overdue before a service is terminated'
				)}
				<div class="hp-form-actions">
					<button
						type="submit"
						class="hp-btn hp-btn-primary"
						disabled={saving.automation}
						data-testid="settings-save-automation">Save Changes</button
					>
				</div>
			</div>
		</form>
	</div>

	<div class:hidden={active !== 'mail'}>
		{#if errorText && errorTab === 'mail'}
			<div class="hp-alert-red" data-testid="settings-error-mail">{errorText}</div>
		{/if}
		<div class="hp-info">
			<i class="fas fa-info-circle" style="margin-right:8px"></i>The mail transport (<code
				>MAIL_DRIVER</code
			>, <code>SMTP_HOST</code>, <code>SMTP_PORT</code>,
			<code>SMTP_ENCRYPTION</code>, <code>SMTP_AUTH</code>) is configured via environment variables.
			The sender identity below is stored in the database and applies immediately.
		</div>
		<form
			method="POST"
			action="?/saveMail"
			data-testid="settings-form-mail"
			use:enhance={mk('mail')}
		>
			<div style="max-width:1000px">
				{@render textField(
					'From Name',
					'mail.from_name',
					'text',
					data.settings.mail.from_name,
					true
				)}
				{@render textField(
					'From Email',
					'mail.from_email',
					'email',
					data.settings.mail.from_email,
					true,
					'Envelope sender for every outbound email. Leave it valid — an empty or malformed address fails every send.'
				)}
				<div class="hp-form-actions">
					<button
						type="submit"
						class="hp-btn hp-btn-primary"
						disabled={saving.mail}
						data-testid="settings-save-mail">Save Changes</button
					>
				</div>
			</div>
		</form>

		<!-- SMTP smoke test: sends synchronously so relay errors surface here. -->
		<div class="hp-panel" style="margin-top:20px;max-width:1000px">
			<div class="hp-panel-hd"><span class="title">Send Test Email</span></div>
			<div style="padding:14px 12px">
				<p class="hp-help" style="margin:0 0 12px">
					Delivers a diagnostic message through the configured mail driver right now, bypassing the
					queue, so a wrong host, port, TLS mode, credential or sender address reports back
					immediately.
				</p>

				{#if form?.testEmailSent}
					<div class="hp-alert-green" data-testid="settings-test-email-success">
						<i class="fas fa-check-circle" style="margin-right:8px"></i>Test email sent to
						{form.testEmailTo}. Check the inbox and the Email Log.
					</div>
				{:else if form?.testEmailError}
					<div class="hp-alert-red" data-testid="settings-test-email-error">
						<i class="fas fa-exclamation-triangle" style="margin-right:8px"
						></i>{form.testEmailError}
					</div>
				{/if}

				<form
					method="POST"
					action="?/sendTestEmail"
					data-testid="settings-form-test-email"
					use:enhance={testEmailSubmit}
				>
					<div class="hp-formrow">
						<label for="field-test-email-to">Recipient</label>
						<div class="hp-field">
							<input
								id="field-test-email-to"
								name="test_email_to"
								type="email"
								class="hp-input"
								required
								placeholder="you@example.com"
								value={form?.testEmailTo ?? ''}
								data-testid="settings-test-email-input"
							/>
						</div>
					</div>
					<div class="hp-form-actions">
						<button
							type="submit"
							class="hp-btn"
							disabled={saving.testEmail}
							aria-busy={saving.testEmail}
							data-testid="settings-test-email-send"
						>
							<i class="fas fa-paper-plane"></i>{saving.testEmail ? 'Sending…' : 'Send Test Email'}
						</button>
					</div>
				</form>
			</div>
		</div>
	</div>

	<div class:hidden={active !== 'tickets'}>
		{#if errorText && errorTab === 'tickets'}
			<div class="hp-alert-red" data-testid="settings-error-tickets">{errorText}</div>
		{/if}
		<form
			method="POST"
			action="?/saveTickets"
			data-testid="settings-form-tickets"
			use:enhance={mk('tickets')}
		>
			<div style="max-width:1000px">
				{@render textField(
					'Allowed Extensions',
					'tickets.allowed_extensions',
					'text',
					data.settings.tickets.allowed_extensions.join(','),
					false,
					'Comma-separated file extensions'
				)}
				{@render textField(
					'Max Attachment (MB)',
					'tickets.max_attachment_mb',
					'number',
					data.settings.tickets.max_attachment_mb
				)}
				<div class="hp-form-actions">
					<button
						type="submit"
						class="hp-btn hp-btn-primary"
						disabled={saving.tickets}
						data-testid="settings-save-tickets">Save Changes</button
					>
				</div>
			</div>
		</form>
	</div>

	<div class:hidden={active !== 'security'}>
		{#if errorText && errorTab === 'security'}
			<div class="hp-alert-red" data-testid="settings-error-security">{errorText}</div>
		{/if}
		<div class="hp-info">
			<i class="fas fa-shield-halved" style="margin-right:8px"></i>
			CAPTCHA (Cloudflare Turnstile) protects login, registration, and order checkout from bots. Set your
			public Site Key here; the Secret Key is configured via the <code>TURNSTILE_SECRET_KEY</code> environment
			variable.
		</div>
		<form
			method="POST"
			action="?/saveSecurity"
			data-testid="settings-form-security"
			use:enhance={mk('security')}
		>
			<div style="max-width:1000px">
				<input
					type="hidden"
					name="security.captcha_provider"
					value={data.settings.security.captcha_provider || 'turnstile'}
				/>
				{@render checkField(
					'Enable CAPTCHA',
					'security.captcha_enabled',
					data.settings.security.captcha_enabled,
					'Require a Turnstile challenge on login, registration, and checkout'
				)}
				{@render textField(
					'Turnstile Site Key',
					'security.captcha_site_key',
					'text',
					data.settings.security.captcha_site_key,
					false,
					'Cloudflare Turnstile public site key (e.g. 0x4AAA…). Leave blank to keep CAPTCHA off.'
				)}
				{@render checkField(
					'Require Email Verification',
					'security.require_email_verification',
					data.settings.security.require_email_verification,
					'Clients must verify their email address before they can check out. Disable only for testing.'
				)}
				<div class="hp-form-actions">
					<button
						type="submit"
						class="hp-btn hp-btn-primary"
						disabled={saving.security}
						data-testid="settings-save-security">Save Changes</button
					>
				</div>
			</div>
		</form>
	</div>
</div>

<style>
	.hidden {
		display: none;
	}
</style>
