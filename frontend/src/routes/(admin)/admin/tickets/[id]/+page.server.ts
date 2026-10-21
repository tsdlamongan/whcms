import { clientNamesFor } from '$lib/server/adminNames';
import { apiFetch } from '$lib/server/api';
import { fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';
import {
	parseAttachments,
	TICKET_PRIORITIES,
	TICKET_STATUSES,
	type NormalizedReply,
	type StaffUser,
	type Ticket,
	type TicketDepartment,
	type TicketReply
} from '../types';

type TicketDetailPayload =
	(Ticket & { replies?: TicketReply[] }) | { ticket?: Ticket; replies?: TicketReply[] };

export const load: PageServerLoad = async (event) => {
	const [ticketRes, deptRes, staffRes] = await Promise.all([
		apiFetch<TicketDetailPayload>(event, `/api/v1/admin/tickets/${event.params.id}`),
		apiFetch<TicketDepartment[]>(event, '/api/v1/ticket-departments'),
		apiFetch<StaffUser[]>(event, '/api/v1/admin/staff')
	]);

	// GET /admin/tickets/:id may return the ticket flat (with embedded replies) or a
	// {ticket, replies} composite - accept both, since the DTO is not pinned in CONTRACTS.
	let ticket: Ticket | null = null;
	let rawReplies: TicketReply[] = [];
	const payload = ticketRes.data;
	if (payload) {
		if ('ticket' in payload && payload.ticket) {
			ticket = payload.ticket;
			rawReplies = payload.replies ?? [];
		} else {
			const flat = payload as Ticket & { replies?: TicketReply[] };
			ticket = flat;
			rawReplies = flat.replies ?? [];
		}
	}

	// The ticket row carries only client_id; resolve the display name.
	if (ticket && ticket.client_id && !ticket.client_name) {
		const clientName = (await clientNamesFor(event, [ticket.client_id])).get(ticket.client_id);
		if (clientName) ticket = { ...ticket, client_name: clientName };
	}

	// Admin view keeps internal notes (highlighted in the UI).
	const replies: NormalizedReply[] = rawReplies
		.map((r) => ({
			id: r.id,
			ticket_id: r.ticket_id,
			user_id: r.user_id,
			author_name: r.author_name,
			message: r.message,
			is_internal: r.is_internal === true,
			created_at: r.created_at,
			attachments: parseAttachments(r.attachments)
		}))
		.sort((a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime());

	return {
		ticket,
		replies,
		departments: deptRes.data ?? [],
		staff: (staffRes.data ?? []).filter((u) => u.role !== 'client' && u.status === 'active'),
		notFound: ticketRes.status === 404,
		loadError: ticketRes.error?.message ?? null
	};
};

export const actions: Actions = {
	reply: async (event) => {
		const form = await event.request.formData();
		const message = String(form.get('message') ?? '').trim();
		const isInternal = form.get('is_internal') === 'on';

		if (!message) {
			return fail(400, {
				replyErrorKey: 'adminsupport.tickets.messageRequired',
				replyMessage: message,
				replyInternal: isInternal
			});
		}

		const body = new FormData();
		body.set('message', message);
		body.set('is_internal', isInternal ? 'true' : 'false');
		for (const entry of form.getAll('attachments')) {
			if (entry instanceof File && entry.name && entry.size > 0) {
				body.append('attachments', entry, entry.name);
			}
		}

		const res = await apiFetch(event, `/api/v1/admin/tickets/${event.params.id}/replies`, {
			method: 'POST',
			body
		});

		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				replyErrorMessage: res.error.message,
				replyMessage: message,
				replyInternal: isInternal
			});
		}

		return { replied: true, internal: isInternal };
	},

	assign: async (event) => {
		const form = await event.request.formData();
		const raw = String(form.get('assigned_user_id') ?? '').trim();
		const assignedUserID = raw ? Number(raw) : null;
		if (raw && !Number.isInteger(assignedUserID)) {
			return fail(400, { updateErrorKey: 'adminsupport.tickets.updateFailed' });
		}

		const res = await apiFetch(event, `/api/v1/admin/tickets/${event.params.id}/assign`, {
			method: 'POST',
			body: { assigned_user_id: assignedUserID }
		});

		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, { updateErrorMessage: res.error.message });
		}
		return { updated: true };
	},

	status: async (event) => {
		const form = await event.request.formData();
		const status = String(form.get('status') ?? '');
		if (!(TICKET_STATUSES as readonly string[]).includes(status)) {
			return fail(400, { updateErrorKey: 'adminsupport.tickets.updateFailed' });
		}

		const res = await apiFetch(event, `/api/v1/admin/tickets/${event.params.id}/status`, {
			method: 'POST',
			body: { status }
		});

		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, { updateErrorMessage: res.error.message });
		}
		return { updated: true };
	},

	priority: async (event) => {
		const form = await event.request.formData();
		const priority = String(form.get('priority') ?? '');
		if (!(TICKET_PRIORITIES as readonly string[]).includes(priority)) {
			return fail(400, { updateErrorKey: 'adminsupport.tickets.updateFailed' });
		}

		const res = await apiFetch(event, `/api/v1/admin/tickets/${event.params.id}/priority`, {
			method: 'POST',
			body: { priority }
		});

		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, { updateErrorMessage: res.error.message });
		}
		return { updated: true };
	},

	// Closing is just the "closed" status: the API has no separate /close for staff.
	close: async (event) => {
		const res = await apiFetch(event, `/api/v1/admin/tickets/${event.params.id}/status`, {
			method: 'POST',
			body: { status: 'closed' }
		});

		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, { closeErrorMessage: res.error.message });
		}
		return { closed: true };
	}
};
