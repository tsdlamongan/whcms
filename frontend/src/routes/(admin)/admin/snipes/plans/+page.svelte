<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import HpModal from '$lib/components/hp/HpModal.svelte';
	import { formatIDR } from '$lib/money';
	import { toast } from '$lib/stores/toast.svelte';
	import type { SubmitFunction } from '@sveltejs/kit';
	import type { PageProps } from './$types';
	import type { SnipePlan } from './+page.server';

	let { data }: PageProps = $props();

	let open = $state(false);
	let saving = $state(false);
	let formError = $state('');

	// The modal's fields; id 0 = a new plan.
	let id = $state(0);
	let name = $state('');
	let windowDays = $state(30);
	let fee = $state(0);
	let successFee = $state(0);
	let sort = $state(0);
	let active = $state(true);

	function openNew() {
		id = 0;
		name = '';
		windowDays = 30;
		fee = 0;
		successFee = 0;
		sort = data.plans.length;
		active = true;
		formError = '';
		open = true;
	}

	function openEdit(p: SnipePlan) {
		id = p.id;
		name = p.name;
		windowDays = p.window_days;
		fee = p.fee;
		successFee = p.success_fee;
		sort = p.sort;
		active = p.active;
		formError = '';
		open = true;
	}

	const saveHandler: SubmitFunction = () => {
		saving = true;
		formError = '';
		return async ({ result, update }) => {
			saving = false;
			if (result.type === 'success') {
				open = false;
				toast.success('Plan saved');
			} else if (result.type === 'failure') {
				formError =
					(result.data as { errorMessage?: string })?.errorMessage ?? 'Could not save the plan';
			}
			await update({ reset: false });
		};
	};

	const toggleHandler: SubmitFunction = () => {
		return async ({ result, update }) => {
			if (result.type === 'failure') {
				toast.error(
					(result.data as { errorMessage?: string })?.errorMessage ?? 'Could not update the plan'
				);
			}
			await update({ reset: false });
		};
	};
</script>

<svelte:head>
	<title>Domain Snipe Plans — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Domain Snipe Plans</h1>

<div class="hp-info">
	<i class="fas fa-info-circle" style="margin-right:8px"></i>The packages clients can buy for a
	hunt. The <strong>service fee</strong> is kept whatever happens; the registration price of the
	domain (plus any <strong>success fee</strong>) is paid up front and credited back to the client's
	account credit when the hunt ends without winning the domain. Editing a plan never changes snipes
	that were already sold. Hunt timing and limits live under
	<a href="/admin/snipes" style="color:#337AB7;font-weight:600">Domain Snipes</a>.
</div>

{#if data.listError}
	<div class="hp-alert-yellow" data-testid="snipe-plans-error">
		<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
	</div>
{/if}

<div style="margin-bottom:10px">
	<button class="hp-btn hp-btn-primary" onclick={openNew} data-testid="snipe-plan-add">
		<i class="fas fa-plus"></i>Add Plan
	</button>
</div>

<div class="hp-scroll">
	<table class="hp-table" data-testid="snipe-plans-table">
		<thead>
			<tr>
				<th style="width:60px">ID</th>
				<th>Name</th>
				<th class="c" style="width:110px">Window</th>
				<th class="r" style="width:130px">Service Fee</th>
				<th class="r" style="width:130px">Success Fee</th>
				<th class="c" style="width:70px">Sort</th>
				<th class="c" style="width:100px">Status</th>
				<th class="c" style="width:200px">Actions</th>
			</tr>
		</thead>
		<tbody>
			{#each data.plans as p (p.id)}
				<tr class="hp-row" data-testid={`row-snipe-plan-${p.id}`}>
					<td>{p.id}</td>
					<td>{p.name}</td>
					<td class="c">{p.window_days} days</td>
					<td class="r">{p.fee > 0 ? formatIDR(p.fee) : 'Free'}</td>
					<td class="r">{p.success_fee > 0 ? formatIDR(p.success_fee) : '—'}</td>
					<td class="c">{p.sort}</td>
					<td class="c"
						><span class={`hp-badge ${p.active ? 'active' : 'cancelled'}`}
							>{p.active ? 'Active' : 'Inactive'}</span
						></td
					>
					<td class="c" style="white-space:nowrap">
						<button
							class="hp-btn"
							onclick={() => openEdit(p)}
							data-testid={`snipe-plan-edit-${p.id}`}
						>
							<i class="fas fa-pen"></i>Edit
						</button>
						<form
							method="POST"
							action="?/toggle"
							use:enhance={toggleHandler}
							style="display:inline"
						>
							<input type="hidden" name="id" value={p.id} />
							<input type="hidden" name="name" value={p.name} />
							<input type="hidden" name="window_days" value={p.window_days} />
							<input type="hidden" name="fee" value={p.fee} />
							<input type="hidden" name="success_fee" value={p.success_fee} />
							<input type="hidden" name="sort" value={p.sort} />
							<input type="hidden" name="active" value={String(p.active)} />
							<button class="hp-btn" type="submit" data-testid={`snipe-plan-toggle-${p.id}`}>
								{p.active ? 'Deactivate' : 'Activate'}
							</button>
						</form>
					</td>
				</tr>
			{:else}
				<tr
					><td colspan="8" style="text-align:center;padding:28px;color:#999"
						>No plans yet. Clients cannot buy a snipe until you add one.</td
					></tr
				>
			{/each}
		</tbody>
	</table>
</div>

<HpModal {open} title={id ? 'Edit Plan' : 'Add Plan'} onClose={() => (open = false)}>
	<form method="POST" action="?/save" use:enhance={saveHandler} data-testid="snipe-plan-form">
		{#if formError}
			<div class="hp-alert-red" data-testid="snipe-plan-error">
				<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{formError}
			</div>
		{/if}
		<input type="hidden" name="id" value={id} />

		<div class="hp-formrow">
			<label for="plan-name">Name<span style="color:#d9534f"> *</span></label>
			<div class="hp-field">
				<input
					id="plan-name"
					name="name"
					class="hp-input"
					bind:value={name}
					data-testid="snipe-plan-name"
					required
				/>
			</div>
		</div>
		<div class="hp-formrow">
			<label for="plan-days">Hunting window (days)</label>
			<div class="hp-field">
				<input
					id="plan-days"
					name="window_days"
					type="number"
					min="1"
					max="365"
					class="hp-input"
					bind:value={windowDays}
					data-testid="snipe-plan-days"
				/>
				<div class="hp-help" style="margin-top:3px">
					How long the hunt may run, counted from the payment.
				</div>
			</div>
		</div>
		<div class="hp-formrow">
			<label for="plan-fee">Service fee (IDR)</label>
			<div class="hp-field">
				<input
					id="plan-fee"
					name="fee"
					type="number"
					min="0"
					step="1"
					class="hp-input"
					bind:value={fee}
					data-testid="snipe-plan-fee"
				/>
				<div class="hp-help" style="margin-top:3px">
					Not refundable. 0 makes the plan free (the domain price is still prepaid).
				</div>
			</div>
		</div>
		<div class="hp-formrow">
			<label for="plan-success">Success fee (IDR)</label>
			<div class="hp-field">
				<input
					id="plan-success"
					name="success_fee"
					type="number"
					min="0"
					step="1"
					class="hp-input"
					bind:value={successFee}
					data-testid="snipe-plan-success-fee"
				/>
				<div class="hp-help" style="margin-top:3px">
					Prepaid, kept only when the domain is won; credited back otherwise.
				</div>
			</div>
		</div>
		<div class="hp-formrow">
			<label for="plan-sort">Sort order</label>
			<div class="hp-field">
				<input
					id="plan-sort"
					name="sort"
					type="number"
					min="0"
					step="1"
					class="hp-input"
					bind:value={sort}
					data-testid="snipe-plan-sort"
				/>
			</div>
		</div>
		<div class="hp-formrow">
			<label for="plan-active">&nbsp;</label>
			<div class="hp-field" style="flex:1 1 auto">
				<label class="hp-checkline" style="padding:0">
					<input
						id="plan-active"
						name="active"
						type="checkbox"
						bind:checked={active}
						data-testid="snipe-plan-active"
					/>
					Active (clients can buy it)
				</label>
			</div>
		</div>

		<div style="display:flex;justify-content:flex-end;gap:8px;margin-top:10px">
			<button type="button" class="hp-btn" disabled={saving} onclick={() => (open = false)}
				>Cancel</button
			>
			<button
				type="submit"
				class="hp-btn hp-btn-primary"
				disabled={saving}
				aria-busy={saving}
				data-testid="snipe-plan-submit"
			>
				{#if saving}<i class="fas fa-spinner fa-spin"></i>{/if}Save Plan
			</button>
		</div>
	</form>
</HpModal>
