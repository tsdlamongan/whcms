<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import { toast } from '$lib/stores/toast.svelte';
	import type { SubmitFunction } from '@sveltejs/kit';
	import type { PageProps } from './$types';
	import ServerForm from '../ServerForm.svelte';

	let { data, form }: PageProps = $props();

	const server = $derived(data.server);

	const moduleLabels: Record<string, string> = { cpanel: 'cPanel', directadmin: 'DirectAdmin' };

	let deleteOpen = $state(false);
	let deleteLoading = $state(false);
	let deleteForm = $state<HTMLFormElement>();

	// Save-form validation error (the Test result is rendered inside ServerForm).
	const errorText = $derived(
		form?.action !== 'test'
			? form?.errorKey === 'adminops.servers.fillRequired'
				? 'Fill in all required fields.'
				: (form?.errorMessage ?? null)
			: null
	);

	const deleteHandler: SubmitFunction = () => {
		deleteLoading = true;
		return async ({ result, update }) => {
			deleteLoading = false;
			if (result.type === 'redirect') {
				deleteOpen = false;
				toast.success('Server deleted successfully.');
			} else if (result.type === 'failure') {
				deleteOpen = false;
				const d = result.data as { errorMessage?: string } | undefined;
				toast.error(d?.errorMessage ?? 'Failed to delete server.');
			}
			await update();
		};
	};
</script>

<svelte:head>
	<title>{server.name} — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">
	{server.name}
	<span class="hp-stext" style="color:#5b9bd5;font-size:14px;margin-left:10px">
		{moduleLabels[server.module] ?? server.module}
	</span>
	{#if server.active}
		<span class="hp-badge active" style="margin-left:8px;vertical-align:middle">Active</span>
	{:else}
		<span class="hp-badge inactive" style="margin-left:8px;vertical-align:middle">Off</span>
	{/if}
</h1>
<p class="hp-lead">
	{server.hostname}:{server.port} — Capacity: {server.accounts_count ?? '—'} /
	{server.max_accounts > 0 ? server.max_accounts : '∞'}
</p>

<div style="display:flex;gap:8px;margin-bottom:16px;flex-wrap:wrap">
	<a class="hp-btn" href="/admin/servers"><i class="fas fa-arrow-left"></i>Back to Servers</a>
	<span style="display:contents" data-testid="server-delete-button">
		<button type="button" class="hp-btn hp-btn-danger" onclick={() => (deleteOpen = true)}>
			<i class="fas fa-trash"></i>Delete Server
		</button>
	</span>
</div>

{#if errorText}
	<div class="hp-alert-red" data-testid="server-form-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{errorText}
	</div>
{/if}

<section class="hp-panel" style="max-width:780px">
	<div class="hp-panel-hd"><span class="title">Edit Server</span></div>
	<div style="padding:14px 12px">
		<!--
			Keyed on the server id so navigating between server detail pages
			(a reused route component) remounts the form with the right initial
			values instead of showing the previous server's — and never blanks.
		-->
		{#key server.id}
			<ServerForm
				action="?/save"
				testAction="?/test"
				groups={data.groups}
				initial={server}
				successKey="adminops.servers.saveSuccess"
				submitLabel="Save Changes"
			/>
		{/key}
	</div>
</section>

<form
	method="POST"
	action="?/delete"
	style="display:none"
	bind:this={deleteForm}
	use:enhance={deleteHandler}
></form>

<ConfirmDialog
	bind:open={deleteOpen}
	title="Delete Server"
	message="Delete this server? This cannot be undone."
	danger
	loading={deleteLoading}
	onConfirm={() => deleteForm?.requestSubmit()}
/>
