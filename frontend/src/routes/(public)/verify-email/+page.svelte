<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import FormField from '$lib/components/FormField.svelte';
	import LoadingButton from '$lib/components/LoadingButton.svelte';
	import { t } from '$lib/i18n';
	import { untrack } from 'svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	let resendEmail = $state(untrack(() => form?.email ?? ''));
	let resending = $state(false);

	const failed = $derived(data.status !== 'ok');
	const errorText = $derived(
		data.status === 'missing_token'
			? t('feauth.verify.missingToken')
			: data.status === 'invalid_token'
				? t('feauth.verify.failedExpired')
				: (data.message ?? t('toast.error'))
	);
</script>

<svelte:head>
	<title>{t('auth.verifyEmail')} — {appName}</title>
</svelte:head>

<h1 class="ca-h1">{t('auth.verifyEmail')}</h1>

{#if data.status === 'ok'}
	<div class="ca-alert-success" data-testid="verify-email-success">
		<strong>{t('feauth.verify.successTitle')}</strong>
		<div>{t('feauth.verify.successBody')}</div>
	</div>

	<a
		href="/login"
		class="ca-btn ca-btn-primary ca-btn-block auth-cta"
		data-testid="verify-email-login-link"
	>
		{t('feauth.verify.goLogin')}
	</a>
{:else if failed}
	<div class="ca-alert-danger" data-testid="verify-email-error">
		<strong>{t('feauth.verify.failedTitle')}</strong>
		<div>{errorText}</div>
	</div>

	{#if form?.resent}
		<div class="ca-alert-success" data-testid="verify-email-resend-success">
			{t('feauth.register.resendSuccess')}
		</div>
	{:else}
		<p class="verify-prompt">{t('feauth.verify.resendPrompt')}</p>

		{#if form?.resendErrorKey}
			<div class="ca-alert-danger" data-testid="verify-email-resend-error">
				{t(form.resendErrorKey)}
			</div>
		{/if}

		<form
			method="POST"
			action="?/resend"
			data-testid="verify-email-resend-form"
			use:enhance={() => {
				resending = true;
				return async ({ update }) => {
					resending = false;
					await update();
				};
			}}
		>
			<div data-testid="verify-email-resend-email">
				<FormField
					label={t('auth.email')}
					name="email"
					type="email"
					bind:value={resendEmail}
					placeholder="nama@contoh.co.id"
					autocomplete="email"
					required
				/>
			</div>
			<div data-testid="verify-email-resend-submit">
				<LoadingButton type="submit" loading={resending} class="ca-btn ca-btn-primary ca-btn-block">
					{t('feauth.verify.resendSubmit')}
				</LoadingButton>
			</div>
		</form>
	{/if}

	<p class="auth-alt">
		<a href="/login" class="auth-link" data-testid="verify-email-login-link">
			{t('auth.login')}
		</a>
	</p>
{/if}

<style>
	.auth-cta {
		margin-top: 4px;
	}
	.verify-prompt {
		margin: 16px 0 12px;
		font-size: 0.9rem;
		color: #555;
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
