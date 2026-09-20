<script lang="ts">
	import { appName } from '$lib/appName';
	import ServerForm from '../ServerForm.svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	const ERROR_MESSAGES: Record<string, string> = {
		'adminops.servers.fillRequired': 'Fill in all required fields.'
	};
	const errorText = $derived(
		form?.errorKey
			? (ERROR_MESSAGES[form.errorKey] ?? 'Something went wrong.')
			: (form?.errorMessage ?? null)
	);
</script>

<svelte:head>
	<title>Add Server — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Add Server</h1>
<p class="hp-lead">Manage provisioning servers for automatic account creation.</p>

{#if errorText}
	<div class="hp-alert-red" data-testid="server-form-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{errorText}
	</div>
{/if}

<section class="hp-panel" style="max-width:780px">
	<div class="hp-panel-hd"><span class="title">New Server</span></div>
	<div style="padding:14px 12px">
		<ServerForm
			action="?/save"
			testAction="?/test"
			groups={data.groups}
			successKey="adminops.servers.saveSuccess"
		/>
	</div>
</section>
