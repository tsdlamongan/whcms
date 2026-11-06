<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import { invalidateAll } from '$app/navigation';
	import DateText from '$lib/components/DateText.svelte';
	import HpModal from '$lib/components/hp/HpModal.svelte';
	import { toast } from '$lib/stores/toast.svelte';
	import type { PageProps } from './$types';
	import { TICKET_PRIORITIES, TICKET_STATUSES, type TicketAttachment } from '../types';

	let { data, form }: PageProps = $props();

	let replyMessage = $state('');
	let replyInternal = $state(false);
	let replySubmitting = $state(false);
	let closeOpen = $state(false);
	let closing = $state(false);
	let replyForm = $state<HTMLFormElement | null>(null);

	const ticket = $derived(data.ticket);
	const isClosed = $derived(ticket?.status === 'closed');

	const priorityLabels: Record<string, string> = { low: 'Low', medium: 'Medium', high: 'High' };

	function statusClass(s: string): string {
		if (s === 'open') return 'green';
		if (s === 'closed') return 'gray';
		return 'orange';
	}

	const departmentName = $derived(
		ticket
			? (ticket.department_name ??
					data.departments.find((d) => d.id === ticket.department_id)?.name ??
					`#${ticket.department_id}`)
			: '—'
	);

	function updateErrorMessage(): string | null {
		if (form?.updateErrorKey) return 'Failed to update ticket.';
		return form?.updateErrorMessage ?? null;
	}
	function replyErrorMessage(): string | null {
		if (form?.replyErrorKey) return 'Message is required.';
		return form?.replyErrorMessage ?? null;
	}
	const replyErrorText = $derived(replyErrorMessage());
	const updateErrorText = $derived(updateErrorMessage());

	/** Selects submit their small form on change (progressive enhancement keeps a submit button). */
	function submitOnChange(e: Event) {
		(e.currentTarget as HTMLSelectElement).form?.requestSubmit();
	}

	function attachmentHref(att: TicketAttachment): string {
		const params = new URLSearchParams({ filename: att.filename });
		return `/admin/tickets/${ticket?.id}/attachments/${att.index}?${params.toString()}`;
	}
</script>

<svelte:head>
	<title>{ticket ? `Ticket ${ticket.ticket_number}` : 'Support Tickets'} — {appName} Admin</title>
</svelte:head>

{#if data.notFound || !ticket}
	<h1 class="hp-h1">Support Tickets</h1>
	<div class="hp-alert-red">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>
		{data.loadError ?? 'Failed to load data.'}
	</div>
{:else}
	<div
		style="display:flex;flex-wrap:wrap;align-items:flex-start;justify-content:space-between;gap:12px;margin-bottom:6px"
	>
		<div>
			<h1 class="hp-h1" style="margin-bottom:4px">
				<span data-testid="ticket-number">{ticket.ticket_number}</span>
				<span
					class={`hp-stext ${statusClass(ticket.status)}`}
					style="font-size:14px;margin-left:10px">{ticket.status}</span
				>
			</h1>
			<p style="color:#666;margin:0 0 12px" data-testid="ticket-subject">{ticket.subject}</p>
		</div>
		{#if !isClosed}
			<button
				type="button"
				class="hp-btn hp-btn-danger"
				disabled={closing}
				onclick={() => (closeOpen = true)}
				data-testid="ticket-close-button"
			>
				<i class="fas fa-times-circle"></i>Close Ticket
			</button>
		{/if}
	</div>

	{#if data.loadError}
		<div class="hp-alert-red">
			<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.loadError}
		</div>
	{/if}

	{#if updateErrorText}
		<div class="hp-alert-red" data-testid="ticket-update-error">
			<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{updateErrorText}
		</div>
	{/if}

	{#if form?.closeErrorMessage}
		<div class="hp-alert-red">
			<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{form.closeErrorMessage}
		</div>
	{/if}

	<div class="tkt-grid">
		<!-- Thread + reply -->
		<div style="min-width:0">
			<section class="hp-panel">
				<div class="hp-panel-hd"><span class="title">Conversation</span></div>
				<div data-testid="ticket-thread">
					{#if data.replies.length === 0}
						<div style="text-align:center;padding:40px 20px;color:#999">
							No messages on this ticket yet.
						</div>
					{:else}
						{#each data.replies as reply (reply.id)}
							<div
								class="tkt-reply"
								class:internal={reply.is_internal}
								data-testid={reply.is_internal ? `reply-internal-${reply.id}` : `reply-${reply.id}`}
							>
								<div class="tkt-reply-meta">
									<span class="who">{reply.author_name || '—'}</span>
									{#if reply.is_internal}
										<span class="hp-badge" style="background:#f89406">Internal</span>
									{:else if reply.user_id && reply.user_id !== ticket.client_id}
										<span class="hp-badge" style="background:#337ab7">Staff</span>
									{/if}
									<span class="when"><DateText value={reply.created_at} mode="datetime" /></span>
								</div>
								<p class="tkt-reply-msg">{reply.message}</p>
								{#if reply.attachments.length > 0}
									<div style="margin-top:8px;display:flex;flex-wrap:wrap;gap:8px">
										{#each reply.attachments as att, i (att.index)}
											<a
												href={attachmentHref(att)}
												target="_blank"
												rel="noopener"
												class="tkt-attachment"
												data-testid={`ticket-attachment-${reply.id}-${i}`}
											>
												<i class="fas fa-paperclip"></i>{att.filename}
											</a>
										{/each}
									</div>
								{/if}
							</div>
						{/each}
					{/if}
				</div>
			</section>

			<!-- Reply box -->
			<section class="hp-panel" style="margin-top:16px">
				<div class="hp-panel-hd"><span class="title">Write a Reply</span></div>
				<div style="padding:14px 12px">
					{#if replyErrorText}
						<div class="hp-alert-red" data-testid="ticket-reply-error">
							<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{replyErrorText}
						</div>
					{/if}
					<form
						method="POST"
						action="?/reply"
						enctype="multipart/form-data"
						bind:this={replyForm}
						use:enhance={() => {
							replySubmitting = true;
							return async ({ result, update }) => {
								replySubmitting = false;
								if (result.type === 'success') {
									toast.success(
										replyInternal ? 'Internal note saved successfully.' : 'Reply sent successfully.'
									);
									replyMessage = '';
									replyInternal = false;
									replyForm?.reset();
								}
								await update({ reset: false });
							};
						}}
					>
						<textarea
							name="message"
							rows="5"
							bind:value={replyMessage}
							placeholder="Write your reply…"
							data-testid="ticket-reply-message"
							class="hp-textarea"
							class:tkt-internal-input={replyInternal}
							style="margin-bottom:12px"></textarea>

						<div style="margin-bottom:12px">
							<label
								class="hp-field-label"
								for="reply-attachments"
								style="display:block;margin-bottom:4px">Attachments</label
							>
							<input
								id="reply-attachments"
								type="file"
								name="attachments"
								multiple
								data-testid="ticket-reply-attachments"
							/>
							<div class="hp-help" style="margin-top:4px">Optional. Multiple files allowed.</div>
						</div>

						<div
							style="display:flex;flex-wrap:wrap;align-items:center;justify-content:space-between;gap:12px"
						>
							<label class="hp-checkline" style="padding:0">
								<input
									type="checkbox"
									name="is_internal"
									bind:checked={replyInternal}
									data-testid="ticket-reply-internal-toggle"
								/>
								Save as internal note (not visible to the client)
							</label>
							<button
								type="submit"
								class={replyInternal ? 'hp-btn' : 'hp-btn hp-btn-primary'}
								disabled={replySubmitting}
								aria-busy={replySubmitting}
								data-testid="ticket-reply-submit"
							>
								{#if replySubmitting}
									<i class="fas fa-spinner fa-spin"></i>
								{/if}
								{replyInternal ? 'Internal Note' : 'Reply'}
							</button>
						</div>
					</form>
				</div>
			</section>
		</div>

		<!-- Properties sidebar -->
		<aside>
			<section class="hp-panel">
				<div class="hp-panel-hd"><span class="title">Ticket Properties</span></div>
				<div style="padding:14px 12px">
					<div class="tkt-prop">
						<div class="tkt-prop-lbl">Department</div>
						<div class="tkt-prop-val" data-testid="ticket-department">{departmentName}</div>
					</div>
					<div class="tkt-prop">
						<div class="tkt-prop-lbl">Client</div>
						<div class="tkt-prop-val">
							{#if ticket.client_id}
								<a
									href={`/admin/clients/${ticket.client_id}`}
									style="color:#337ab7"
									data-testid="ticket-client-link"
								>
									{ticket.client_name ?? `#${ticket.client_id}`}
								</a>
							{:else}
								<span style="color:#999;font-style:italic">—</span>
							{/if}
						</div>
					</div>
					<div class="tkt-prop">
						<div class="tkt-prop-lbl">Opened</div>
						<div class="tkt-prop-val"><DateText value={ticket.created_at} mode="datetime" /></div>
					</div>
					<div class="tkt-prop">
						<div class="tkt-prop-lbl">Last Reply</div>
						<div class="tkt-prop-val">
							<DateText value={ticket.last_reply_at} mode="datetime" />
						</div>
					</div>

					<form
						method="POST"
						action="?/assign"
						class="tkt-prop"
						use:enhance={() => {
							return async ({ result, update }) => {
								if (result.type === 'success') {
									toast.success('Ticket updated successfully.');
									await invalidateAll();
								}
								await update({ reset: false });
							};
						}}
					>
						<label class="tkt-prop-lbl" for="ticket-assign-select">Assigned to</label>
						<select
							id="ticket-assign-select"
							name="assigned_user_id"
							class="hp-select"
							disabled={isClosed}
							onchange={submitOnChange}
							data-testid="ticket-assign-select"
						>
							<option value="" selected={!ticket.assigned_user_id}>Unassigned</option>
							{#each data.staff as s (s.id)}
								<option value={String(s.id)} selected={ticket.assigned_user_id === s.id}
									>{s.email}</option
								>
							{/each}
						</select>
						<noscript>
							<button type="submit" class="hp-btn" style="margin-top:4px">Assign</button>
						</noscript>
					</form>

					<form
						method="POST"
						action="?/status"
						class="tkt-prop"
						use:enhance={() => {
							return async ({ result, update }) => {
								if (result.type === 'success') {
									toast.success('Ticket updated successfully.');
									await invalidateAll();
								}
								await update({ reset: false });
							};
						}}
					>
						<label class="tkt-prop-lbl" for="ticket-status-select">Status</label>
						<select
							id="ticket-status-select"
							name="status"
							class="hp-select"
							onchange={submitOnChange}
							data-testid="ticket-status-select"
						>
							{#each TICKET_STATUSES as s (s)}
								<option value={s} selected={ticket.status === s}>{s}</option>
							{/each}
						</select>
						<noscript>
							<button type="submit" class="hp-btn" style="margin-top:4px">Save</button>
						</noscript>
					</form>

					<form
						method="POST"
						action="?/priority"
						class="tkt-prop"
						style="margin-bottom:0"
						use:enhance={() => {
							return async ({ result, update }) => {
								if (result.type === 'success') {
									toast.success('Ticket updated successfully.');
									await invalidateAll();
								}
								await update({ reset: false });
							};
						}}
					>
						<label class="tkt-prop-lbl" for="ticket-priority-select">Priority</label>
						<select
							id="ticket-priority-select"
							name="priority"
							class="hp-select"
							disabled={isClosed}
							onchange={submitOnChange}
							data-testid="ticket-priority-select"
						>
							{#each TICKET_PRIORITIES as p (p)}
								<option value={p} selected={ticket.priority === p}>{priorityLabels[p]}</option>
							{/each}
						</select>
						<noscript>
							<button type="submit" class="hp-btn" style="margin-top:4px">Save</button>
						</noscript>
					</form>
				</div>
			</section>
		</aside>
	</div>

	<!-- Close confirm -->
	<form
		method="POST"
		action="?/close"
		id="ticket-close-form"
		use:enhance={() => {
			closing = true;
			return async ({ result, update }) => {
				closing = false;
				closeOpen = false;
				if (result.type === 'success') {
					toast.success('Ticket closed successfully.');
					await invalidateAll();
				}
				await update({ reset: false });
			};
		}}
	></form>

	<HpModal open={closeOpen} title="Close this ticket?" onClose={() => (closeOpen = false)}>
		<p style="margin:0 0 16px;color:#555">
			Closed tickets can no longer be replied to by the client.
		</p>
		<div style="display:flex;justify-content:flex-end;gap:8px">
			<button type="button" class="hp-btn" onclick={() => (closeOpen = false)} disabled={closing}
				>Cancel</button
			>
			<button
				type="button"
				class="hp-btn hp-btn-danger"
				disabled={closing}
				onclick={() => {
					(document.getElementById('ticket-close-form') as HTMLFormElement | null)?.requestSubmit();
				}}
			>
				{#if closing}<i class="fas fa-spinner fa-spin"></i>{/if}Close Ticket
			</button>
		</div>
	</HpModal>
{/if}

<style>
	.tkt-grid {
		display: grid;
		grid-template-columns: 1fr 300px;
		gap: 16px;
		align-items: start;
	}
	@media (max-width: 860px) {
		.tkt-grid {
			grid-template-columns: 1fr;
		}
	}

	.tkt-reply {
		padding: 14px 16px;
		border-bottom: 1px solid #ebebeb;
	}
	.tkt-reply:last-child {
		border-bottom: none;
	}
	.tkt-reply.internal {
		background: #fcf8e3;
		border-left: 4px solid #f89406;
	}
	.tkt-reply-meta {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 8px;
		margin-bottom: 6px;
		font-size: 12px;
		color: #888;
	}
	.tkt-reply-meta .who {
		font-weight: 700;
		color: #333;
	}
	.tkt-reply-msg {
		margin: 0;
		font-size: 13px;
		color: #444;
		white-space: pre-wrap;
	}
	.tkt-attachment {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		padding: 4px 9px;
		border: 1px solid #ddd;
		border-radius: 3px;
		background: #f9f9f9;
		color: #555;
		font-size: 12px;
	}
	.tkt-attachment:hover {
		border-color: #337ab7;
		color: #337ab7;
	}

	.tkt-internal-input {
		background: #fcf8e3;
		border-color: #faebcc;
	}

	.tkt-prop {
		margin-bottom: 14px;
	}
	.tkt-prop-lbl {
		display: block;
		font-size: 11px;
		font-weight: 700;
		color: #888;
		text-transform: uppercase;
		letter-spacing: 0.3px;
		margin-bottom: 4px;
	}
	.tkt-prop-val {
		color: #444;
		font-size: 13px;
	}
</style>
