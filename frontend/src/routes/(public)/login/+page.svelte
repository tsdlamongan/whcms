<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import FormField from '$lib/components/FormField.svelte';
	import LoadingButton from '$lib/components/LoadingButton.svelte';
	import Turnstile from '$lib/components/ca/Turnstile.svelte';
	import { t } from '$lib/i18n';
	import { untrack } from 'svelte';
	import type { PageProps } from './$types';

	let { form, data }: PageProps = $props();

	let email = $state(untrack(() => form?.email ?? ''));
	let password = $state('');
	let totpCode = $state('');
	let showTotp = $state(false);
	let showPassword = $state(false);
	let submitting = $state(false);
	let captchaReset = $state(0);

	const errorText = $derived(form?.errorKey ? t(form.errorKey) : (form?.errorMessage ?? null));
</script>

<svelte:head>
	<title>{t('auth.login')} — {appName}</title>
</svelte:head>

<h1 class="ca-h1">{t('auth.loginTitle')}</h1>
<p class="ca-muted login-subtitle">{t('portal.auth.loginSubtitle')}</p>

{#if errorText}
	<div class="ca-alert-danger" role="alert">{errorText}</div>
{/if}

<form
	method="POST"
	use:enhance={() => {
		submitting = true;
		return async ({ result, update }) => {
			submitting = false;
			if (result.type === 'failure') captchaReset++;
			await update();
		};
	}}
>
	<div class="login-field login-field--icon">
		<i class="fa-solid fa-user field-ic" aria-hidden="true"></i>
		<FormField
			label={t('auth.email')}
			name="email"
			type="email"
			bind:value={email}
			placeholder="nama@contoh.co.id"
			autocomplete="email"
			required
		/>
	</div>

	<div class="login-forgot-row">
		<a href="/forgot-password" class="login-forgot">{t('auth.forgotPassword')}</a>
	</div>

	<div class="login-field login-field--icon login-field--pw">
		<i class="fa-solid fa-key field-ic" aria-hidden="true"></i>
		<FormField
			label={t('auth.password')}
			name="password"
			type={showPassword ? 'text' : 'password'}
			bind:value={password}
			autocomplete="current-password"
			required
		/>
		<button
			type="button"
			class="pw-toggle"
			onclick={() => (showPassword = !showPassword)}
			aria-label={showPassword ? t('portal.auth.hidePassword') : t('portal.auth.showPassword')}
		>
			<i class="fa-solid {showPassword ? 'fa-eye-slash' : 'fa-eye'}" aria-hidden="true"></i>
		</button>
	</div>

	{#if showTotp}
		<FormField
			label={t('auth.totpCode')}
			name="totp_code"
			type="text"
			bind:value={totpCode}
			placeholder="123456"
			hint={t('auth.totpHint')}
			autocomplete="one-time-code"
		/>
	{/if}

	<div class="login-totp-row">
		<button type="button" class="login-totp-toggle" onclick={() => (showTotp = !showTotp)}>
			<i class="fa-solid fa-shield-halved" aria-hidden="true"></i>
			{t('auth.totpCode')}
		</button>
	</div>

	{#if data.captcha?.enabled && data.captcha.siteKey}
		<Turnstile siteKey={data.captcha.siteKey} resetSignal={captchaReset} />
	{/if}

	<div class="login-actions">
		<LoadingButton type="submit" loading={submitting} class="ca-btn ca-btn-primary">
			{t('auth.login')}
		</LoadingButton>
		<label class="ca-check login-remember">
			<input type="checkbox" name="remember" />
			{t('portal.auth.rememberMe')}
		</label>
	</div>
</form>

<p class="login-footer">
	{t('auth.noAccount')}
	<a href="/register" class="login-create">{t('auth.register')}</a>
</p>

<style>
	.login-subtitle {
		margin-top: -8px;
		margin-bottom: 20px;
	}

	/* Bootstrap-style input-group with a left FA icon overlaid on the reused
	   FormField input (whose #field-* id + name are preserved). The icon box is
	   pinned to the input row (label block ≈ 24px, input ≈ 38px tall). */
	.login-field {
		position: relative;
	}
	.login-field .field-ic {
		position: absolute;
		left: 12px;
		top: 24px;
		height: 38px;
		display: flex;
		align-items: center;
		color: #adb5bd;
		font-size: 14px;
		pointer-events: none;
	}
	.login-field--icon :global(input) {
		padding-left: 34px;
	}
	.login-field--pw :global(input) {
		padding-right: 42px;
	}
	.pw-toggle {
		position: absolute;
		right: 1px;
		top: 24px;
		height: 38px;
		width: 40px;
		display: flex;
		align-items: center;
		justify-content: center;
		background: none;
		border: none;
		color: #6c757d;
		cursor: pointer;
	}
	.pw-toggle:hover {
		color: var(--ca-primary);
	}

	.login-forgot-row {
		display: flex;
		justify-content: flex-end;
		margin: -6px 0 4px;
	}
	.login-forgot {
		font-size: 0.85rem;
		color: var(--ca-primary);
	}
	.login-forgot:hover {
		text-decoration: underline;
	}

	.login-totp-row {
		margin-bottom: 16px;
	}
	.login-totp-toggle {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		padding: 0;
		background: none;
		border: none;
		color: #6c757d;
		font-family: inherit;
		font-size: 0.85rem;
		cursor: pointer;
	}
	.login-totp-toggle:hover {
		color: var(--ca-primary);
	}

	.login-actions {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
	}
	.login-remember {
		font-size: 0.9rem;
		color: #555;
	}

	.login-footer {
		margin: 22px 0 0;
		text-align: center;
		font-size: 0.9rem;
		color: #6c757d;
	}
	.login-create {
		font-weight: 600;
		color: var(--ca-primary);
	}
	.login-create:hover {
		text-decoration: underline;
	}
</style>
