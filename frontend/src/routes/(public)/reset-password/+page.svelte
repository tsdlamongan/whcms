<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import FormField from '$lib/components/FormField.svelte';
	import LoadingButton from '$lib/components/LoadingButton.svelte';
	import { t } from '$lib/i18n';
	import { toast } from '$lib/stores/toast.svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	let password = $state('');
	let confirmPassword = $state('');
	let submitting = $state(false);

	const fieldError = (name: string): string | undefined => {
		const key = form?.fieldErrors?.[name];
		return key ? t(key) : undefined;
	};

	const topError = $derived(form?.errorKey ? t(form.errorKey) : (form?.errorMessage ?? null));
</script>

<svelte:head>
	<title>{t('auth.resetPassword')} — {appName}</title>
</svelte:head>

<h1 class="ca-h1">{t('auth.resetPassword')}</h1>
<p class="ca-muted auth-subtitle">{t('feauth.reset.subtitle')}</p>

{#if !data.token}
	<div class="ca-alert-danger" data-testid="reset-password-invalid">
		<strong>{t('feauth.reset.invalidTitle')}</strong>
		<div>{t('feauth.reset.missingToken')}</div>
	</div>

	<a
		href="/forgot-password"
		class="ca-btn ca-btn-primary ca-btn-block auth-cta"
		data-testid="reset-password-request-new"
	>
		{t('feauth.reset.requestNew')}
	</a>
{:else}
	{#if topError}
		<div class="ca-alert-danger" data-testid="reset-password-error">
			{topError}
			{#if form?.tokenRejected}
				<a
					href="/forgot-password"
					class="reset-inline-link"
					data-testid="reset-password-request-new"
				>
					{t('feauth.reset.requestNew')}
				</a>
			{/if}
		</div>
	{/if}

	<form
		method="POST"
		data-testid="reset-password-form"
		use:enhance={() => {
			submitting = true;
			return async ({ result, update }) => {
				submitting = false;
				if (result.type === 'redirect') {
					toast.success(t('feauth.reset.success'));
				}
				await update();
			};
		}}
	>
		<input type="hidden" name="token" value={data.token} />

		<div data-testid="reset-password-password">
			<FormField
				label={t('feauth.reset.newPassword')}
				name="password"
				type="password"
				bind:value={password}
				error={fieldError('password')}
				hint={t('feauth.register.passwordHint')}
				autocomplete="new-password"
				required
			/>
		</div>

		<div data-testid="reset-password-confirm">
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

		<div data-testid="reset-password-submit">
			<LoadingButton type="submit" loading={submitting} class="ca-btn ca-btn-primary ca-btn-block">
				{t('feauth.reset.submit')}
			</LoadingButton>
		</div>
	</form>

	<p class="auth-alt">
		<a href="/login" class="auth-link" data-testid="reset-password-login-link">
			{t('auth.login')}
		</a>
	</p>
{/if}

<style>
	.auth-subtitle {
		margin-top: -8px;
		margin-bottom: 20px;
	}
	.auth-cta {
		margin-top: 4px;
	}
	.reset-inline-link {
		display: block;
		margin-top: 6px;
		font-weight: 600;
		color: inherit;
		text-decoration: underline;
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
