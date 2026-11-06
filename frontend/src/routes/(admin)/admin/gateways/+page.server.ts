import { apiFetch } from '$lib/server/api';
import { fail } from '@sveltejs/kit';
import type { Actions, PageServerLoad } from './$types';

/** One manual-gateway bank account row - adminops.BankAccountInput/ports.BankAccount JSON tags. */
interface BankAccount {
	bank_name: string;
	account_number: string;
	account_holder: string;
}

/** GET /admin/gateways response - adminops.GatewaysConfig. */
interface GatewaysConfig {
	duitku: {
		merchant_code: string;
		mode: string;
		base_url: string;
		api_key_set: boolean;
	};
	manual: {
		enabled: boolean;
		accounts: BankAccount[];
		instructions: string;
	};
}

export interface DuitkuConfig {
	merchantCode: string;
	mode: string;
	baseUrl: string;
	apiKeySet: boolean;
}

export interface ManualConfig {
	enabled: boolean;
	accounts: BankAccount[];
	instructions: string;
}

function normalizeDuitku(cfg: GatewaysConfig | null | undefined): DuitkuConfig {
	return {
		merchantCode: cfg?.duitku?.merchant_code ?? '',
		mode: cfg?.duitku?.mode ?? 'sandbox',
		baseUrl: cfg?.duitku?.base_url ?? '',
		apiKeySet: cfg?.duitku?.api_key_set ?? false
	};
}

function normalizeManual(cfg: GatewaysConfig | null | undefined): ManualConfig {
	return {
		enabled: cfg?.manual?.enabled ?? false,
		accounts: cfg?.manual?.accounts ?? [],
		instructions: cfg?.manual?.instructions ?? ''
	};
}

export const load: PageServerLoad = async (event) => {
	const res = await apiFetch<GatewaysConfig>(event, '/api/v1/admin/gateways');

	return {
		duitku: normalizeDuitku(res.data),
		manual: normalizeManual(res.data),
		errorMessage: res.error?.message ?? null
	};
};

export const actions: Actions = {
	duitku: async (event) => {
		const form = await event.request.formData();
		const merchantCode = String(form.get('merchant_code') ?? '').trim();
		const mode = String(form.get('mode') ?? 'sandbox');
		const baseUrl = String(form.get('base_url') ?? '').trim();

		if (!merchantCode) {
			return fail(400, {
				action: 'duitku',
				errorKey: 'adminBilling.gateways.errMerchantCode',
				merchantCode,
				mode,
				baseUrl
			});
		}
		if (mode !== 'sandbox' && mode !== 'production') {
			return fail(400, {
				action: 'duitku',
				errorKey: 'adminBilling.gateways.errMode',
				merchantCode,
				mode,
				baseUrl
			});
		}

		// api_key is tri-state: omit entirely to leave the stored key
		// untouched, send "" only when "Clear stored key" is checked,
		// otherwise send whatever new value was typed.
		const body: Record<string, unknown> = { merchant_code: merchantCode, mode, base_url: baseUrl };
		const clearApiKey = form.get('clear_api_key') === 'on';
		const apiKeyInput = String(form.get('api_key') ?? '');
		if (clearApiKey) {
			body.api_key = '';
		} else if (apiKeyInput !== '') {
			body.api_key = apiKeyInput;
		}

		const res = await apiFetch(event, '/api/v1/admin/gateways', {
			method: 'PUT',
			body
		});

		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				action: 'duitku',
				errorMessage: res.error.message,
				merchantCode,
				mode,
				baseUrl
			});
		}

		return { success: true, action: 'duitku' };
	},

	manual: async (event) => {
		const form = await event.request.formData();
		const enabled = form.get('enabled') === 'on';
		const instructions = String(form.get('instructions') ?? '').trim();

		// Bank account rows arrive as repeated indexed fields
		// (bank_name_0/account_number_0/account_holder_0, ...) - see
		// +page.svelte's dynamic row add/remove. Blank rows are dropped.
		const accounts: BankAccount[] = [];
		for (let i = 0; form.has(`bank_name_${i}`); i++) {
			const bankName = String(form.get(`bank_name_${i}`) ?? '').trim();
			const accountNumber = String(form.get(`account_number_${i}`) ?? '').trim();
			const accountHolder = String(form.get(`account_holder_${i}`) ?? '').trim();
			if (bankName || accountNumber || accountHolder) {
				accounts.push({
					bank_name: bankName,
					account_number: accountNumber,
					account_holder: accountHolder
				});
			}
		}

		const res = await apiFetch(event, '/api/v1/admin/gateways/manual', {
			method: 'PUT',
			body: { enabled, accounts, instructions }
		});

		if (res.error) {
			return fail(res.status >= 400 ? res.status : 500, {
				action: 'manual',
				errorMessage: res.error.message,
				enabled,
				instructions
			});
		}

		return { success: true, action: 'manual' };
	}
};
