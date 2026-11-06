import { apiFetch } from '$lib/server/api';
import { captchaToken, loadCaptchaConfig } from '$lib/server/captcha';
import { fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';

export interface TicketDepartment {
	id: number;
	name: string;
	email: string;
	active: boolean;
	sort: number;
}

interface ContactResult {
	ticket_number: string;
}

export const load: PageServerLoad = async (event) => {
	const [res, captcha] = await Promise.all([
		apiFetch<TicketDepartment[]>(event, '/api/v1/ticket-departments'),
		loadCaptchaConfig(event)
	]);
	return { departments: res.data ?? [], captcha };
};

export const actions: Actions = {
	default: async (event) => {
		const form = await event.request.formData();
		const str = (name: string) => String(form.get(name) ?? '').trim();

		const values = {
			name: str('name'),
			email: str('email'),
			subject: str('subject'),
			message: str('message'),
			department_id: str('department_id')
		};

		if (!values.name || !values.email || !values.subject || !values.message) {
			return fail(400, { errorKey: 'portal.contact.fillAllFields', values });
		}

		const body: Record<string, string | number> = {
			name: values.name,
			email: values.email,
			subject: values.subject,
			message: values.message
		};
		const deptId = Number(values.department_id);
		if (deptId > 0) body.department_id = deptId;
		const captcha = captchaToken(form);
		if (captcha) body.captcha_token = captcha;

		const res = await apiFetch<ContactResult>(event, '/api/v1/contact', {
			method: 'POST',
			body,
			token: null
		});

		if (res.error || !res.data) {
			return fail(res.status >= 400 ? res.status : 500, {
				errorMessage: res.error?.message || undefined,
				errorKey: res.error?.message ? undefined : 'portal.contact.error',
				values
			});
		}

		return { success: true, ticketNumber: res.data.ticket_number };
	}
};
