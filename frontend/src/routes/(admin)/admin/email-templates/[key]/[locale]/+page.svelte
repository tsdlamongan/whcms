<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import { toast } from '$lib/stores/toast.svelte';
	import { untrack } from 'svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	let subject = $state(untrack(() => form?.values?.subject ?? data.template?.subject ?? ''));
	let bodyHtml = $state(untrack(() => form?.values?.body_html ?? data.template?.body_html ?? ''));
	let bodyText = $state(untrack(() => form?.values?.body_text ?? data.template?.body_text ?? ''));

	let saving = $state(false);
	let previewing = $state(false);

	const fieldErrors = $derived<Record<string, string>>(form?.fieldErrors ?? {});
	const preview = $derived(form?.preview ?? null);

	function fieldErrorText(key?: string): string | undefined {
		if (!key) return undefined;
		return key === 'adminsupport.common.requiredField' ? 'This field is required.' : key;
	}

	// Go text/template + html/template placeholders (the backend renders with a
	// map[string]any - see internal/modules/notifications/dto.go). A variable
	// not supplied for this key renders as "<no value>", so only the two groups
	// below are safe to use in this template.
	const globalVariables = [
		'{{.CompanyName}}',
		'{{.CompanyAddress}}',
		'{{.CompanyEmail}}',
		'{{.FrontendURL}}',
		'{{.Year}}',
		'{{.Name}}',
		'{{.Email}}'
	];

	/** Per-key variables, mirroring the table in notifications/dto.go. */
	const KEY_VARIABLES: Record<string, string[]> = {
		_layout: ['{{.Content}}'],
		verify_email: ['{{.VerifyURL}}'],
		reset_password: ['{{.ResetURL}}'],
		invoice_created: ['{{.InvoiceNumber}}', '{{.Total}}', '{{.DueDate}}', '{{.InvoiceURL}}'],
		invoice_reminder: ['{{.InvoiceNumber}}', '{{.Total}}', '{{.DueDate}}', '{{.InvoiceURL}}'],
		invoice_overdue: ['{{.InvoiceNumber}}', '{{.Total}}', '{{.DueDate}}', '{{.InvoiceURL}}'],
		payment_received: ['{{.InvoiceNumber}}', '{{.Amount}}'],
		service_activated: [
			'{{.ServiceName}}',
			'{{.Domain}}',
			'{{.Username}}',
			'{{.Password}}',
			'{{.PanelURL}}'
		],
		service_suspended: ['{{.ServiceName}}', '{{.Domain}}', '{{.Reason}}'],
		service_unsuspended: ['{{.ServiceName}}', '{{.Domain}}'],
		service_terminated: ['{{.ServiceName}}', '{{.Domain}}'],
		service_renewed: ['{{.ServiceName}}', '{{.Domain}}', '{{.NextDueDate}}'],
		domain_registered: ['{{.Domain}}', '{{.ExpiryDate}}'],
		domain_renewed: ['{{.Domain}}', '{{.ExpiryDate}}'],
		ticket_opened: ['{{.TicketNumber}}', '{{.Subject}}', '{{.TicketURL}}'],
		ticket_replied: ['{{.TicketNumber}}', '{{.Subject}}', '{{.TicketURL}}'],
		admin_alert: ['{{.Subject}}', '{{.Detail}}']
	};

	const keyVariables = $derived(KEY_VARIABLES[data.template?.key ?? ''] ?? []);
	const isLayout = $derived(data.template?.key === '_layout');
</script>

<svelte:head>
	<title>Edit Template — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Edit Email Template</h1>

{#if data.notFound || !data.template}
	<div class="hp-alert-red">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>
		{data.loadError ?? 'Failed to load data.'}
	</div>
{:else}
	<p class="hp-lead">
		<span style="font-family:monospace;font-weight:600;color:#333" data-testid="template-key"
			>{data.template.key}</span
		>
		<span class="tpl-locale" data-testid="template-locale">{data.template.locale}</span>
	</p>

	{#if data.loadError}
		<div class="hp-alert-red">
			<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.loadError}
		</div>
	{/if}

	{#if form?.errorMessage}
		<div class="hp-alert-red" data-testid="template-save-error">
			<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{form.errorMessage}
		</div>
	{/if}

	<div class="hp-main2">
		<!-- Editor -->
		<section class="hp-panel">
			<div class="hp-panel-hd"><span class="title">Template</span></div>
			<div style="padding:14px 12px">
				<form
					method="POST"
					action="?/save"
					data-testid="template-form"
					use:enhance={({ action }) => {
						const isPreview = action.search.includes('preview');
						if (isPreview) previewing = true;
						else saving = true;
						return async ({ result, update }) => {
							previewing = false;
							saving = false;
							if (result.type === 'success' && !isPreview) {
								toast.success('Template saved successfully.');
							}
							await update({ reset: false });
						};
					}}
				>
					<div class="hp-formrow top">
						<label for="field-subject">Subject<span style="color:#d9534f"> *</span></label>
						<div class="hp-field" style="flex:1 1 auto">
							<input
								id="field-subject"
								name="subject"
								class="hp-input"
								class:hp-input-error={!!fieldErrors.subject}
								bind:value={subject}
								required
								data-testid="template-field-subject"
							/>
							{#if fieldErrors.subject}
								<div style="color:#d9534f;font-size:12px;margin-top:3px">
									{fieldErrorText(fieldErrors.subject)}
								</div>
							{/if}
						</div>
					</div>

					<div class="hp-formrow top">
						<label for="template-body-html">Body (HTML)<span style="color:#d9534f"> *</span></label>
						<div class="hp-field" style="flex:1 1 auto">
							<textarea
								id="template-body-html"
								name="body_html"
								rows="14"
								bind:value={bodyHtml}
								data-testid="template-body-input"
								class="hp-textarea"
								class:hp-input-error={!!fieldErrors.body_html}
								style="font-family:monospace;font-size:12px"></textarea>
							{#if fieldErrors.body_html}
								<div style="color:#d9534f;font-size:12px;margin-top:3px">
									{fieldErrorText(fieldErrors.body_html)}
								</div>
							{/if}
							<div
								style="margin-top:8px;background:#f5f5f5;border-radius:4px;padding:8px 10px;font-size:12px;color:#666"
								data-testid="template-variables"
							>
								{#if keyVariables.length > 0}
									<div style="font-weight:600;margin-bottom:4px">
										Variables for <code>{data.template.key}</code>:
									</div>
									<div
										style="display:flex;flex-wrap:wrap;gap:6px;font-family:monospace;margin-bottom:8px"
									>
										{#each keyVariables as v (v)}
											<code
												style="background:#fff;border:1px solid #ddd;border-radius:3px;padding:1px 5px"
												>{v}</code
											>
										{/each}
									</div>
								{/if}
								<div style="font-weight:600;margin-bottom:4px">Available in every template:</div>
								<div style="display:flex;flex-wrap:wrap;gap:6px;font-family:monospace">
									{#each globalVariables as v (v)}
										<code
											style="background:#fff;border:1px solid #ddd;border-radius:3px;padding:1px 5px"
											>{v}</code
										>
									{/each}
								</div>
							</div>

							{#if isLayout}
								<div class="hp-info" style="margin-top:8px;font-size:12px">
									<i class="fas fa-info-circle" style="margin-right:6px"></i>This is the
									<strong>global layout</strong>: it wraps the body of every other email.
									<code>{'{{.Content}}'}</code> is the slot each template body is rendered into and must
									stay present. Editing this restyles all outbound email at once.
								</div>
							{:else}
								<div class="hp-help" style="margin-top:6px">
									The shared header, footer and mobile styles come from the
									<a href="/admin/email-templates/_layout/{data.template.locale}">global layout</a>
									— write only the message body here.
								</div>
							{/if}
						</div>
					</div>

					<div class="hp-formrow top">
						<label for="field-body_text">Body (plain text)</label>
						<div class="hp-field" style="flex:1 1 auto">
							<textarea
								id="field-body_text"
								name="body_text"
								rows="5"
								class="hp-textarea"
								bind:value={bodyText}
								data-testid="template-field-body_text"></textarea>
							<div class="hp-help" style="margin-top:3px">
								Optional. Used as a fallback for email clients without HTML.
							</div>
						</div>
					</div>

					<div class="hp-form-actions" style="justify-content:flex-end">
						<button
							type="submit"
							formaction="?/preview"
							class="hp-btn"
							disabled={previewing || saving}
							aria-busy={previewing}
							data-testid="template-preview-button"
						>
							<i class="fas fa-eye"></i>{previewing ? 'Rendering…' : 'Preview'}
						</button>
						<button
							type="submit"
							class="hp-btn hp-btn-primary"
							disabled={saving}
							aria-busy={saving}
							data-testid="template-save-button"
						>
							<i class="fas fa-save"></i>{saving ? 'Saving…' : 'Save'}
						</button>
					</div>
				</form>
			</div>
		</section>

		<!-- Preview -->
		<section class="hp-panel">
			<div class="hp-panel-hd">
				<span class="title">Template Preview</span>
			</div>
			<div style="padding:14px 12px">
				{#if preview?.subject}
					<p
						style="margin:0 0 10px;color:#666;font-size:12px"
						data-testid="template-preview-subject"
					>
						{preview.subject}
					</p>
				{/if}
				{#if form?.previewErrorMessage}
					<div class="hp-alert-red">
						<i class="fas fa-exclamation-triangle" style="margin-right:8px"
						></i>{form.previewErrorMessage}
					</div>
				{:else if preview?.html}
					<iframe
						title="Template Preview"
						srcdoc={preview.html}
						sandbox=""
						style="height:32rem;width:100%;border:1px solid #ddd;border-radius:4px;background:#fff"
						data-testid="template-preview-frame"
					></iframe>
				{:else}
					<p style="text-align:center;color:#999;padding:40px 8px">
						Click Preview to render the last saved version with sample data.
					</p>
				{/if}
			</div>
		</section>
	</div>
{/if}

<style>
	.tpl-locale {
		display: inline-block;
		margin-left: 8px;
		padding: 1px 7px 2px;
		border-radius: 3px;
		background: #d9edf7;
		border: 1px solid #bce8f1;
		color: #31708f;
		font-size: 11px;
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.3px;
	}
</style>
