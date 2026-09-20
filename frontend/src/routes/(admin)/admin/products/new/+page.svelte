<script lang="ts">
	import { appName } from '$lib/appName';
	import { t } from '$lib/i18n';
	import ProductForm from '../ProductForm.svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	const errorText = $derived(form?.errorKey ? t(form.errorKey) : (form?.errorMessage ?? null));
</script>

<svelte:head>
	<title>Create New Product — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Create New Product</h1>
<p class="hp-lead">Manage the hosting &amp; services product catalog.</p>

{#if data.groupsError}
	<div class="hp-alert-red" data-testid="product-groups-load-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.groupsError}
	</div>
{/if}

<ProductForm
	groups={data.groups}
	serverGroups={data.serverGroups}
	templateKeys={data.templateKeys}
	{errorText}
	errorKey={form?.errorKey}
	submitLabel="Create Product"
/>
