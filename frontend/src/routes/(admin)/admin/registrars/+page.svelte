<script lang="ts">
	import { appName } from '$lib/appName';
	import RegistrarCard from './RegistrarCard.svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	const errorText = $derived(
		form?.action === 'save'
			? form?.errorKey
				? 'Invalid config JSON'
				: (form?.errorMessage ?? null)
			: null
	);
</script>

<svelte:head>
	<title>Domain Registrars — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Domain Registrars</h1>

{#if errorText}
	<div class="hp-alert-red" data-testid="registrar-save-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{errorText}
	</div>
{/if}
{#if data.listError}
	<div class="hp-alert-red" data-testid="registrar-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

<div
	style="background:#1A4D80;color:#fff;text-align:center;padding:9px;font-weight:700;border-radius:3px 3px 0 0"
>
	Module
</div>

{#if data.registrars.length === 0}
	<div class="hp-panel" style="border-radius:0 0 3px 3px;padding:28px;text-align:center;color:#999">
		No registrar modules available.
	</div>
{:else}
	<div>
		{#each data.registrars as registrar (registrar.id)}
			<RegistrarCard {registrar} result={form} />
		{/each}
	</div>
{/if}
