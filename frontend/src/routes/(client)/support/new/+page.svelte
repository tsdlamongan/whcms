<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import Alert from '$lib/components/Alert.svelte';
	import Breadcrumb from '$lib/components/Breadcrumb.svelte';
	import FormField from '$lib/components/FormField.svelte';
	import LoadingButton from '$lib/components/LoadingButton.svelte';
	import { t } from '$lib/i18n';
	import { toast } from '$lib/stores/toast.svelte';
	import { untrack } from 'svelte';
	import type { PageProps } from './$types';
	import { TICKET_PRIORITIES } from '../types';

	let { data, form }: PageProps = $props();

	let departmentId = $state(untrack(() => form?.values?.department_id ?? ''));
	let subject = $state(untrack(() => form?.values?.subject ?? ''));
	let priority = $state(untrack(() => form?.values?.priority ?? 'medium'));
	let message = $state(untrack(() => form?.values?.message ?? ''));
	let submitting = $state(false);

	const departmentOptions = $derived(
		data.departments.map((d) => ({ value: String(d.id), label: d.name }))
	);
	const priorityOptions = $derived(
		TICKET_PRIORITIES.map((p) => ({ value: p, label: t(`supportfe.priority.${p}`) }))
	);

	const errorText = $derived(form?.errorMessage ?? (form?.errorKey ? t(form.errorKey) : null));

	function fieldError(name: string): string | undefined {
		const key = form?.fieldErrors?.[name];
		return key ? t(key) : undefined;
	}
</script>

<svelte:head>
	<title>{t('supportfe.form.title')} — {appName}</title>
</svelte:head>

<Breadcrumb
	items={[
		{ label: t('nav.home'), href: '/dashboard' },
		{ label: t('nav.support'), href: '/support' },
		{ label: t('supportfe.form.title') }
	]}
/>

<h1 class="ca-h1" style="margin-bottom:4px">{t('supportfe.form.title')}</h1>
<p class="ca-muted" style="margin-bottom:20px">{t('supportfe.form.subtitle')}</p>

{#if data.loadError}
	<div class="mb-4">
		<Alert type="error" title={t('supportfe.form.departmentsLoadFailed')}>{data.loadError}</Alert>
	</div>
{/if}

{#if errorText}
	<div class="mb-4">
		<Alert type="error">{errorText}</Alert>
	</div>
{/if}

<div class="ca-card ca-card-body max-w-2xl">
	<form
		method="POST"
		enctype="multipart/form-data"
		data-testid="ticket-form"
		use:enhance={() => {
			submitting = true;
			return async ({ result, update }) => {
				submitting = false;
				if (result.type === 'redirect') {
					toast.success(t('supportfe.form.created'));
				}
				await update({ reset: false });
			};
		}}
	>
		<div data-testid="ticket-department">
			<FormField
				label={t('supportfe.form.department')}
				name="department_id"
				type="select"
				bind:value={departmentId}
				options={departmentOptions}
				placeholder={t('supportfe.form.departmentPlaceholder')}
				error={fieldError('department_id')}
				required
			/>
		</div>

		<div data-testid="ticket-subject">
			<FormField
				label={t('supportfe.form.subject')}
				name="subject"
				type="text"
				bind:value={subject}
				placeholder={t('supportfe.form.subjectPlaceholder')}
				error={fieldError('subject')}
				required
			/>
		</div>

		<div data-testid="ticket-priority">
			<FormField
				label={t('supportfe.form.priority')}
				name="priority"
				type="select"
				bind:value={priority}
				options={priorityOptions}
				error={fieldError('priority')}
				required
			/>
		</div>

		<div data-testid="ticket-message">
			<FormField
				label={t('supportfe.form.message')}
				name="message"
				type="textarea"
				rows={7}
				bind:value={message}
				placeholder={t('supportfe.form.messagePlaceholder')}
				error={fieldError('message')}
				required
			/>
		</div>

		<div class="mb-5">
			<label class="ca-label" for="field-attachments">
				{t('supportfe.form.attachments')}
			</label>
			<input
				id="field-attachments"
				name="attachments"
				type="file"
				multiple
				accept=".jpg,.jpeg,.png,.gif,.pdf,.zip,.txt,.log"
				data-testid="ticket-attachments"
				class="block w-full cursor-pointer rounded-md border border-gray-300 bg-white text-sm text-gray-600 shadow-sm file:mr-3 file:cursor-pointer file:rounded-l-md file:border-0 file:bg-gray-100 file:px-3 file:py-2 file:text-sm file:font-medium file:text-gray-700 hover:file:bg-gray-200"
			/>
			<p class="mt-1 text-xs text-gray-500">{t('supportfe.form.attachmentsHint', { size: 8 })}</p>
		</div>

		<div class="flex items-center gap-3">
			<span data-testid="ticket-submit">
				<LoadingButton type="submit" loading={submitting}>
					{t('supportfe.form.submit')}
				</LoadingButton>
			</span>
			<a href="/support" class="text-sm text-gray-500 hover:text-primary hover:underline">
				{t('action.cancel')}
			</a>
		</div>
	</form>
</div>
