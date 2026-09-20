<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import { invalidateAll } from '$app/navigation';
	import FormField from '$lib/components/FormField.svelte';
	import { t } from '$lib/i18n';
	import { untrack } from 'svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	type Phase = 'infra' | 'restarting' | 'admin' | 'settings';

	let phase = $state<Phase>(untrack(() => (data.status.config_ready ? 'admin' : 'infra')));
	let submittingAction = $state<string | null>(null);

	// Infra step fields - sensible local-dev defaults; blank for anything secret.
	let dbHost = $state('localhost');
	let dbPort = $state('5432');
	let dbUser = $state('root');
	let dbPassword = $state('');
	let dbName = $state('whmcs');
	let dbSslMode = $state('disable');
	let redisAddr = $state('localhost:6379');
	let redisPassword = $state('');
	let rustfsEndpoint = $state('http://localhost:9000');
	let rustfsAccessKey = $state('');
	let rustfsSecretKey = $state('');
	let rustfsBucket = $state('whmcs');
	let rustfsUseSsl = $state(false);
	let mailDriver = $state('log');
	let smtpHost = $state('');
	let smtpPort = $state(587);
	let smtpUser = $state('');
	let smtpPass = $state('');
	let mailHttpUrl = $state('');
	let mailTestRecipient = $state('');

	let adminEmail = $state('');
	let adminPassword = $state('');

	let companyName = $state('');
	let companyEmail = $state('');
	let companyAddress = $state('');

	// react to the most recent action's result
	$effect(() => {
		if (form?.action === 'saveConfig' && form.ok) {
			phase = 'restarting';
		}
		if (form?.action === 'createAdmin' && form.ok) {
			phase = 'settings';
		}
	});

	function resultFor(action: string) {
		return form?.action === action ? form : null;
	}

	// While restarting, poll the colocated status proxy until config_ready
	// flips true, then re-run load() so the server-rendered phase catches up.
	$effect(() => {
		if (phase !== 'restarting') return;
		const timer = setInterval(async () => {
			try {
				const res = await fetch('/install/status');
				if (!res.ok) return;
				const body = (await res.json()) as { config_ready?: boolean; installed?: boolean };
				if (body.config_ready) {
					await invalidateAll();
					phase = 'admin';
				}
			} catch {
				// API still restarting - try again on the next tick.
			}
		}, 1500);
		return () => clearInterval(timer);
	});
</script>

<svelte:head>
	<title>{t('install.title', { name: appName })}</title>
</svelte:head>

<div data-testid="install-wizard">
	{#if phase === 'infra'}
		<div data-testid="install-infra-step">
			<h1 class="ca-h1">{t('install.infra.heading')}</h1>
			<p class="ca-muted">{t('install.infra.intro')}</p>
			{#if data.status.missing?.length}
				<div class="ca-alert-danger" role="alert">
					{t('install.infra.missing', { vars: data.status.missing.join(', ') })}
				</div>
			{/if}

			<form
				method="POST"
				action="?/saveConfig"
				use:enhance={({ action }) => {
					submittingAction = action.search.replace('?/', '');
					return async ({ update }) => {
						submittingAction = null;
						// Never reload load() here: after a successful saveConfig the
						// API is about to self-restart (the "restarting" $effect below
						// polls for it instead), and reloading after a testX click
						// would just be a wasted extra status fetch.
						await update({ invalidateAll: false });
					};
				}}
			>
				<fieldset class="install-section">
					<legend>{t('install.infra.app')}</legend>
					<FormField label={t('install.infra.appBaseUrl')} name="app_base_url" />
					<FormField label={t('install.infra.frontendUrl')} name="frontend_url" />
				</fieldset>

				<fieldset class="install-section">
					<legend>{t('install.infra.database')}</legend>
					<FormField
						label={t('install.infra.dbHost')}
						name="db_host"
						bind:value={dbHost}
						required
					/>
					<FormField
						label={t('install.infra.dbPort')}
						name="db_port"
						bind:value={dbPort}
						required
					/>
					<FormField
						label={t('install.infra.dbUser')}
						name="db_user"
						bind:value={dbUser}
						required
					/>
					<FormField
						label={t('install.infra.dbPassword')}
						name="db_password"
						type="password"
						bind:value={dbPassword}
					/>
					<FormField
						label={t('install.infra.dbName')}
						name="db_name"
						bind:value={dbName}
						required
					/>
					<FormField
						label={t('install.infra.dbSslMode')}
						name="db_sslmode"
						type="select"
						bind:value={dbSslMode}
						options={[
							{ value: 'disable', label: 'disable' },
							{ value: 'require', label: 'require' }
						]}
					/>
					<button
						type="submit"
						formaction="?/testDb"
						class="ca-btn ca-btn-secondary"
						data-testid="test-db-btn"
						disabled={submittingAction === 'testDb'}
					>
						{submittingAction === 'testDb' ? t('install.infra.testing') : t('install.infra.test')}
					</button>
					{#if resultFor('testDb')}
						<span
							data-testid="test-db-result"
							class={resultFor('testDb')?.ok ? 'ca-text-success' : 'ca-text-danger'}
						>
							{resultFor('testDb')?.ok ? t('install.infra.testOk') : resultFor('testDb')?.message}
						</span>
					{/if}
				</fieldset>

				<fieldset class="install-section">
					<legend>{t('install.infra.redis')}</legend>
					<FormField
						label={t('install.infra.redisAddr')}
						name="redis_addr"
						bind:value={redisAddr}
						required
					/>
					<FormField
						label={t('install.infra.redisPassword')}
						name="redis_password"
						type="password"
						bind:value={redisPassword}
					/>
					<button
						type="submit"
						formaction="?/testRedis"
						class="ca-btn ca-btn-secondary"
						data-testid="test-redis-btn"
						disabled={submittingAction === 'testRedis'}
					>
						{submittingAction === 'testRedis'
							? t('install.infra.testing')
							: t('install.infra.test')}
					</button>
					{#if resultFor('testRedis')}
						<span
							data-testid="test-redis-result"
							class={resultFor('testRedis')?.ok ? 'ca-text-success' : 'ca-text-danger'}
						>
							{resultFor('testRedis')?.ok
								? t('install.infra.testOk')
								: resultFor('testRedis')?.message}
						</span>
					{/if}
				</fieldset>

				<fieldset class="install-section">
					<legend>{t('install.infra.storage')}</legend>
					<FormField
						label={t('install.infra.storageEndpoint')}
						name="rustfs_endpoint"
						bind:value={rustfsEndpoint}
						required
					/>
					<FormField
						label={t('install.infra.storageAccessKey')}
						name="rustfs_access_key"
						bind:value={rustfsAccessKey}
					/>
					<FormField
						label={t('install.infra.storageSecretKey')}
						name="rustfs_secret_key"
						type="password"
						bind:value={rustfsSecretKey}
					/>
					<FormField
						label={t('install.infra.storageBucket')}
						name="rustfs_bucket"
						bind:value={rustfsBucket}
						required
					/>
					<FormField
						label={t('install.infra.storageUseSsl')}
						name="rustfs_use_ssl"
						type="checkbox"
						bind:value={rustfsUseSsl}
					/>
					<button
						type="submit"
						formaction="?/testS3"
						class="ca-btn ca-btn-secondary"
						data-testid="test-s3-btn"
						disabled={submittingAction === 'testS3'}
					>
						{submittingAction === 'testS3' ? t('install.infra.testing') : t('install.infra.test')}
					</button>
					{#if resultFor('testS3')}
						<span
							data-testid="test-s3-result"
							class={resultFor('testS3')?.ok ? 'ca-text-success' : 'ca-text-danger'}
						>
							{resultFor('testS3')?.ok ? t('install.infra.testOk') : resultFor('testS3')?.message}
						</span>
					{/if}
				</fieldset>

				<fieldset class="install-section">
					<legend>{t('install.infra.mail')}</legend>
					<FormField
						label={t('install.infra.mailDriver')}
						name="mail_driver"
						type="select"
						bind:value={mailDriver}
						options={[
							{ value: 'log', label: t('install.infra.mailDriverLog') },
							{ value: 'smtp', label: t('install.infra.mailDriverSmtp') },
							{ value: 'http', label: t('install.infra.mailDriverHttp') }
						]}
					/>
					{#if mailDriver === 'smtp'}
						<FormField label={t('install.infra.smtpHost')} name="smtp_host" bind:value={smtpHost} />
						<FormField
							label={t('install.infra.smtpPort')}
							name="smtp_port"
							type="number"
							bind:value={smtpPort}
						/>
						<FormField label={t('install.infra.smtpUser')} name="smtp_user" bind:value={smtpUser} />
						<FormField
							label={t('install.infra.smtpPass')}
							name="smtp_pass"
							type="password"
							bind:value={smtpPass}
						/>
					{:else if mailDriver === 'http'}
						<FormField
							label={t('install.infra.mailHttpUrl')}
							name="mail_http_url"
							bind:value={mailHttpUrl}
						/>
					{/if}
					{#if mailDriver !== 'log'}
						<FormField
							label="Test recipient"
							name="mail_test_recipient"
							type="email"
							bind:value={mailTestRecipient}
						/>
						<button
							type="submit"
							formaction="?/testMail"
							class="ca-btn ca-btn-secondary"
							data-testid="test-mail-btn"
							disabled={submittingAction === 'testMail'}
						>
							{submittingAction === 'testMail'
								? t('install.infra.testing')
								: t('install.infra.test')}
						</button>
						{#if resultFor('testMail')}
							<span
								data-testid="test-mail-result"
								class={resultFor('testMail')?.ok ? 'ca-text-success' : 'ca-text-danger'}
							>
								{resultFor('testMail')?.ok
									? t('install.infra.testOk')
									: resultFor('testMail')?.message}
							</span>
						{/if}
					{/if}
				</fieldset>

				{#if resultFor('saveConfig') && !resultFor('saveConfig')?.ok}
					<div class="ca-alert-danger" role="alert" data-testid="save-config-error">
						{resultFor('saveConfig')?.message}
					</div>
				{/if}

				<button
					type="submit"
					class="ca-btn ca-btn-primary"
					data-testid="save-config-btn"
					disabled={submittingAction === 'saveConfig'}
				>
					{submittingAction === 'saveConfig'
						? t('install.infra.saving')
						: t('install.infra.saveAndContinue')}
				</button>
			</form>
		</div>
	{:else if phase === 'restarting'}
		<div data-testid="install-restarting-step">
			<h1 class="ca-h1">{t('install.restarting.heading')}</h1>
			<p class="ca-muted">{t('install.restarting.body')}</p>
		</div>
	{:else if phase === 'admin'}
		<div data-testid="install-admin-step">
			<h1 class="ca-h1">{t('install.admin.heading')}</h1>
			<p class="ca-muted">{t('install.admin.intro', { name: appName })}</p>

			{#if resultFor('createAdmin') && !resultFor('createAdmin')?.ok}
				<div class="ca-alert-danger" role="alert">{resultFor('createAdmin')?.message}</div>
			{/if}

			<form
				method="POST"
				action="?/createAdmin"
				use:enhance={() => {
					submittingAction = 'createAdmin';
					return async ({ update }) => {
						submittingAction = null;
						// Not invalidateAll: load() redirects to /login once installed,
						// which would fire immediately after this succeeds and skip the
						// settings step below entirely.
						await update({ invalidateAll: false });
					};
				}}
			>
				<FormField
					label={t('install.admin.email')}
					name="email"
					type="email"
					bind:value={adminEmail}
					required
				/>
				<FormField
					label={t('install.admin.password')}
					name="password"
					type="password"
					hint={t('install.admin.passwordHint')}
					bind:value={adminPassword}
					required
				/>
				<button
					type="submit"
					class="ca-btn ca-btn-primary"
					data-testid="create-admin-btn"
					disabled={submittingAction === 'createAdmin'}
				>
					{submittingAction === 'createAdmin'
						? t('install.admin.submitting')
						: t('install.admin.submit')}
				</button>
			</form>
		</div>
	{:else if phase === 'settings'}
		<div data-testid="install-settings-step">
			<h1 class="ca-h1">{t('install.settings.heading')}</h1>
			<p class="ca-muted">{t('install.settings.intro')}</p>

			{#if resultFor('saveSettings') && !resultFor('saveSettings')?.ok}
				<div class="ca-alert-danger" role="alert">{resultFor('saveSettings')?.message}</div>
			{/if}

			<form
				method="POST"
				action="?/saveSettings"
				use:enhance={() => {
					submittingAction = 'saveSettings';
					return async ({ update }) => {
						submittingAction = null;
						await update();
					};
				}}
			>
				<FormField
					label={t('install.settings.companyName')}
					name="company_name"
					bind:value={companyName}
				/>
				<FormField
					label={t('install.settings.companyEmail')}
					name="company_email"
					type="email"
					bind:value={companyEmail}
				/>
				<FormField
					label={t('install.settings.companyAddress')}
					name="company_address"
					bind:value={companyAddress}
				/>
				<button
					type="submit"
					class="ca-btn ca-btn-primary"
					data-testid="save-settings-btn"
					disabled={submittingAction === 'saveSettings'}
				>
					{submittingAction === 'saveSettings'
						? t('install.settings.submitting')
						: t('install.settings.submit')}
				</button>
			</form>
			<a href="/login" data-testid="skip-settings-link">{t('install.settings.skip')}</a>
		</div>
	{/if}
</div>
