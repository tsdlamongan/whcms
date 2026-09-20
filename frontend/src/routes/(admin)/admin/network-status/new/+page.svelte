<script lang="ts">
	import { appName } from '$lib/appName';
	import { t } from '$lib/i18n';
	import NetworkForm from '../NetworkForm.svelte';
	import type { PageProps } from './$types';

	let { form }: PageProps = $props();

	const errorText = $derived(form?.errorKey ? t(form.errorKey) : (form?.errorMessage ?? null));
</script>

<svelte:head>
	<title>New Network Status Entry — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Add Network Status Entry</h1>
<p class="hp-lead">Announce scheduled maintenance, an ongoing issue or an outage.</p>

<NetworkForm {errorText} submitLabel="Create Entry" />
