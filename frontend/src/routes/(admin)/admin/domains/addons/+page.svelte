<script lang="ts">
	import { appName } from '$lib/appName';
	import DomainAddonRow from './DomainAddonRow.svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();
</script>

<svelte:head>
	<title>Domain Addons — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Domain Addons</h1>

<p class="hp-lead">
	Fixed catalog of add-ons clients can attach to a domain (ID Protection, DNS Management, Email
	Forwarding). Only price and availability are configurable.
</p>

{#if data.listError}
	<div class="hp-alert-yellow" data-testid="domain-addon-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

{#if data.addons.length === 0}
	<div class="hp-panel" style="padding:28px;text-align:center;color:#999">
		No domain addons found.
	</div>
{:else}
	<div class="hp-panel" style="padding:0">
		{#each data.addons as addon (addon.id)}
			<DomainAddonRow {addon} result={form} />
		{/each}
	</div>
{/if}
