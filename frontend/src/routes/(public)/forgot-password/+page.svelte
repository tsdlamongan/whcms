<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import FormField from '$lib/components/FormField.svelte';
	import LoadingButton from '$lib/components/LoadingButton.svelte';
	import { t } from '$lib/i18n';
	import { untrack } from 'svelte';
	import type { PageProps } from './$types';

	let { form }: PageProps = $props();

	let email = $state(untrack(() => form?.email ?? ''));
	let submitting = $state(false);
</script>

<svelte:head>
	<title>{t('auth.forgotPassword')} — {appName}</title>
</svelte:head>

<h1 class="ca-h1">{t('auth.resetPassword')}</h1>
<p class="ca-muted auth-subtitle">{t('feauth.forgot.subtitle')}</p>

{#if form?.success}
	<div class="ca-alert-success" data-testid="forgot-password-success">
		<strong>{t('feauth.forgot.successTitle')}</strong>
		<div>{t('feauth.forgot.successBody', { email: form.email })}</div>
	</div>

	<p class="auth-alt">
		<a href="/login" class="auth-link" data-testid="forgot-password-login-link">
			{t('feauth.forgot.backToLogin')}
		</a>
	</p>
{:else}
	<form
		method="POST"
		data-testid="forgot-password-form"
		use:enhance={() => {
			submitting = true;
			return async ({ update }) => {
				submitting = false;
				await update();
			};
		}}
	>
		<div data-testid="forgot-password-email">
			<FormField
				label={t('auth.email')}
				name="email"
				type="email"
				bind:value={email}
				error={form?.fieldErrorKey ? t(form.fieldErrorKey) : undefined}
				placeholder="nama@contoh.co.id"
				autocomplete="email"
				required
			/>
		</div>

		<div data-testid="forgot-password-submit">
			<LoadingButton type="submit" loading={submitting} class="ca-btn ca-btn-primary ca-btn-block">
				{t('feauth.forgot.submit')}
			</LoadingButton>
		</div>
	</form>

	<p class="auth-alt">
		<a href="/login" class="auth-link" data-testid="forgot-password-login-link">
			{t('feauth.forgot.backToLogin')}
		</a>
	</p>
{/if}

<style>
	.auth-subtitle {
		margin-top: -8px;
		margin-bottom: 20px;
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
