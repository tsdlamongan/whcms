<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import Alert from '$lib/components/Alert.svelte';
	import Breadcrumb from '$lib/components/Breadcrumb.svelte';
	import DateText from '$lib/components/DateText.svelte';
	import FormField from '$lib/components/FormField.svelte';
	import LoadingButton from '$lib/components/LoadingButton.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import MoneyText from '$lib/components/MoneyText.svelte';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import Tabs from '$lib/components/Tabs.svelte';
	import type { BreadcrumbItem, SelectOption, TabItem } from '$lib/components/types';
	import { t } from '$lib/i18n';
	import { toast } from '$lib/stores/toast.svelte';
	import type { SubmitFunction } from '@sveltejs/kit';
	import { untrack } from 'svelte';
	import type { PageProps } from './$types';
	import ContactForm from './ContactForm.svelte';
	import DnsEditor from './DnsEditor.svelte';

	let { data, form }: PageProps = $props();

	const domain = $derived(data.domain);

	const breadcrumb: BreadcrumbItem[] = $derived([
		{ label: t('nav.home'), href: '/dashboard' },
		{ label: t('nav.domains'), href: '/domains' },
		{ label: domain?.name ?? '…' }
	]);

	const tabs: TabItem[] = $derived([
		{ id: 'overview', label: t('clientDomains.tabs.overview'), href: '?tab=overview' },
		{ id: 'nameservers', label: t('clientDomains.tabs.nameservers'), href: '?tab=nameservers' },
		{ id: 'dns', label: t('clientDomains.tabs.dns'), href: '?tab=dns' },
		{ id: 'epp', label: t('clientDomains.tabs.epp'), href: '?tab=epp' },
		{ id: 'contact', label: t('clientDomains.tabs.contact'), href: '?tab=contact' },
		{ id: 'addons', label: t('clientDomains.tabs.addons'), href: '?tab=addons' }
	]);

	/** Error text for a given form section (translated key or raw API message). */
	function sectionError(section: string): string | null {
		if (form?.section !== section) return null;
		if (form?.errorKey) {
			const params =
				'errorParams' in form
					? (form.errorParams as Record<string, string | number> | undefined)
					: undefined;
			return t(form.errorKey, params);
		}
		return form?.errorMessage ?? null;
	}

	// Renew modal
	let renewOpen = $state(false);
	let renewYears = $state('1');
	let renewSubmitting = $state(false);

	const yearOptions: SelectOption[] = $derived(
		['1', '2', '3'].map((n) => ({ value: n, label: t('clientDomains.detail.years', { n }) }))
	);
	const renewTotal = $derived((domain?.recurring_amount ?? 0) * (Number(renewYears) || 1));
	const canRenew = $derived(domain?.status === 'active' || domain?.status === 'expired');

	const renewEnhance: SubmitFunction = () => {
		renewSubmitting = true;
		return async ({ result, update }) => {
			renewSubmitting = false;
			if (result.type === 'redirect' || result.type === 'success') {
				renewOpen = false;
				toast.success(t('clientDomains.toast.renewCreated'));
			}
			await update();
		};
	};

	// Auto renew toggle (overview)
	let toggleSubmitting = $state(false);
	const toggleEnhance: SubmitFunction = () => {
		toggleSubmitting = true;
		const next = !domain?.auto_renew;
		return async ({ result, update }) => {
			toggleSubmitting = false;
			if (result.type === 'success') {
				toast.success(
					t(next ? 'clientDomains.toast.autoRenewOn' : 'clientDomains.toast.autoRenewOff')
				);
			} else if (result.type === 'failure' || result.type === 'error') {
				toast.error(t('clientDomains.errors.actionFailed'));
			}
			await update();
		};
	};

	// Nameservers form
	let ns1 = $state(untrack(() => data.nameservers[0] ?? ''));
	let ns2 = $state(untrack(() => data.nameservers[1] ?? ''));
	let ns3 = $state(untrack(() => data.nameservers[2] ?? ''));
	let ns4 = $state(untrack(() => data.nameservers[3] ?? ''));
	let nsSubmitting = $state(false);

	const nsEnhance: SubmitFunction = () => {
		nsSubmitting = true;
		return async ({ result, update }) => {
			nsSubmitting = false;
			if (result.type === 'success') {
				toast.success(t('clientDomains.ns.saved'));
			}
			await update();
		};
	};

	// EPP reveal
	let eppSubmitting = $state(false);
	const eppEnhance: SubmitFunction = () => {
		eppSubmitting = true;
		return async ({ result, update }) => {
			eppSubmitting = false;
			await update();
		};
	};

	async function copyEpp(code: string) {
		try {
			await navigator.clipboard.writeText(code);
			toast.success(t('clientDomains.epp.copied'));
		} catch {
			toast.error(t('clientDomains.errors.actionFailed'));
		}
	}

	// Remount keys so tab data loaded after mount re-initializes editor state.
	const dnsKey = $derived(JSON.stringify(data.dns));
	const contactKey = $derived(JSON.stringify(data.contact));

	// Domain addons form
	const savedAddonKeys = $derived(
		[
			domain?.id_protection ? 'id_protection' : null,
			domain?.dns_management_enabled ? 'dns_management' : null,
			domain?.email_forwarding_enabled ? 'email_forwarding' : null
		].filter((k): k is string => k !== null)
	);
	let pendingAddonKeys = $state<string[] | null>(null);
	const activeAddonKeys = $derived(pendingAddonKeys ?? savedAddonKeys);
	function toggleAddonKey(key: string) {
		const cur = new Set(activeAddonKeys);
		if (cur.has(key)) cur.delete(key);
		else cur.add(key);
		pendingAddonKeys = [...cur];
	}
	let addonsSubmitting = $state(false);
	const addonsEnhance: SubmitFunction = () => {
		addonsSubmitting = true;
		return async ({ result, update }) => {
			addonsSubmitting = false;
			if (result.type === 'success') {
				pendingAddonKeys = null;
				toast.success(t('clientDomains.addons.saved'));
			}
			await update();
		};
	};
</script>

<svelte:head>
	<title>{domain?.name ?? t('nav.domains')} — {appName}</title>
</svelte:head>

<Breadcrumb items={breadcrumb} />

{#if !domain}
	<div data-testid="domain-not-found">
		<Alert type="error" title={t('clientDomains.detail.notFound')}>
			{data.domainError?.message ?? ''}
		</Alert>
		<a href="/domains" class="mt-4 inline-block text-sm text-primary hover:underline">
			← {t('clientDomains.detail.back')}
		</a>
	</div>
{:else}
	<div class="mb-5 flex flex-wrap items-center justify-between gap-3">
		<div class="flex items-center gap-3">
			<h1 class="ca-h1" style="margin-bottom:0" data-testid="domain-name">{domain.name}</h1>
			<span data-testid="domain-status">
				<StatusBadge status={domain.status} />
			</span>
		</div>
		{#if canRenew}
			<span data-testid="domain-renew-button">
				<LoadingButton onclick={() => (renewOpen = true)}>
					{t('clientDomains.detail.renewNow')}
				</LoadingButton>
			</span>
		{/if}
	</div>

	<div class="mb-5" data-testid="domain-tabs">
		<Tabs {tabs} active={data.tab} />
	</div>

	{#if data.tab === 'overview'}
		{@const overviewError = sectionError('overview')}
		<div class="ca-card" data-testid="domain-overview">
			<h2 class="ca-card-header">
				{t('clientDomains.detail.overviewTitle')}
			</h2>
			<div class="ca-card-body">
				{#if overviewError}
					<div class="mb-4">
						<Alert type="error">{overviewError}</Alert>
					</div>
				{/if}

				<dl class="grid grid-cols-1 gap-x-8 gap-y-3 text-sm sm:grid-cols-2">
					<div class="flex items-center justify-between gap-4 border-b border-gray-100 pb-2">
						<dt class="text-gray-500">{t('clientDomains.detail.status')}</dt>
						<dd><StatusBadge status={domain.status} /></dd>
					</div>
					<div class="flex items-center justify-between gap-4 border-b border-gray-100 pb-2">
						<dt class="text-gray-500">{t('clientDomains.detail.registrationDate')}</dt>
						<dd class="font-medium text-gray-800">
							<DateText value={domain.registration_date} />
						</dd>
					</div>
					<div class="flex items-center justify-between gap-4 border-b border-gray-100 pb-2">
						<dt class="text-gray-500">{t('clientDomains.detail.expiryDate')}</dt>
						<dd class="font-medium text-gray-800" data-testid="domain-expiry">
							<DateText value={domain.expiry_date} />
						</dd>
					</div>
					<div class="flex items-center justify-between gap-4 border-b border-gray-100 pb-2">
						<dt class="text-gray-500">{t('clientDomains.detail.nextDueDate')}</dt>
						<dd class="font-medium text-gray-800">
							<DateText value={domain.next_due_date} />
						</dd>
					</div>
					<div class="flex items-center justify-between gap-4 border-b border-gray-100 pb-2">
						<dt class="text-gray-500">{t('clientDomains.detail.billingCycle')}</dt>
						<dd class="font-medium text-gray-800">
							{t(`clientDomains.cycle.${domain.billing_cycle}`)}
						</dd>
					</div>
					<div class="flex items-center justify-between gap-4 border-b border-gray-100 pb-2">
						<dt class="text-gray-500">{t('clientDomains.detail.recurringAmount')}</dt>
						<dd class="font-medium text-gray-800">
							<MoneyText amount={domain.recurring_amount} />
						</dd>
					</div>
					<div class="flex items-center justify-between gap-4 border-b border-gray-100 pb-2">
						<dt class="text-gray-500">{t('clientDomains.detail.autoRenew')}</dt>
						<dd>
							<form
								method="POST"
								action="?/autorenew"
								use:enhance={toggleEnhance}
								class="inline-flex"
							>
								<input type="hidden" name="value" value={domain.auto_renew ? 'false' : 'true'} />
								<button
									type="submit"
									role="switch"
									aria-checked={domain.auto_renew}
									aria-label={t('clientDomains.detail.autoRenew')}
									disabled={toggleSubmitting}
									data-testid="domain-autorenew-toggle"
									class={`relative inline-flex h-5 w-9 items-center rounded-full transition disabled:opacity-60 ${
										domain.auto_renew ? 'bg-success' : 'bg-gray-300'
									}`}
								>
									<span
										class={`inline-block h-4 w-4 transform rounded-full bg-white shadow transition ${
											domain.auto_renew ? 'translate-x-4' : 'translate-x-0.5'
										}`}
									></span>
								</button>
							</form>
						</dd>
					</div>
					<div class="flex items-center justify-between gap-4 border-b border-gray-100 pb-2">
						<dt class="text-gray-500">{t('clientDomains.detail.idProtection')}</dt>
						<dd class="font-medium text-gray-800">
							{domain.id_protection ? t('common.yes') : t('common.no')}
						</dd>
					</div>
				</dl>
			</div>
		</div>
	{:else if data.tab === 'nameservers'}
		{@const nsError = sectionError('nameservers')}
		<div class="ca-card max-w-lg">
			<h2 class="ca-card-header">{t('clientDomains.ns.title')}</h2>
			<div class="ca-card-body">
				<p class="ca-muted mb-4">{t('clientDomains.ns.desc')}</p>

				{#if nsError}
					<div class="mb-4" data-testid="ns-error">
						<Alert type="error">{nsError}</Alert>
					</div>
				{/if}

				<form method="POST" action="?/nameservers" use:enhance={nsEnhance} data-testid="ns-form">
					<div data-testid="ns-field-1">
						<FormField
							label={t('clientDomains.ns.label', { n: 1 })}
							name="ns1"
							bind:value={ns1}
							placeholder="ns1.example.com"
							required
						/>
					</div>
					<div data-testid="ns-field-2">
						<FormField
							label={t('clientDomains.ns.label', { n: 2 })}
							name="ns2"
							bind:value={ns2}
							placeholder="ns2.example.com"
							required
						/>
					</div>
					<div data-testid="ns-field-3">
						<FormField
							label={t('clientDomains.ns.label', { n: 3 })}
							name="ns3"
							bind:value={ns3}
							hint={t('clientDomains.ns.optionalHint')}
						/>
					</div>
					<div data-testid="ns-field-4">
						<FormField
							label={t('clientDomains.ns.label', { n: 4 })}
							name="ns4"
							bind:value={ns4}
							hint={t('clientDomains.ns.optionalHint')}
						/>
					</div>
					<div class="flex justify-end">
						<span data-testid="ns-save">
							<LoadingButton type="submit" loading={nsSubmitting}>
								{t('clientDomains.ns.save')}
							</LoadingButton>
						</span>
					</div>
				</form>
			</div>
		</div>
	{:else if data.tab === 'dns'}
		{#if data.dnsAddonRequired}
			<div class="ca-card max-w-lg" data-testid="dns-addon-required">
				<h2 class="ca-card-header">{t('clientDomains.addons.dnsRequiredTitle')}</h2>
				<div class="ca-card-body">
					<p class="ca-muted mb-4">{t('clientDomains.addons.dnsRequiredDesc')}</p>
					<a href="?tab=addons" class="ca-btn ca-btn-primary"
						>{t('clientDomains.addons.goToAddons')}</a
					>
				</div>
			</div>
		{:else if data.dnsError}
			<div data-testid="dns-load-error">
				<Alert type="error">
					{t('clientDomains.errors.loadFailed', { message: data.dnsError })}
				</Alert>
			</div>
		{:else}
			{#key dnsKey}
				<DnsEditor records={data.dns ?? []} errorText={sectionError('dns')} />
			{/key}
		{/if}
	{:else if data.tab === 'addons'}
		{@const addonsError = sectionError('addons')}
		<div class="ca-card max-w-lg" data-testid="domain-addons">
			<h2 class="ca-card-header">{t('clientDomains.addons.title')}</h2>
			<div class="ca-card-body">
				<p class="ca-muted mb-4">{t('clientDomains.addons.desc')}</p>

				{#if addonsError}
					<div class="mb-4" data-testid="addons-error">
						<Alert type="error">{addonsError}</Alert>
					</div>
				{/if}

				<form method="POST" action="?/addons" use:enhance={addonsEnhance} data-testid="addons-form">
					<div class="space-y-3 mb-4">
						{#each data.addons as addon (addon.key)}
							<label
								class="flex items-center justify-between gap-4 rounded-md border border-gray-200 px-3 py-2"
								data-testid={`addon-row-${addon.key}`}
							>
								<span class="flex items-center gap-2">
									<input
										type="checkbox"
										name="addons"
										value={addon.key}
										checked={activeAddonKeys.includes(addon.key)}
										onchange={() => toggleAddonKey(addon.key)}
										data-testid={`addon-checkbox-${addon.key}`}
									/>
									<span class="font-medium text-gray-800">{addon.name}</span>
								</span>
								<span class="ca-muted text-sm">
									<MoneyText amount={addon.price} />{t('clientDomains.addons.perYear')}
								</span>
							</label>
						{/each}
					</div>

					<div class="flex justify-end">
						<span data-testid="addons-save">
							<LoadingButton type="submit" loading={addonsSubmitting}>
								{t('clientDomains.addons.save')}
							</LoadingButton>
						</span>
					</div>
				</form>
			</div>
		</div>
	{:else if data.tab === 'epp'}
		{@const eppError = sectionError('epp')}
		<div class="ca-card max-w-lg">
			<h2 class="ca-card-header">{t('clientDomains.epp.title')}</h2>
			<div class="ca-card-body">
				<p class="ca-muted mb-4">{t('clientDomains.epp.desc')}</p>

				{#if eppError}
					<div class="mb-4" data-testid="epp-error">
						<Alert type="error">{eppError}</Alert>
					</div>
				{/if}

				{#if form?.section === 'epp' && form?.eppCode}
					<div class="mb-4">
						<label class="ca-label" for="epp-code">
							{t('clientDomains.epp.codeLabel')}
						</label>
						<div class="ca-input-group">
							<input
								id="epp-code"
								readonly
								value={form.eppCode}
								class="ca-input font-mono"
								style="background:#f7f7f7"
								data-testid="epp-code"
								onfocus={(e) => (e.currentTarget as HTMLInputElement).select()}
							/>
							<div class="ca-input-group-append">
								<button
									type="button"
									class="ca-btn ca-btn-default"
									onclick={() => copyEpp(String(form?.eppCode ?? ''))}
									data-testid="epp-copy"
								>
									{t('clientDomains.epp.copy')}
								</button>
							</div>
						</div>
					</div>
				{:else}
					<form method="POST" action="?/epp" use:enhance={eppEnhance}>
						<span data-testid="epp-reveal-button">
							<LoadingButton type="submit" loading={eppSubmitting}>
								{t('clientDomains.epp.reveal')}
							</LoadingButton>
						</span>
					</form>
				{/if}
			</div>
		</div>
	{:else if data.tab === 'contact'}
		{#if data.contactError}
			<div class="mb-4" data-testid="contact-load-error">
				<Alert type="warning">
					{t('clientDomains.contact.loadFailed', { message: data.contactError })}
				</Alert>
			</div>
		{/if}
		{#key contactKey}
			<ContactForm
				contact={data.contact}
				errorText={sectionError('contact')}
				fieldErrors={form?.section === 'contact' ? (form?.fieldErrors ?? null) : null}
				values={form?.section === 'contact' ? (form?.values ?? null) : null}
			/>
		{/key}
	{/if}

	<Modal bind:open={renewOpen} title={t('clientDomains.detail.renewTitle')}>
		{@const renewError = sectionError('renew')}
		<form method="POST" action="?/renew" use:enhance={renewEnhance} data-testid="domain-renew-form">
			{#if renewError}
				<div class="mb-3" data-testid="renew-error">
					<Alert type="error">{renewError}</Alert>
				</div>
			{/if}

			<FormField
				label={t('clientDomains.detail.renewPeriod')}
				name="years"
				type="select"
				bind:value={renewYears}
				options={yearOptions}
			/>

			<div class="mb-4 flex items-center justify-between rounded-md bg-gray-50 px-3 py-2 text-sm">
				<span class="text-gray-600">{t('clientDomains.detail.renewTotal')}</span>
				<MoneyText amount={renewTotal} class="font-semibold text-gray-800" />
			</div>

			<p class="mb-4 text-xs text-gray-500">{t('clientDomains.detail.renewHint')}</p>

			<div class="flex justify-end gap-2">
				<LoadingButton
					variant="secondary"
					onclick={() => (renewOpen = false)}
					disabled={renewSubmitting}
				>
					{t('action.cancel')}
				</LoadingButton>
				<span data-testid="domain-renew-submit">
					<LoadingButton type="submit" loading={renewSubmitting}>
						{t('clientDomains.detail.renewNow')}
					</LoadingButton>
				</span>
			</div>
		</form>
	</Modal>
{/if}
