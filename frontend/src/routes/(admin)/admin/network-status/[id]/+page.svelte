<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import HpModal from '$lib/components/hp/HpModal.svelte';
	import { t } from '$lib/i18n';
	import { toast } from '$lib/stores/toast.svelte';
	import NetworkForm from '../NetworkForm.svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	const saveError = $derived(
		form?.op === 'save' && !form?.success
			? form?.errorKey
				? t(form.errorKey)
				: (form?.errorMessage ?? null)
			: null
	);
	const deleteError = $derived(
		form?.op === 'delete' && !form?.success ? (form?.errorMessage ?? null) : null
	);

	let confirmDeleteOpen = $state(false);
	let deleting = $state(false);
	let deleteFormEl = $state<HTMLFormElement | null>(null);

	const title = $derived(data.issue?.title ?? 'Edit Network Status Entry');
</script>

<svelte:head>
	<title>{title} — {appName} Admin</title>
</svelte:head>

{#if data.loadError || !data.issue}
	<div class="hp-alert-red" data-testid="network-load-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>
		{data.notFound
			? 'Network status entry not found.'
			: (data.loadError ?? 'Something went wrong.')}
	</div>
	<a href="/admin/network-status" class="hp-help" data-testid="network-back-link"
		>← Back to network status</a
	>
{:else}
	<div
		style="display:flex;flex-wrap:wrap;align-items:center;justify-content:space-between;gap:12px;margin-bottom:14px"
	>
		<div>
			<h1 class="hp-h1" style="margin-bottom:2px" data-testid="network-title">
				{data.issue.title}
			</h1>
			<p style="margin:0;color:#888;font-size:13px">Edit network status entry</p>
		</div>
		<span data-testid="network-delete-button">
			<button type="button" class="hp-btn hp-btn-danger" onclick={() => (confirmDeleteOpen = true)}>
				<i class="fas fa-trash-alt"></i>Delete
			</button>
		</span>
	</div>

	{#if data.created}
		<div class="hp-alert-green" data-testid="network-created-alert">
			<i class="fas fa-check-circle" style="margin-right:8px"></i>Network status entry created
			successfully.
		</div>
	{/if}

	{#if deleteError}
		<div class="hp-alert-red" data-testid="network-delete-error">
			<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{deleteError}
		</div>
	{/if}

	<NetworkForm
		issue={data.issue}
		action="?/save"
		errorText={saveError}
		submitLabel="Save Changes"
		onSuccess={() => toast.success(t('adminPortal.network.saved'))}
	/>

	<!-- danger zone: delete -->
	<form
		method="POST"
		action="?/delete"
		class="hidden"
		bind:this={deleteFormEl}
		use:enhance={() => {
			deleting = true;
			return async ({ result, update }) => {
				deleting = false;
				confirmDeleteOpen = false;
				if (result.type === 'redirect') toast.success(t('adminPortal.network.deleted'));
				await update();
			};
		}}
	></form>

	<HpModal
		open={confirmDeleteOpen}
		title="Delete Entry"
		onClose={() => (confirmDeleteOpen = false)}
	>
		<p style="color:#555;margin-bottom:16px">
			Delete network status entry "{data.issue.title}"? This cannot be undone.
		</p>
		<div style="display:flex;justify-content:flex-end;gap:8px">
			<button
				type="button"
				class="hp-btn"
				onclick={() => (confirmDeleteOpen = false)}
				disabled={deleting}
			>
				Cancel
			</button>
			<button
				type="button"
				class="hp-btn hp-btn-danger"
				disabled={deleting}
				onclick={() => deleteFormEl?.requestSubmit()}
			>
				{deleting ? 'Deleting…' : 'Delete'}
			</button>
		</div>
	</HpModal>
{/if}

<style>
	.hidden {
		display: none;
	}
	.hp-alert-green {
		background: #eaf6ea;
		border: 1px solid #c3e6c3;
		border-radius: 4px;
		padding: 12px 14px;
		margin-bottom: 14px;
		color: #2f7a2f;
	}
</style>
