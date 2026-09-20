<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import FormField from '$lib/components/FormField.svelte';
	import LoadingButton from '$lib/components/LoadingButton.svelte';
	import Turnstile from '$lib/components/ca/Turnstile.svelte';
	import { i18n, t } from '$lib/i18n';
	import { untrack } from 'svelte';
	import type { PageProps } from './$types';

	let { form, data }: PageProps = $props();

	// Curated country list (ISO 3166-1 alpha-2); names rendered per locale via Intl.
	const COUNTRY_CODES = [
		'ID',
		'MY',
		'SG',
		'TH',
		'VN',
		'PH',
		'BN',
		'TL',
		'IN',
		'CN',
		'HK',
		'TW',
		'JP',
		'KR',
		'AU',
		'NZ',
		'SA',
		'AE',
		'US',
		'CA',
		'GB',
		'DE',
		'FR',
		'NL'
	];

	let email = $state(untrack(() => form?.values?.email ?? ''));
	let password = $state('');
	let confirmPassword = $state('');
	let firstName = $state(untrack(() => form?.values?.first_name ?? ''));
	let lastName = $state(untrack(() => form?.values?.last_name ?? ''));
	let company = $state(untrack(() => form?.values?.company ?? ''));
	let address1 = $state(untrack(() => form?.values?.address1 ?? ''));
	let city = $state(untrack(() => form?.values?.city ?? ''));
	let postcode = $state(untrack(() => form?.values?.postcode ?? ''));
	let country = $state(untrack(() => form?.values?.country ?? 'ID'));
	let phone = $state(untrack(() => form?.values?.phone ?? ''));
	let submitting = $state(false);
	let resending = $state(false);
	let captchaReset = $state(0);

	const countryOptions = $derived.by(() => {
		const names = new Intl.DisplayNames([i18n.locale], { type: 'region' });
		return COUNTRY_CODES.map((code) => ({ value: code, label: names.of(code) ?? code }));
	});

	const fieldError = (name: string): string | undefined => {
		const key = form?.fieldErrors?.[name];
		return key ? t(key) : undefined;
	};

	const topError = $derived(form?.errorKey ? t(form.errorKey) : (form?.errorMessage ?? null));
	// Keep showing the "check your email" panel after a failed resend attempt too.
	const success = $derived(form?.success === true || !!form?.resendEmail);
	const successEmail = $derived(form?.success === true ? form.email : (form?.resendEmail ?? ''));
</script>

<svelte:head>
	<title>{t('auth.register')} — {appName}</title>
</svelte:head>

{#if success}
	<div class="ca-alert-success" data-testid="register-success">
		<strong>{t('feauth.register.successTitle')}</strong>
		<div>{t('feauth.register.successBody', { email: successEmail })}</div>
	</div>

	{#if form?.resent}
		<div class="ca-alert-info" data-testid="register-resend-success">
			{t('feauth.register.resendSuccess')}
		</div>
	{/if}

	{#if form?.resendErrorKey}
		<div class="ca-alert-danger" data-testid="register-resend-error">
			{t(form.resendErrorKey)}
		</div>
	{/if}

	<form
		method="POST"
		action="?/resend"
		class="mt-2"
		use:enhance={() => {
			resending = true;
			return async ({ update }) => {
				resending = false;
				await update();
			};
		}}
	>
		<input type="hidden" name="email" value={successEmail} />
		<div data-testid="register-resend-button">
			<LoadingButton
				type="submit"
				variant="secondary"
				loading={resending}
				class="ca-btn ca-btn-default ca-btn-block"
			>
				{t('feauth.register.resend')}
			</LoadingButton>
		</div>
	</form>

	<p class="auth-alt">
		<a href="/login" class="auth-link" data-testid="register-login-link">
			{t('auth.login')}
		</a>
	</p>
{:else}
	<h1 class="ca-h1">{t('feauth.register.title')}</h1>
	<p class="ca-muted auth-subtitle">{t('feauth.register.subtitle')}</p>

	{#if form?.resendErrorKey}
		<div class="ca-alert-danger" data-testid="register-resend-error">
			{t(form.resendErrorKey)}
		</div>
	{/if}

	{#if topError}
		<div class="ca-alert-danger" data-testid="register-error">
			{topError}
		</div>
	{/if}

	<form
		method="POST"
		action="?/register"
		data-testid="register-form"
		use:enhance={() => {
			submitting = true;
			return async ({ result, update }) => {
				submitting = false;
				if (result.type === 'failure') captchaReset++;
				await update();
			};
		}}
	>
		<div data-testid="register-email">
			<FormField
				label={t('auth.email')}
				name="email"
				type="email"
				bind:value={email}
				error={fieldError('email')}
				placeholder="nama@contoh.co.id"
				autocomplete="email"
				required
			/>
		</div>

		<div class="ca-grid-2">
			<div data-testid="register-password">
				<FormField
					label={t('auth.password')}
					name="password"
					type="password"
					bind:value={password}
					error={fieldError('password')}
					hint={t('feauth.register.passwordHint')}
					autocomplete="new-password"
					required
				/>
			</div>
			<div data-testid="register-confirm-password">
				<FormField
					label={t('auth.confirmPassword')}
					name="confirm_password"
					type="password"
					bind:value={confirmPassword}
					error={fieldError('confirm_password')}
					autocomplete="new-password"
					required
				/>
			</div>
		</div>

		<div class="ca-grid-2">
			<div data-testid="register-first-name">
				<FormField
					label={t('feauth.register.firstName')}
					name="first_name"
					bind:value={firstName}
					error={fieldError('first_name')}
					autocomplete="given-name"
					required
				/>
			</div>
			<div data-testid="register-last-name">
				<FormField
					label={t('feauth.register.lastName')}
					name="last_name"
					bind:value={lastName}
					error={fieldError('last_name')}
					autocomplete="family-name"
					required
				/>
			</div>
		</div>

		<div data-testid="register-company">
			<FormField
				label={`${t('feauth.register.company')} (${t('common.optional')})`}
				name="company"
				bind:value={company}
				error={fieldError('company')}
				autocomplete="organization"
			/>
		</div>

		<div data-testid="register-address">
			<FormField
				label={t('feauth.register.address')}
				name="address1"
				bind:value={address1}
				error={fieldError('address1')}
				autocomplete="street-address"
				required
			/>
		</div>

		<div class="ca-grid-2">
			<div data-testid="register-city">
				<FormField
					label={t('feauth.register.city')}
					name="city"
					bind:value={city}
					error={fieldError('city')}
					autocomplete="address-level2"
					required
				/>
			</div>
			<div data-testid="register-postcode">
				<FormField
					label={t('feauth.register.postcode')}
					name="postcode"
					bind:value={postcode}
					error={fieldError('postcode')}
					autocomplete="postal-code"
					required
				/>
			</div>
		</div>

		<div class="ca-grid-2">
			<div data-testid="register-country">
				<FormField
					label={t('feauth.register.country')}
					name="country"
					type="select"
					bind:value={country}
					error={fieldError('country')}
					options={countryOptions}
					required
				/>
			</div>
			<div data-testid="register-phone">
				<FormField
					label={t('feauth.register.phone')}
					name="phone"
					bind:value={phone}
					error={fieldError('phone')}
					placeholder="+62 812 3456 7890"
					autocomplete="tel"
					required
				/>
			</div>
		</div>

		{#if data.captcha?.enabled && data.captcha.siteKey}
			<Turnstile siteKey={data.captcha.siteKey} resetSignal={captchaReset} />
		{/if}

		<div data-testid="register-submit">
			<LoadingButton type="submit" loading={submitting} class="ca-btn ca-btn-primary ca-btn-block">
				{t('auth.register')}
			</LoadingButton>
		</div>
	</form>

	<p class="auth-alt">
		{t('auth.haveAccount')}
		<a href="/login" class="auth-link" data-testid="register-login-link">
			{t('auth.login')}
		</a>
	</p>
{/if}

<style>
	.auth-subtitle {
		margin-top: -8px;
		margin-bottom: 20px;
	}
	.ca-grid-2 {
		display: grid;
		grid-template-columns: 1fr;
		gap: 0 14px;
	}
	@media (min-width: 576px) {
		.ca-grid-2 {
			grid-template-columns: 1fr 1fr;
		}
	}
	.auth-alt {
		margin: 22px 0 0;
		text-align: center;
		font-size: 0.9rem;
		color: #6c757d;
	}
	.auth-link {
		font-weight: 600;
		color: var(--ca-primary);
	}
	.auth-link:hover {
		text-decoration: underline;
	}
</style>
