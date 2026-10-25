<script lang="ts">
	import { enhance } from '$app/forms';
	import DateText from '$lib/components/DateText.svelte';
	import { documentRequirementKeys, tldOf } from '$lib/domainDocuments';
	import { t } from '$lib/i18n';
	import { formatIDR } from '$lib/money';
	import { toast } from '$lib/stores/toast.svelte';
	import type { SubmitFunction } from '@sveltejs/kit';
	import { needsDocuments, needsEmailVerification, type DomainRow } from '../shared';

	/** The `documents` action result (the registrar-hosted upload link lives only here). */
	interface DocumentsForm {
		section?: string;
		uploadUrl?: string;
		errorMessage?: string;
		errorKey?: string;
	}

	let { domain, form }: { domain: DomainRow; form: DocumentsForm | null | undefined } = $props();

	const documents = $derived(needsDocuments(domain));
	const email = $derived(needsEmailVerification(domain));
	const rejected = $derived(domain.status === 'rejected');
	const suspended = $derived(domain.status === 'suspended');

	const docsForm = $derived(form?.section === 'documents' ? form : null);
	const uploadUrl = $derived(docsForm?.uploadUrl ?? null);
	const docsError = $derived(
		docsForm?.errorKey ? t(docsForm.errorKey) : (docsForm?.errorMessage ?? null)
	);

	const deadlines = $derived(domain.verification_deadlines);
	const requirements = $derived(documentRequirementKeys(domain.name));
	const reason = $derived(domain.status_reason);
	const bounced = $derived(documents && domain.document_rejected_at !== null);

	const stateText = $derived(
		email
			? t(
					`clientDomains.docs.emailState.${domain.verification_status === 'verifying' ? 'verifying' : 'waiting'}`
				)
			: t(`clientDomains.docs.state.${domain.verification_status || 'waiting'}`)
	);

	// A rejected domain's refund outcome (documented policy: no refund when the
	// required documents were never verified).
	const refunded = $derived(domain.refunded_at !== null && domain.refund_amount > 0);
	const documentFailure = $derived(
		domain.document_required && domain.verification_status !== 'active'
	);

	let linkPending = $state(false);
	let refreshPending = $state(false);
	let resendPending = $state(false);

	const linkEnhance: SubmitFunction = () => {
		linkPending = true;
		return async ({ update }) => {
			linkPending = false;
			await update({ reset: false });
		};
	};

	const refreshEnhance: SubmitFunction = () => {
		refreshPending = true;
		return async ({ result, update }) => {
			refreshPending = false;
			if (result.type === 'success') toast.success(t('clientDomains.docs.refreshed'));
			await update({ reset: false });
		};
	};

	const resendEnhance: SubmitFunction = () => {
		resendPending = true;
		return async ({ result, update }) => {
			resendPending = false;
			if (result.type === 'success') toast.success(t('clientDomains.docs.resent'));
			await update({ reset: false });
		};
	};
</script>

{#if rejected}
	<div class="ca-alert-danger mb-5" data-testid="domain-rejected-panel">
		<p class="font-semibold" style="margin-bottom:4px">
			{t('clientDomains.docs.rejectedPanel.title')}
		</p>
		<p style="margin-bottom:4px">
			{t('clientDomains.docs.rejectedPanel.body', { domain: domain.name })}
		</p>
		{#if reason}
			<p style="margin-bottom:4px" data-testid="domain-rejected-reason">
				{t('clientDomains.docs.rejectedPanel.reason', { reason })}
			</p>
		{/if}
		<p style="margin-bottom:0" data-testid="domain-refund-outcome">
			{#if refunded}
				<span data-testid="domain-refunded" data-amount={domain.refund_amount}>
					{t('clientDomains.docs.rejectedPanel.refunded', {
						amount: formatIDR(domain.refund_amount)
					})}
				</span>
			{:else if documentFailure}
				<span data-testid="domain-no-refund-documents">
					{t('clientDomains.docs.rejectedPanel.noRefundDocuments')}
				</span>
			{:else}
				<span data-testid="domain-no-refund">
					{t('clientDomains.docs.rejectedPanel.noRefundOther')}
				</span>
			{/if}
		</p>
	</div>
{:else if documents || email}
	<div
		class="ca-card mb-5"
		data-testid="domain-verification-panel"
		data-kind={documents ? 'documents' : 'email'}
	>
		<h2 class="ca-card-header">
			{documents ? t('clientDomains.docs.title') : t('clientDomains.docs.titleEmail')}
		</h2>
		<div class="ca-card-body">
			<p class="ca-muted mb-3">
				{documents
					? t('clientDomains.docs.intro', { tld: `.${tldOf(domain.name)}` })
					: t('clientDomains.docs.introEmail')}
			</p>

			{#if suspended}
				<div class="ca-alert-danger" data-testid="domain-suspended-alert">
					<p class="font-semibold" style="margin-bottom:2px">
						{t('clientDomains.docs.suspendedTitle')}
					</p>
					{reason
						? t('clientDomains.docs.suspendedWithReason', { reason })
						: t('clientDomains.docs.suspended')}
				</div>
			{/if}

			{#if bounced}
				<div class="ca-alert-danger" data-testid="document-rejected-alert">
					<p class="font-semibold" style="margin-bottom:2px">
						{t('clientDomains.docs.rejectedTitle')}
					</p>
					{reason
						? t('clientDomains.docs.rejectedWithReason', { reason })
						: t('clientDomains.docs.rejected')}
				</div>
			{/if}

			<dl class="mb-4 grid grid-cols-1 gap-x-8 gap-y-2 text-sm sm:grid-cols-2">
				<div class="flex items-center justify-between gap-4 border-b border-gray-100 pb-2">
					<dt class="text-gray-500">{t('clientDomains.docs.stateLabel')}</dt>
					<dd
						class="font-medium text-gray-800"
						data-testid="verification-state"
						data-state={domain.verification_status}
					>
						{stateText}
					</dd>
				</div>
			</dl>

			{#if deadlines}
				<div class="mb-4" data-testid="verification-deadlines">
					<p class="mb-1 text-sm font-semibold text-gray-700">
						{t('clientDomains.docs.deadlinesTitle')}
					</p>
					<ul class="text-sm text-gray-700" style="list-style:none;padding:0;margin:0">
						{#if deadlines.upload_by}
							<li data-testid="deadline-upload-by">
								{t('clientDomains.docs.uploadBy')}:
								<strong><DateText value={deadlines.upload_by} /></strong>
							</li>
						{/if}
						<li data-testid="deadline-suspend-on">
							{deadlines.kind === 'documents'
								? t('clientDomains.docs.suspendOn')
								: t('clientDomains.docs.suspendOnEmail')}:
							<strong><DateText value={deadlines.suspend_on} /></strong>
						</li>
						{#if deadlines.delete_on}
							<li data-testid="deadline-delete-on">
								{t('clientDomains.docs.deleteOn')}:
								<strong><DateText value={deadlines.delete_on} /></strong>
							</li>
						{/if}
					</ul>
				</div>
			{/if}

			{#if documents && requirements.length > 0}
				<div class="mb-4" data-testid="document-requirements">
					<p class="mb-1 text-sm font-semibold text-gray-700">
						{t('clientDomains.docs.requirementsTitle')}
					</p>
					<ul class="text-sm text-gray-700" style="margin:0;padding-left:1.1rem;list-style:disc">
						{#each requirements as key (key)}
							<li data-testid={`document-requirement-${key}`}>
								{t(`clientDomains.docs.req.${key}`)}
							</li>
						{/each}
					</ul>
				</div>
			{/if}

			{#if docsError}
				<div class="ca-alert-danger" data-testid="document-error">{docsError}</div>
			{/if}

			{#if documents && uploadUrl}
				<div class="ca-alert-success" data-testid="document-link-ready">
					<p class="font-semibold" style="margin-bottom:4px">
						{t('clientDomains.docs.linkReadyTitle')}
					</p>
					<p style="margin-bottom:8px">{t('clientDomains.docs.linkHint')}</p>
					<a
						href={uploadUrl}
						target="_blank"
						rel="noopener noreferrer"
						class="ca-btn ca-btn-success"
						data-testid="document-upload-link"
					>
						<i class="fas fa-external-link-alt" aria-hidden="true"></i>
						{t('clientDomains.docs.openLink')}
					</a>
				</div>
			{/if}

			<div class="flex flex-wrap items-center gap-3">
				{#if documents}
					<form method="POST" action="?/documentLink" use:enhance={linkEnhance}>
						<button
							type="submit"
							class="ca-btn ca-btn-primary"
							disabled={linkPending}
							aria-busy={linkPending}
							data-testid="document-upload-button"
						>
							{#if linkPending}<span class="ca-spinner" aria-hidden="true"></span>{/if}
							{bounced ? t('clientDomains.docs.reupload') : t('clientDomains.docs.upload')}
						</button>
					</form>
				{:else}
					<form method="POST" action="?/resendVerification" use:enhance={resendEnhance}>
						<button
							type="submit"
							class="ca-btn ca-btn-primary"
							disabled={resendPending}
							aria-busy={resendPending}
							data-testid="verification-resend-button"
						>
							{#if resendPending}<span class="ca-spinner" aria-hidden="true"></span>{/if}
							{t('clientDomains.docs.resend')}
						</button>
					</form>
				{/if}
				<form method="POST" action="?/refresh" use:enhance={refreshEnhance}>
					<button
						type="submit"
						class="ca-btn ca-btn-default"
						disabled={refreshPending}
						aria-busy={refreshPending}
						data-testid="document-refresh-button"
					>
						{#if refreshPending}<span class="ca-spinner" aria-hidden="true"></span>{/if}
						{t('clientDomains.docs.refresh')}
					</button>
				</form>
			</div>
		</div>
	</div>
{/if}
