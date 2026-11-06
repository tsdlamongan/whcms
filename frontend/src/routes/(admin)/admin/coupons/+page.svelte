<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import FormField from '$lib/components/FormField.svelte';
	import HpPager from '$lib/components/hp/HpPager.svelte';
	import LoadingButton from '$lib/components/LoadingButton.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import MoneyText from '$lib/components/MoneyText.svelte';
	import type { SelectOption } from '$lib/components/types';
	import { fmtDate } from '$lib/date';
	import { toast } from '$lib/stores/toast.svelte';
	import type { PageProps } from './$types';

	let { data, form }: PageProps = $props();

	type CouponRow = (typeof data.coupons)[number];

	const ERROR_TEXT: Record<string, string> = {
		'adcatalog.coupons.fillRequired': 'Code and value are required.',
		'adcatalog.coupons.invalidValue': 'Value must be greater than 0.',
		'adcatalog.coupons.invalidPercentage': 'Percentage must be between 1 and 100.',
		'adcatalog.coupons.saveFailed': 'Failed to save the coupon.',
		'adcatalog.coupons.deleteFailed': 'Failed to delete the coupon.'
	};
	function errText(key?: string): string | undefined {
		if (!key) return undefined;
		return ERROR_TEXT[key] ?? key;
	}

	const total = $derived(data.meta?.total ?? data.coupons.length);
	const curPage = $derived(data.meta?.page ?? data.page);
	const perPage = $derived(data.meta?.per_page ?? data.perPage);
	const from = $derived(total === 0 ? 0 : (curPage - 1) * perPage + 1);
	const to = $derived(Math.min(curPage * perPage, total));

	// create / edit modal
	let modalOpen = $state(false);
	let editing = $state<CouponRow | null>(null);
	let saving = $state(false);

	let fCode = $state('');
	let fType = $state('percentage');
	let fValue = $state(0);
	let fAppliesTo = $state<string[]>([]);
	let fMaxUses = $state(0);
	let fRecurring = $state(false);
	let fExpiresAt = $state('');
	let fActive = $state(true);

	const typeOptions: SelectOption[] = [
		{ value: 'percentage', label: 'Percentage (%)' },
		{ value: 'fixed', label: 'Fixed amount (Rp)' }
	];

	function openCreate() {
		editing = null;
		fCode = '';
		fType = 'percentage';
		fValue = 0;
		fAppliesTo = [];
		fMaxUses = 0;
		fRecurring = false;
		fExpiresAt = '';
		fActive = true;
		modalOpen = true;
	}

	function openEdit(row: CouponRow) {
		editing = row;
		fCode = row.code;
		fType = row.type;
		fValue = row.value;
		fAppliesTo = (row.applies_to ?? []).map(String);
		fMaxUses = row.max_uses;
		fRecurring = row.recurring;
		fExpiresAt = row.expires_at ? row.expires_at.slice(0, 10) : '';
		fActive = row.active;
		modalOpen = true;
	}

	// delete confirm
	let confirmOpen = $state(false);
	let deleting = $state(false);
	let deleteTarget = $state<CouponRow | null>(null);
	let deleteFormEl = $state<HTMLFormElement | null>(null);

	function askDelete(row: CouponRow) {
		deleteTarget = row;
		confirmOpen = true;
	}

	const modalError = $derived(
		modalOpen && form?.op === 'save' && !form?.success
			? form?.errorKey
				? errText(form.errorKey)
				: (form?.errorMessage ?? null)
			: null
	);

	function appliesToLabel(row: CouponRow): string {
		const n = row.applies_to?.length ?? 0;
		return n === 0 ? 'All products' : `${n} product${n === 1 ? '' : 's'}`;
	}
</script>

<svelte:head>
	<title>Coupons — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Coupons</h1>

<p class="hp-lead">Manage discount codes that clients can apply to their orders.</p>

{#if data.listError}
	<div class="hp-alert-yellow" data-testid="coupon-list-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

{#if form?.op === 'delete' && !form?.success && (form?.errorMessage || form?.errorKey)}
	<div class="hp-alert-red" data-testid="coupon-delete-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{form.errorKey
			? errText(form.errorKey)
			: form.errorMessage}
	</div>
{/if}

<form method="get" class="hp-filter" data-testid="coupon-filter-form">
	<div class="grow">
		<div class="hp-field-label">Search</div>
		<input
			class="hp-input"
			type="search"
			name="search"
			value={data.search}
			placeholder="Search coupon code…"
			data-testid="coupon-search-input"
		/>
	</div>
	<button class="hp-btn hp-btn-primary" type="submit" data-testid="coupon-filter-submit">
		<i class="fas fa-search"></i>Search
	</button>
</form>

<div class="hp-listbar">
	<div class="hp-count">{total} Records Found, Showing {from} to {to}</div>
	<span data-testid="coupon-create-button">
		<button type="button" class="hp-btn hp-btn-primary" onclick={openCreate}>
			<i class="fas fa-plus"></i>New Coupon
		</button>
	</span>
</div>

<div class="hp-scroll">
	<table class="hp-table">
		<thead>
			<tr>
				<th>Code</th>
				<th>Type</th>
				<th class="r">Value</th>
				<th>Applies To</th>
				<th class="c">Usage</th>
				<th class="c">Recurring</th>
				<th>Expires</th>
				<th class="c" style="width:90px">Status</th>
				<th style="width:160px"></th>
			</tr>
		</thead>
		<tbody>
			{#each data.coupons as row (row.id)}
				<tr class="hp-row">
					<td>
						<button
							type="button"
							class="hp-rowbtn"
							data-testid={`row-coupon-${row.id}`}
							onclick={() => openEdit(row)}
						>
							{row.code}
						</button>
					</td>
					<td style="color:#555"
						>{row.type === 'percentage' ? 'Percentage (%)' : 'Fixed amount (Rp)'}</td
					>
					<td class="r">
						{#if row.type === 'percentage'}
							<span style="font-variant-numeric:tabular-nums">{row.value}%</span>
						{:else}
							<span style="font-variant-numeric:tabular-nums"><MoneyText amount={row.value} /></span
							>
						{/if}
					</td>
					<td style="color:#555">{appliesToLabel(row)}</td>
					<td class="c" style="font-variant-numeric:tabular-nums;color:#666">
						{row.used_count}/{row.max_uses > 0 ? row.max_uses : '∞'}
					</td>
					<td class="c">{row.recurring ? 'Yes' : 'No'}</td>
					<td style="color:#666">
						{#if row.expires_at}
							{fmtDate(row.expires_at)}
						{:else}
							<span style="color:#999">Never</span>
						{/if}
					</td>
					<td class="c"
						><span class={`hp-badge ${row.active ? 'active' : 'inactive'}`}
							>{row.active ? 'Active' : 'Inactive'}</span
						></td
					>
					<td style="white-space:nowrap">
						<button
							type="button"
							class="hp-btn"
							style="padding:5px 10px"
							data-testid={`coupon-edit-${row.id}`}
							onclick={() => openEdit(row)}
						>
							Edit
						</button>
						<button
							type="button"
							class="hp-btn hp-btn-danger"
							style="padding:5px 10px"
							data-testid={`coupon-delete-${row.id}`}
							onclick={() => askDelete(row)}
						>
							Delete
						</button>
					</td>
				</tr>
			{:else}
				<tr
					><td colspan="9" style="text-align:center;padding:28px;color:#999">No coupons found</td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

<HpPager page={curPage} {perPage} {total} />

<!-- create / edit modal -->
<Modal bind:open={modalOpen} title={editing ? 'Edit Coupon' : 'Create Coupon'} size="lg">
	<form
		method="POST"
		action={editing ? '?/update' : '?/create'}
		data-testid="coupon-form"
		use:enhance={() => {
			saving = true;
			return async ({ result, update }) => {
				saving = false;
				if (result.type === 'success') {
					modalOpen = false;
					toast.success('Coupon saved successfully.');
				}
				await update();
			};
		}}
	>
		{#if editing}
			<input type="hidden" name="id" value={editing.id} />
		{/if}

		{#if modalError}
			<div class="hp-alert-red" style="margin-bottom:14px" data-testid="coupon-form-error">
				{modalError}
			</div>
		{/if}

		<div class="grid gap-x-4 sm:grid-cols-2">
			<div data-testid="coupon-code-field">
				<FormField label="Code" name="code" bind:value={fCode} required />
			</div>
			<div data-testid="coupon-type-field">
				<FormField
					label="Type"
					name="type"
					type="select"
					bind:value={fType}
					options={typeOptions}
				/>
			</div>
			<div data-testid="coupon-value-field">
				<FormField
					label="Value"
					name="value"
					type="number"
					bind:value={fValue}
					hint={fType === 'percentage'
						? 'Discount percentage 1–100.'
						: 'Discount amount in Rupiah (no decimals).'}
					required
				/>
			</div>
			<div data-testid="coupon-max-uses-field">
				<FormField
					label="Max uses"
					name="max_uses"
					type="number"
					bind:value={fMaxUses}
					hint="0 = unlimited."
				/>
			</div>
		</div>

		<div class="mb-4">
			<label class="mb-1 block text-sm font-medium text-gray-700" for="coupon-applies-to">
				Applies to products
			</label>
			<select
				id="coupon-applies-to"
				name="applies_to"
				multiple
				size="5"
				bind:value={fAppliesTo}
				data-testid="coupon-applies-to-select"
				class="block w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm shadow-sm focus:border-primary focus:outline-2 focus:outline-primary/40"
			>
				{#each data.products as p (p.id)}
					<option value={String(p.id)}>{p.name}</option>
				{/each}
			</select>
			<p class="mt-1 text-xs text-gray-500">
				Leave empty to apply to all products. Hold Ctrl/Cmd to select multiple.
			</p>
		</div>

		<div class="grid gap-x-4 sm:grid-cols-2">
			<div data-testid="coupon-expires-field">
				<FormField
					label="Expires"
					name="expires_at"
					type="date"
					bind:value={fExpiresAt}
					hint="Leave empty if the coupon never expires."
				/>
			</div>
			<div>
				<div data-testid="coupon-recurring-field">
					<FormField
						label="Also applies to renewals (recurring)"
						name="recurring"
						type="checkbox"
						bind:value={fRecurring}
					/>
				</div>
				<div data-testid="coupon-active-field">
					<FormField label="Active" name="active" type="checkbox" bind:value={fActive} />
				</div>
			</div>
		</div>

		<div class="mt-5 flex justify-end gap-2">
			<LoadingButton variant="secondary" onclick={() => (modalOpen = false)} disabled={saving}>
				Cancel
			</LoadingButton>
			<span data-testid="coupon-form-submit">
				<LoadingButton type="submit" loading={saving}>Save</LoadingButton>
			</span>
		</div>
	</form>
</Modal>

<!-- delete confirm -->
<form
	method="POST"
	action="?/delete"
	class="hidden"
	bind:this={deleteFormEl}
	use:enhance={() => {
		deleting = true;
		return async ({ result, update }) => {
			deleting = false;
			confirmOpen = false;
			if (result.type === 'success') toast.success('Coupon deleted successfully.');
			await update();
		};
	}}
>
	<input type="hidden" name="id" value={deleteTarget?.id ?? ''} />
</form>

<ConfirmDialog
	bind:open={confirmOpen}
	danger
	loading={deleting}
	title="Delete"
	message={`Delete coupon "${deleteTarget?.code ?? ''}"? It can no longer be used.`}
	confirmLabel="Delete"
	onConfirm={() => deleteFormEl?.requestSubmit()}
/>

<style>
	.hp-rowbtn {
		background: none;
		border: none;
		padding: 0;
		font: inherit;
		font-weight: 600;
		color: var(--hp-link, #337ab7);
		cursor: pointer;
	}
	.hp-rowbtn:hover {
		text-decoration: underline;
	}
	.hidden {
		display: none;
	}
</style>
