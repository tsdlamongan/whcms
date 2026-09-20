import { apiFetch } from '$lib/server/api';
import { captchaToken, loadCaptchaConfig } from '$lib/server/captcha';
import { fail, redirect } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
const PASSWORD_MIN = 8;

/** Text fields echoed back to the form on validation failure (never passwords). */
interface RegisterValues {
	email: string;
	first_name: string;
	last_name: string;
	company: string;
	address1: string;
	city: string;
	state: string;
	postcode: string;
	country: string;
	phone: string;
}

export const load: PageServerLoad = async (event) => {
	if (event.locals.user) {
		redirect(303, event.locals.user.role === 'client' ? '/dashboard' : '/admin');
	}
	return { captcha: await loadCaptchaConfig(event) };
};

export const actions: Actions = {
	register: async (event) => {
		const form = await event.request.formData();
		const str = (name: string) => String(form.get(name) ?? '').trim();

		const values: RegisterValues = {
			email: str('email'),
			first_name: str('first_name'),
			last_name: str('last_name'),
			company: str('company'),
			address1: str('address1'),
			city: str('city'),
			state: str('state'),
			postcode: str('postcode'),
			country: str('country') || 'ID',
			phone: str('phone')
		};
		const password = String(form.get('password') ?? '');
		const confirmPassword = String(form.get('confirm_password') ?? '');

		// Field-level validation - values are i18n keys resolved in the page.
		const fieldErrors: Record<string, string> = {};
		const required: (keyof RegisterValues)[] = [
			'email',
			'first_name',
			'last_name',
			'address1',
			'city',
			'state',
			'postcode',
			'country',
			'phone'
		];
		for (const key of required) {
			if (!values[key]) fieldErrors[key] = 'feauth.validation.required';
		}
		if (values.email && !EMAIL_RE.test(values.email)) {
			fieldErrors.email = 'feauth.validation.invalidEmail';
		}
		if (!password) {
			fieldErrors.password = 'feauth.validation.required';
		} else if (password.length < PASSWORD_MIN) {
			fieldErrors.password = 'feauth.validation.passwordMin';
		}
		if (password && confirmPassword !== password) {
			fieldErrors.confirm_password = 'feauth.validation.passwordMismatch';
		}
		if (Object.keys(fieldErrors).length > 0) {
			return fail(400, { fieldErrors, values });
		}

		const captcha = captchaToken(form);
		const res = await apiFetch(event, '/api/v1/auth/register', {
			method: 'POST',
			body: { ...values, password, ...(captcha ? { captcha_token: captcha } : {}) },
			token: null
		});

		if (res.error) {
			if (res.status === 409 || res.error.code === 'CONFLICT') {
				return fail(409, {
					fieldErrors: { email: 'feauth.register.emailTaken' } as Record<string, string>,
					values
				});
			}
			return fail(res.status >= 400 ? res.status : 500, {
				errorMessage: res.error.message || undefined,
				errorKey: res.error.message ? undefined : 'feauth.register.failed',
				values
			});
		}

		// No auto-login: account requires email verification first.
		return { success: true, email: values.email };
	},

	resend: async (event) => {
		const form = await event.request.formData();
		const email = String(form.get('email') ?? '').trim();
		if (!email) {
			return fail(400, { resendErrorKey: 'feauth.validation.required', resendEmail: email });
		}

		const res = await apiFetch(event, '/api/v1/auth/resend-verification', {
			method: 'POST',
			body: { email },
			token: null
		});

		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				resendErrorKey: 'feauth.register.resendFailed',
				resendEmail: email
			});
		}
		return { success: true, email, resent: true };
	}
};
