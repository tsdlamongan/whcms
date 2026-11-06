<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import HpModal from '$lib/components/hp/HpModal.svelte';
	import { t } from '$lib/i18n';
	import { toast } from '$lib/stores/toast.svelte';
	import ProductForm from '../ProductForm.svelte';
	import OptionsEditor from './OptionsEditor.svelte';
	import SpecsEditor from './SpecsEditor.svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	function scopedError(scope: string): string | null {
		if (form?.scope !== scope) return null;
		return form?.errorKey ? t(form.errorKey) : (form?.errorMessage ?? null);
	}

	const productError = $derived(scopedError('product'));
	const optionsActionError = $derived(scopedError('options'));
	const deleteError = $derived(scopedError('delete'));
	const specsActionError = $derived(scopedError('specs'));
	const productErrorKey = $derived(form?.scope === 'product' ? form?.errorKey : null);
	const specsFieldErrors = $derived<Record<string, string>>(
		form?.scope === 'specs' ? (form?.fields ?? {}) : {}
	);
	const specsFormValues = $derived(form?.scope === 'specs' ? (form?.values ?? null) : null);

	let confirmDeleteOpen = $state(false);
	let deleting = $state(false);
	let deleteFormEl = $state<HTMLFormElement | null>(null);
	let duplicating = $state(false);

	const title = $derived(data.product?.name ?? 'Edit Product');
</script>

<svelte:head>
	<title>{title} — {appName} Admin</title>
</svelte:head>

{#if data.loadError || !data.product}
	<div class="hp-alert-red" data-testid="product-load-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>
		{data.notFound ? 'Product not found.' : (data.loadError ?? 'Something went wrong.')}
	</div>
	<a href="/admin/products" class="hp-help" data-testid="product-back-link"
		>← Back to product list</a
	>
{:else}
	<div
		style="display:flex;flex-wrap:wrap;align-items:center;justify-content:space-between;gap:12px;margin-bottom:14px"
	>
		<div>
			<h1 class="hp-h1" style="margin-bottom:2px" data-testid="product-title">
				{data.product.name}
			</h1>
			<p style="margin:0;color:#888;font-size:13px">
				Edit product ·
				<code style="background:#f2f2f2;border-radius:3px;padding:1px 5px">{data.product.slug}</code
				>
			</p>
		</div>
		<div style="display:flex;gap:8px">
			<form
				method="POST"
				action="?/duplicate"
				use:enhance={() => {
					duplicating = true;
					return async ({ update }) => {
						duplicating = false;
						await update();
					};
				}}
			>
				<button
					type="submit"
					class="hp-btn"
					disabled={duplicating}
					data-testid="product-duplicate-button"
				>
					<i class="fas fa-copy" style="color:#5b9bd5"></i>{duplicating
						? 'Duplicating…'
						: 'Duplicate Product'}
				</button>
			</form>
			<span data-testid="product-delete-button">
				<button
					type="button"
					class="hp-btn hp-btn-danger"
					onclick={() => (confirmDeleteOpen = true)}
				>
					<i class="fas fa-trash-alt"></i>Delete Product
				</button>
			</span>
		</div>
	</div>

	{#if data.created}
		<div class="hp-alert-green" data-testid="product-created-alert">
			<i class="fas fa-check-circle" style="margin-right:8px"></i>Product saved successfully.
		</div>
	{/if}

	{#if data.duplicated}
		<div class="hp-alert-green" data-testid="product-duplicated-alert">
			<i class="fas fa-check-circle" style="margin-right:8px"></i>Product duplicated as "{data
				.product.name}". It's hidden — review the name, slug and pricing, then unhide it when ready.
		</div>
	{/if}

	{#if data.pricingError}
		<div class="hp-alert-yellow" data-testid="product-pricing-error">
			<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>Product saved, but pricing
			failed to save. Please review the Pricing tab.
		</div>
	{/if}

	{#if deleteError}
		<div class="hp-alert-red" data-testid="product-delete-error">
			<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{deleteError}
		</div>
	{/if}

	<!--
		Key on the product id so a client-side navigation between two product
		edit pages ([id] → [id], which SvelteKit serves by reusing this page
		component) rebuilds ProductForm from the new product. Without the key,
		ProductForm keeps the state it initialized once (untrack) from the first
		product and the form shows stale/empty inputs until a full page refresh.
		The id is stable across a save (?/save re-runs load with the same id),
		so in-progress edits are preserved on save as before.
	-->
	{#key data.product.id}
		<ProductForm
			product={data.product}
			pricing={data.pricing}
			groups={data.groups}
			serverGroups={data.serverGroups}
			templateKeys={data.templateKeys}
			action="?/save"
			errorText={productError}
			errorKey={productErrorKey}
			submitLabel="Save Changes"
			onSuccess={() => toast.success('Product saved successfully.')}
		>
			{#snippet optionsTab()}
				<OptionsEditor
					optionGroups={data.optionGroups}
					error={data.optionsError}
					actionError={optionsActionError}
				/>
			{/snippet}
		</ProductForm>
	{/key}

	<div style="margin-top:16px">
		<SpecsEditor
			specs={data.specs}
			configurable={data.product.configurable}
			error={data.specsError}
			actionError={specsActionError}
			fieldErrors={specsFieldErrors}
			formValues={specsFormValues}
		/>
	</div>

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
				if (result.type === 'redirect') toast.success('Product deleted successfully.');
				await update();
			};
		}}
	></form>

	<HpModal
		open={confirmDeleteOpen}
		title="Delete Product"
		onClose={() => (confirmDeleteOpen = false)}
	>
		<p style="color:#555;margin-bottom:16px">
			Delete product "{data.product.name}"? It will be hidden from the catalog (soft delete).
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
