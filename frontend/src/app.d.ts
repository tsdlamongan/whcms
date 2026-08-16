// See https://svelte.dev/docs/kit/types#app.d.ts
// for information about these interfaces

declare global {
	/** Authenticated user identity, populated by hooks.server.ts from GET /api/v1/auth/me. */
	interface SessionUser {
		id: number;
		email: string;
		role: 'admin' | 'staff' | 'client';
		client_id: number;
		name: string;
		/** False while the account's email is unverified (checkout is gated on it). */
		email_verified: boolean;
	}

	namespace App {
		// interface Error {}
		interface Locals {
			/** null when the request carries no valid session. */
			user: SessionUser | null;
			/** Current access token (possibly rotated during this request). */
			accessToken: string | null;
			/** UI locale resolved from the `locale` cookie (default 'id'). */
			locale: 'id' | 'en';
		}
		// interface PageData {}
		// interface PageState {}
		// interface Platform {}
	}
}

export {};
