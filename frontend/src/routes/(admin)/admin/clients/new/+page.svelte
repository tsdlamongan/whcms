<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import { untrack } from 'svelte';
	import type { PageProps } from './$types';

	let { form }: PageProps = $props();

	const initial = untrack(() => form?.values ?? ({} as Record<string, string>));
	const fields = $derived<Record<string, string>>(form?.fields ?? {});

	const ERROR_MESSAGES: Record<string, string> = {
		'admincore.clients.fillRequired': 'Fill in all required fields.',
		'admincore.clients.createFailed': 'Failed to create client.'
	};
	const errorText = $derived(
		form?.errorKey
			? (ERROR_MESSAGES[form.errorKey] ?? 'Something went wrong.')
			: (form?.errorMessage ?? null)
	);

	let submitting = $state(false);
</script>

{#snippet field(label: string, key: string, type = 'text', required = false)}
	<div class="hp-formrow">
		<label for={`field-${key}`}
			>{label}{#if required}<span style="color:#d9534f"> *</span>{/if}</label
		>
		<div class="hp-field">
			<input
				id={`field-${key}`}
				name={key}
				{type}
				value={initial[key] ?? (key === 'country' ? 'ID' : '')}
				{required}
				autocomplete={key === 'password' ? 'new-password' : 'off'}
				class="hp-input"
				class:hp-input-error={!!fields[key]}
				data-testid={`client-field-${key}`}
			/>
			{#if fields[key]}
				<div style="color:#d9534f;font-size:12px;margin-top:3px">{fields[key]}</div>
			{/if}
		</div>
	</div>
{/snippet}

<svelte:head>
	<title>Create New Client — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Create New Client</h1>

{#if errorText}
	<div class="hp-alert-red" data-testid="client-create-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{errorText}
	</div>
{/if}

<form
	method="POST"
	data-testid="client-create-form"
	use:enhance={() => {
		submitting = true;
		return async ({ update }) => {
			submitting = false;
			await update({ reset: false });
		};
	}}
>
	<div class="hp-panel" style="padding:16px 22px 6px;max-width:920px">
		<h2
			style="font-size:14px;font-weight:700;color:#555;margin:0 0 4px;text-transform:uppercase;letter-spacing:.03em"
		>
			Account
		</h2>
		{@render field('Email Address', 'email', 'email', true)}
		{@render field('Password', 'password', 'password', true)}

		<h2
			style="font-size:14px;font-weight:700;color:#555;margin:18px 0 4px;text-transform:uppercase;letter-spacing:.03em"
		>
			Profile
		</h2>
		{@render field('First Name', 'first_name', 'text', true)}
		{@render field('Last Name', 'last_name', 'text', true)}
		{@render field('Company Name (optional)', 'company')}
		{@render field('Phone', 'phone')}
		{@render field('Address 1', 'address1')}
		{@render field('Address 2 (optional)', 'address2')}
		{@render field('City', 'city')}
		{@render field('State/Province', 'state')}
		{@render field('Postcode', 'postcode')}
		{@render field('Country (ISO code)', 'country')}
	</div>

	<div class="hp-form-actions">
		<button
			type="submit"
			class="hp-btn hp-btn-primary"
			disabled={submitting}
			data-testid="client-create-submit"
		>
			<i class="fas fa-user-plus"></i>Create Client
		</button>
		<a href="/admin/clients" class="hp-btn">Cancel</a>
	</div>
</form>
