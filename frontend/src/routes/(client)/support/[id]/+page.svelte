<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import Alert from '$lib/components/Alert.svelte';
	import Breadcrumb from '$lib/components/Breadcrumb.svelte';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import DateText from '$lib/components/DateText.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import FormField from '$lib/components/FormField.svelte';
	import LoadingButton from '$lib/components/LoadingButton.svelte';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import { t } from '$lib/i18n';
	import { toast } from '$lib/stores/toast.svelte';
	import { untrack } from 'svelte';
	import type { PageProps } from './$types';
	import { TICKET_PRIORITIES, type TicketAttachment } from '../types';

	let { data, form }: PageProps = $props();

	let replyMessage = $state(untrack(() => form?.replyMessage ?? ''));
	let replying = $state(false);
	let replyFilesInput = $state<HTMLInputElement | null>(null);

	let closeDialogOpen = $state(false);
	let closing = $state(false);
	let closeForm = $state<HTMLFormElement | null>(null);

	const deptNames = $derived(new Map(data.departments.map((d) => [d.id, d.name])));
	const isClosed = $derived(data.ticket?.status === 'closed');

	const priorityVariant: Record<string, 'gray' | 'blue' | 'red'> = {
		low: 'gray',
		medium: 'blue',
		high: 'red'
	};

	function priorityLabel(priority: string): string {
		return (TICKET_PRIORITIES as readonly string[]).includes(priority)
			? t(`supportfe.priority.${priority}`)
			: priority;
	}

	function attachmentHref(att: TicketAttachment): string {
		const params = new URLSearchParams({ filename: att.filename });
		return `/support/${data.ticket?.id}/attachments/${att.index}?${params.toString()}`;
	}

	function formatSize(bytes: number): string {
		if (!bytes || bytes <= 0) return '';
		if (bytes < 1024) return `${bytes} B`;
		if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
		return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
	}

	const replyErrorText = $derived(
		form?.replyErrorMessage ?? (form?.replyErrorKey ? t(form.replyErrorKey) : undefined)
	);

	const pageTitle = $derived(
		data.ticket ? `#${data.ticket.ticket_number} — ${data.ticket.subject}` : t('nav.support')
	);
</script>

<svelte:head>
	<title>{pageTitle} — {appName}</title>
</svelte:head>

<Breadcrumb
	items={[
		{ label: t('nav.home'), href: '/dashboard' },
		{ label: t('nav.support'), href: '/support' },
		{ label: data.ticket ? `#${data.ticket.ticket_number}` : '…' }
	]}
/>

{#if !data.ticket}
	<div class="mb-4">
		<Alert
			type="error"
			title={data.notFound ? t('supportfe.detail.notFound') : t('supportfe.detail.loadFailed')}
		>
			{data.loadError ?? t('supportfe.detail.notFound')}
		</Alert>
	</div>
	<a href="/support" class="text-sm text-primary hover:underline" data-testid="back-to-tickets">
		&larr; {t('supportfe.detail.backToList')}
	</a>
{:else}
	<div data-testid="ticket-detail">
		<div class="mb-5 flex flex-wrap items-start justify-between gap-3">
			<div>
				<h1 class="ca-h1 flex flex-wrap items-center gap-3" style="margin-bottom:0">
					<span class="font-mono text-gray-400">#{data.ticket.ticket_number}</span>
					<span data-testid="ticket-detail-subject">{data.ticket.subject}</span>
					<span data-testid="ticket-status">
						<StatusBadge status={data.ticket.status} />
					</span>
				</h1>
			</div>
			{#if !isClosed}
				<span data-testid="ticket-close">
					<LoadingButton variant="danger" size="sm" onclick={() => (closeDialogOpen = true)}>
						{t('supportfe.detail.close')}
					</LoadingButton>
				</span>
			{/if}
		</div>

		{#if data.loadError}
			<div class="mb-4">
				<Alert type="error">{data.loadError}</Alert>
			</div>
		{/if}

		<!-- Overview card -->
		<div class="ca-card ca-card-body mb-6 grid grid-cols-2 gap-4 sm:grid-cols-4">
			<div>
				<p class="ca-label" style="margin-bottom:2px">{t('supportfe.detail.department')}</p>
				<p class="mt-1 text-sm text-gray-800" data-testid="ticket-department-name">
					{deptNames.get(data.ticket.department_id) ?? '—'}
				</p>
			</div>
			<div>
				<p class="ca-label" style="margin-bottom:2px">{t('supportfe.detail.priority')}</p>
				<p class="mt-1">
					<StatusBadge
						status={data.ticket.priority}
						variant={priorityVariant[data.ticket.priority] ?? 'gray'}
						label={priorityLabel(data.ticket.priority)}
					/>
				</p>
			</div>
			<div>
				<p class="ca-label" style="margin-bottom:2px">{t('supportfe.detail.opened')}</p>
				<p class="mt-1 text-sm text-gray-800">
					<DateText value={data.ticket.created_at} mode="datetime" />
				</p>
			</div>
			<div>
				<p class="ca-label" style="margin-bottom:2px">{t('supportfe.detail.lastReply')}</p>
				<p class="mt-1 text-sm text-gray-800">
					<DateText value={data.ticket.last_reply_at ?? data.ticket.created_at} mode="datetime" />
				</p>
			</div>
		</div>

		<!-- Thread -->
		<h2 class="mb-3 text-sm font-semibold tracking-wide text-gray-600 uppercase">
			{t('supportfe.detail.thread')}
		</h2>
		<div class="space-y-4" data-testid="ticket-thread">
			{#if data.replies.length === 0}
				<div class="ca-card">
					<EmptyState title={t('supportfe.detail.emptyThread')} />
				</div>
			{/if}
			{#each data.replies as reply (reply.id)}
				{@const own = reply.user_id !== null && reply.user_id === data.user.id}
				<div
					class="ca-card"
					style="overflow:hidden;margin-bottom:0"
					data-testid={`ticket-reply-${reply.id}`}
				>
					<div
						class={`flex flex-wrap items-center justify-between gap-2 px-4 py-2.5 ${
							own ? 'bg-primary/5' : 'bg-gray-50'
						}`}
						style="border-bottom:1px solid rgba(0,0,0,.08)"
					>
						<div class="flex items-center gap-2">
							<span class="text-sm font-semibold text-gray-800">{reply.author_name}</span>
							<span
								class={`rounded-full px-2 py-0.5 text-[11px] font-semibold ${
									own ? 'bg-primary/10 text-primary' : 'bg-gray-200 text-gray-600'
								}`}
							>
								{own ? t('supportfe.detail.you') : t('supportfe.detail.staff')}
							</span>
						</div>
						<DateText value={reply.created_at} mode="datetime" class="text-xs text-gray-500" />
					</div>
					<div class="bg-white px-4 py-3 text-sm whitespace-pre-wrap text-gray-700">
						{reply.message}
					</div>
					{#if reply.attachments.length > 0}
						<div class="border-t border-gray-100 bg-white px-4 py-2.5">
							<p class="mb-1.5 text-xs font-semibold text-gray-500 uppercase">
								{t('supportfe.detail.attachmentsLabel')}
							</p>
							<ul class="flex flex-wrap gap-2">
								{#each reply.attachments as att, i (att.index)}
									<li>
										<a
											href={attachmentHref(att)}
											target="_blank"
											rel="noopener"
											class="inline-flex items-center gap-1.5 rounded-md border border-gray-200 bg-gray-50 px-2.5 py-1 text-xs font-medium text-primary hover:bg-gray-100 hover:underline"
											data-testid={`ticket-attachment-${reply.id}-${i}`}
										>
											<svg
												class="h-3.5 w-3.5 text-gray-400"
												viewBox="0 0 24 24"
												fill="none"
												stroke="currentColor"
												stroke-width="2"
												aria-hidden="true"
											>
												<path
													stroke-linecap="round"
													stroke-linejoin="round"
													d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48"
												/>
											</svg>
											{att.filename}
											{#if formatSize(att.size)}
												<span class="text-gray-400">({formatSize(att.size)})</span>
											{/if}
										</a>
									</li>
								{/each}
							</ul>
						</div>
					{/if}
				</div>
			{/each}
		</div>

		<!-- Reply box / closed notice -->
		<div class="mt-6">
			{#if isClosed}
				<Alert type="info">{t('supportfe.detail.closedNotice')}</Alert>
			{:else}
				<div class="ca-card ca-card-body">
					<h2 class="mb-3 text-sm font-semibold tracking-wide text-gray-600 uppercase">
						{t('supportfe.detail.replyTitle')}
					</h2>
					<form
						method="POST"
						action="?/reply"
						enctype="multipart/form-data"
						data-testid="ticket-reply-form"
						use:enhance={() => {
							replying = true;
							return async ({ result, update }) => {
								replying = false;
								if (result.type === 'success') {
									replyMessage = '';
									if (replyFilesInput) replyFilesInput.value = '';
									toast.success(t('supportfe.detail.replySent'));
								} else if (result.type === 'failure') {
									toast.error(
										String(
											(result.data as { replyErrorMessage?: string } | undefined)
												?.replyErrorMessage ?? t('supportfe.detail.replyFailed')
										)
									);
								}
								await update({ reset: false });
							};
						}}
					>
						<div data-testid="ticket-reply-message">
							<FormField
								label={t('supportfe.form.message')}
								name="message"
								type="textarea"
								rows={5}
								bind:value={replyMessage}
								placeholder={t('supportfe.detail.replyPlaceholder')}
								error={replyErrorText}
								required
							/>
						</div>

						<div class="mb-4">
							<label class="ca-label" for="field-reply-attachments">
								{t('supportfe.form.attachments')}
							</label>
							<input
								id="field-reply-attachments"
								name="attachments"
								type="file"
								multiple
								accept=".jpg,.jpeg,.png,.gif,.pdf,.zip,.txt,.log"
								bind:this={replyFilesInput}
								data-testid="ticket-reply-attachments"
								class="block w-full cursor-pointer rounded-md border border-gray-300 bg-white text-sm text-gray-600 shadow-sm file:mr-3 file:cursor-pointer file:rounded-l-md file:border-0 file:bg-gray-100 file:px-3 file:py-2 file:text-sm file:font-medium file:text-gray-700 hover:file:bg-gray-200"
							/>
							<p class="mt-1 text-xs text-gray-500">
								{t('supportfe.form.attachmentsHint', { size: 8 })}
							</p>
						</div>

						<span data-testid="ticket-reply-submit">
							<LoadingButton type="submit" loading={replying}>
								{t('supportfe.detail.replySubmit')}
							</LoadingButton>
						</span>
					</form>
				</div>
			{/if}
		</div>

		<div class="mt-6">
			<a href="/support" class="text-sm text-primary hover:underline" data-testid="back-to-tickets">
				&larr; {t('supportfe.detail.backToList')}
			</a>
		</div>

		<!-- Hidden close form + confirm dialog -->
		<form
			method="POST"
			action="?/close"
			class="hidden"
			bind:this={closeForm}
			use:enhance={() => {
				closing = true;
				return async ({ result, update }) => {
					closing = false;
					closeDialogOpen = false;
					if (result.type === 'success') {
						toast.success(t('supportfe.detail.closed'));
					} else if (result.type === 'failure') {
						toast.error(
							String(
								(result.data as { closeErrorMessage?: string } | undefined)?.closeErrorMessage ??
									t('supportfe.detail.closeFailed')
							)
						);
					}
					await update({ reset: false });
				};
			}}
		></form>

		<ConfirmDialog
			bind:open={closeDialogOpen}
			danger
			loading={closing}
			title={t('supportfe.detail.closeConfirmTitle')}
			message={t('supportfe.detail.closeConfirmMessage')}
			confirmLabel={t('supportfe.detail.close')}
			onConfirm={() => closeForm?.requestSubmit()}
		/>
	</div>
{/if}
