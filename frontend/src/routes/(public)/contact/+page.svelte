<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import { FormField } from '$lib/components';
	import type { SelectOption } from '$lib/components/types';
	import { t } from '$lib/i18n';
	import { untrack } from 'svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	let name = $state(untrack(() => form?.values?.name ?? ''));
	let email = $state(untrack(() => form?.values?.email ?? ''));
	let subject = $state(untrack(() => form?.values?.subject ?? ''));
	let message = $state(untrack(() => form?.values?.message ?? ''));
	let departmentId = $state(untrack(() => form?.values?.department_id ?? ''));
	let submitting = $state(false);

	const deptOptions = $derived<SelectOption[]>([
		{ value: '', label: t('portal.contact.departmentPlaceholder') },
		...data.departments.filter((d) => d.active).map((d) => ({ value: String(d.id), label: d.name }))
	]);

	const errorText = $derived(form?.errorKey ? t(form.errorKey) : (form?.errorMessage ?? null));
</script>

<svelte:head>
	<title>{t('portal.contact.title')} — {appName}</title>
</svelte:head>

<h1 class="ca-h1">{t('portal.contact.title')}</h1>
<p class="ca-lead">{t('portal.contact.subtitle')}</p>

{#if form?.success}
	<div class="ca-alert-success" role="status" data-testid="contact-success">
		<i class="fas fa-check-circle" aria-hidden="true"></i>
		{t('portal.contact.success', { number: form.ticketNumber ?? '' })}
	</div>
{:else}
	{#if errorText}
		<div class="ca-alert-danger" role="alert" data-testid="contact-error">{errorText}</div>
	{/if}

	<section class="ca-card">
		<div class="ca-card-body">
			<form
				method="POST"
				data-testid="contact-form"
				use:enhance={() => {
					submitting = true;
					return async ({ update }) => {
						submitting = false;
						await update();
					};
				}}
			>
				<FormField label={t('portal.contact.name')} name="name" bind:value={name} required />
				<FormField
					label={t('portal.contact.email')}
					name="email"
					type="email"
					autocomplete="email"
					bind:value={email}
					required
				/>
				<FormField
					label={t('portal.contact.subject')}
					name="subject"
					bind:value={subject}
					required
				/>
				{#if deptOptions.length > 1}
					<FormField
						label={t('portal.contact.department')}
						name="department_id"
						type="select"
						options={deptOptions}
						bind:value={departmentId}
					/>
				{/if}
				<FormField
					label={t('portal.contact.message')}
					name="message"
					type="textarea"
					rows={6}
					bind:value={message}
					required
				/>

				<button
					type="submit"
					class="ca-btn ca-btn-primary"
					disabled={submitting}
					data-testid="contact-submit"
				>
					<i class="fas fa-paper-plane" aria-hidden="true"></i>
					{t('portal.contact.submit')}
				</button>
			</form>
		</div>
	</section>
{/if}
