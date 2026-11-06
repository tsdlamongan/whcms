<script lang="ts">
	import { appName } from '$lib/appName';
	import { enhance } from '$app/forms';
	import { invalidateAll } from '$app/navigation';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import LoadingButton from '$lib/components/LoadingButton.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import { fmtDate } from '$lib/date';
	import { toast } from '$lib/stores/toast.svelte';
	import type { PageProps } from './$types';
	import type { StaffRow } from './+page.server';
	import { PERMISSION_MODULES, parsePermissions, type PermissionModule } from './permissions';

	let { data, form }: PageProps = $props();

	// Password gate (docs/DESIGN.md §4 / §9). Real "sudo mode": the unlock form
	// posts the password to POST /auth/confirm-password (verified server-side,
	// attempt-limited) and the loader/actions re-check the server-held
	// confirmation window (15 min), so the list stays withheld until it is open.
	let gateBusy = $state(false);
	let gateRefreshed = false;

	// The window can lapse while the page is open; a mutating action then
	// answers 403 + notConfirmed - reload so the gate comes back.
	$effect(() => {
		if (form?.notConfirmed && !gateRefreshed) {
			gateRefreshed = true;
			void invalidateAll();
		} else if (!form?.notConfirmed) {
			gateRefreshed = false;
		}
	});

	function moduleLabel(mod: PermissionModule): string {
		const labels: Record<PermissionModule, string> = {
			clients: 'Clients',
			orders: 'Orders',
			billing: 'Billing',
			payments: 'Transactions & Payments',
			services: 'Services',
			domains: 'Domains',
			support: 'Support',
			products: 'Products',
			servers: 'Servers',
			reports: 'Reports',
			settings: 'Settings',
			logs: 'Logs',
			announcements: 'Announcements',
			knowledgebase: 'Knowledgebase',
			network: 'Network Status'
		};
		return labels[mod];
	}

	const ERROR_TEXT: Record<string, string> = {
		'adminsupport.staff.emailRequired': 'Email is required.',
		'adminsupport.staff.passwordRequired': 'Password is required for new accounts.'
	};
	function errText(key?: string): string | undefined {
		if (!key) return undefined;
		return ERROR_TEXT[key] ?? key;
	}

	function emptyPermissions(): Record<PermissionModule, boolean> {
		const out = {} as Record<PermissionModule, boolean>;
		for (const mod of PERMISSION_MODULES) out[mod] = false;
		return out;
	}

	let modalOpen = $state(false);
	let editing = $state<StaffRow | null>(null);
	let saving = $state(false);

	let email = $state('');
	let password = $state('');
	let active = $state(true);
	let permissions = $state<Record<PermissionModule, boolean>>(emptyPermissions());

	let deactivateOpen = $state(false);
	let deactivateTarget = $state<StaffRow | null>(null);
	let deactivating = $state(false);

	const errorText = $derived(form?.errorMessage ?? null);
	const fieldErrors = $derived<Record<string, string>>(form?.fieldErrors ?? {});

	const activeStaff = $derived(data.staff.filter((s) => s.status === 'active'));
	const inactiveStaff = $derived(data.staff.filter((s) => s.status !== 'active'));

	function openCreate() {
		editing = null;
		email = '';
		password = '';
		active = true;
		permissions = emptyPermissions();
		modalOpen = true;
	}

	function openEdit(row: StaffRow) {
		editing = row;
		email = row.email;
		password = '';
		active = row.status === 'active';
		const parsed = parsePermissions(row.permissions);
		const next = emptyPermissions();
		for (const mod of PERMISSION_MODULES) next[mod] = parsed[mod] === true;
		permissions = next;
		modalOpen = true;
	}

	function askDeactivate(row: StaffRow) {
		deactivateTarget = row;
		deactivateOpen = true;
	}

	function permsSummary(row: StaffRow): string {
		const parsed = parsePermissions(row.permissions);
		const enabled = PERMISSION_MODULES.filter((m) => parsed[m] === true);
		if (enabled.length === 0) return 'None';
		if (enabled.length === PERMISSION_MODULES.length) return 'All modules';
		return `${enabled.length} module${enabled.length === 1 ? '' : 's'}`;
	}
</script>

{#snippet staffTable(rows: StaffRow[], emptyText: string)}
	<div class="hp-scroll">
		<table class="hp-table">
			<thead>
				<tr>
					<th>Email Address</th>
					<th class="c" style="width:90px">Role</th>
					<th class="c" style="width:60px">2FA</th>
					<th style="width:140px">Last Login</th>
					<th style="width:110px">Created</th>
					<th style="width:150px">Permissions</th>
					<th class="c" style="width:140px"></th>
				</tr>
			</thead>
			<tbody>
				{#each rows as row (row.id)}
					<tr class="hp-row">
						<td>
							<button
								type="button"
								class="hp-rowbtn"
								data-testid={`staff-row-${row.id}`}
								onclick={() => openEdit(row)}
							>
								{row.email}
							</button>
						</td>
						<td class="c">
							<span
								class="hp-stext"
								style={row.role === 'admin' ? 'color:#1A4D80' : 'color:#5b9bd5'}
							>
								{row.role === 'admin' ? 'Admin' : 'Staff'}
							</span>
						</td>
						<td class="c">
							{#if row.twofa_enabled}
								<i class="fas fa-check-circle" style="color:#5cb85c" title="2FA enabled"></i>
							{:else}
								<span style="color:#999">—</span>
							{/if}
						</td>
						<td style="color:#666">
							{#if row.last_login_at}
								{fmtDate(row.last_login_at)}
							{:else}
								<span style="color:#999;font-style:italic">Never</span>
							{/if}
						</td>
						<td style="color:#666">{fmtDate(row.created_at)}</td>
						<td style="color:#666">{permsSummary(row)}</td>
						<td class="c" style="white-space:nowrap">
							<button
								type="button"
								class="hp-btn"
								style="padding:5px 10px"
								data-testid={`staff-edit-${row.id}`}
								onclick={() => openEdit(row)}
							>
								Edit
							</button>
							{#if row.status === 'active'}
								<button
									type="button"
									class="hp-btn hp-btn-danger"
									style="padding:5px 10px"
									data-testid={`staff-deactivate-${row.id}`}
									onclick={() => askDeactivate(row)}
								>
									Deactivate
								</button>
							{/if}
						</td>
					</tr>
				{:else}
					<tr><td colspan="7" style="text-align:center;padding:22px;color:#999">{emptyText}</td></tr
					>
				{/each}
			</tbody>
		</table>
	</div>
{/snippet}

<svelte:head>
	<title>Administrators — {appName} Admin</title>
</svelte:head>

{#if !data.confirmed}
	<div class="hp-gate-wrap">
		<form
			method="POST"
			action="?/unlock"
			class="hp-gate"
			data-testid="staff-gate"
			use:enhance={() => {
				gateBusy = true;
				return async ({ update }) => {
					gateBusy = false;
					await update({ reset: true });
				};
			}}
		>
			<div class="hp-gate-icon"><i class="fas fa-lock"></i></div>
			<h2>Confirm password to continue</h2>
			<p>This page manages administrator accounts. Re-enter your password to continue.</p>
			{#if form?.gateError}
				<div class="hp-alert-red" data-testid="staff-gate-error">{form.gateError}</div>
			{/if}
			<input
				type="password"
				name="password"
				class="hp-input"
				class:hp-input-error={!!form?.gateError}
				placeholder="Password"
				autocomplete="current-password"
				required
				data-testid="staff-gate-password"
			/>
			<button
				type="submit"
				class="hp-btn hp-btn-primary"
				disabled={gateBusy}
				data-testid="staff-gate-confirm"
			>
				Confirm
			</button>
		</form>
	</div>
{:else}
	<h1 class="hp-h1">Administrators</h1>

	<p class="hp-lead">
		Manage staff accounts and per-module permissions. Every administrator below can sign in to this
		admin area — assign only the modules each person needs.
	</p>

	{#if data.listError}
		<div class="hp-alert-yellow" data-testid="staff-list-error">
			<i class="fas fa-exclamation-triangle" style="margin-right:8px"></i>{data.listError}
		</div>
	{/if}

	{#if form?.deactivateErrorMessage}
		<div class="hp-alert-red" data-testid="staff-deactivate-error">
			<i class="fas fa-exclamation-triangle" style="margin-right:8px"
			></i>{form.deactivateErrorMessage}
		</div>
	{/if}

	<div class="hp-listbar">
		<div class="hp-count">
			{data.staff.length} administrator{data.staff.length === 1 ? '' : 's'}
		</div>
		<button
			type="button"
			class="hp-btn hp-btn-primary"
			data-testid="staff-create-button"
			onclick={openCreate}
		>
			<i class="fas fa-plus"></i>Add New Administrator
		</button>
	</div>

	<div data-testid="staff-table">
		<h2 class="hp-h2">Active Administrators</h2>
		{@render staffTable(activeStaff, 'No active administrators')}

		<h2 class="hp-h2" style="margin-top:26px">Inactive Administrators</h2>
		{@render staffTable(inactiveStaff, 'No inactive administrators')}
	</div>

	<Modal
		bind:open={modalOpen}
		title={editing ? 'Edit Administrator' : 'Add New Administrator'}
		size="lg"
	>
		<form
			method="POST"
			action={editing ? '?/update' : '?/create'}
			data-testid="staff-form"
			use:enhance={() => {
				saving = true;
				return async ({ result, update }) => {
					saving = false;
					if (result.type === 'success') {
						toast.success(
							editing
								? 'Administrator updated successfully.'
								: 'Administrator created successfully.'
						);
						modalOpen = false;
						await invalidateAll();
					}
					await update({ reset: false });
				};
			}}
		>
			{#if editing}
				<input type="hidden" name="id" value={editing.id} />
			{/if}

			{#if errorText}
				<div class="hp-alert-red" style="margin-bottom:14px">{errorText}</div>
			{/if}

			<div class="hp-formrow top" style="padding-top:0">
				<label for="staff-email-input">Email address</label>
				<div class="hp-field">
					<input
						id="staff-email-input"
						name="email"
						type="email"
						autocomplete="email"
						bind:value={email}
						required
						class="hp-input"
						class:hp-input-error={!!fieldErrors.email}
						data-testid="staff-email"
					/>
					{#if fieldErrors.email}
						<div style="color:#d9534f;font-size:12px;margin-top:3px">
							{errText(fieldErrors.email)}
						</div>
					{/if}
				</div>
			</div>

			<div class="hp-formrow top">
				<label for="staff-password-input">Password</label>
				<div class="hp-field">
					<input
						id="staff-password-input"
						name="password"
						type="password"
						autocomplete="new-password"
						bind:value={password}
						required={!editing}
						class="hp-input"
						class:hp-input-error={!!fieldErrors.password}
						data-testid="staff-password"
					/>
					{#if fieldErrors.password}
						<div style="color:#d9534f;font-size:12px;margin-top:3px">
							{errText(fieldErrors.password)}
						</div>
					{:else}
						<div class="hp-help" style="margin-top:3px">
							{editing ? 'Leave blank to keep the current password.' : 'Minimum 8 characters.'}
						</div>
					{/if}
				</div>
			</div>

			<div class="hp-formrow">
				<label for="staff-active-input">Account active</label>
				<div class="hp-field" style="flex:1 1 auto">
					<label class="hp-checkline" style="padding:0">
						<input id="staff-active-input" name="active" type="checkbox" bind:checked={active} />
						Account active
					</label>
				</div>
			</div>

			<div class="mt-2 mb-4">
				<div class="hp-field-label" style="margin-bottom:8px">Module permissions</div>
				<div
					style="display:grid;grid-template-columns:repeat(auto-fit,minmax(140px,1fr));gap:8px 16px;border:1px solid #e2e2e2;border-radius:4px;padding:12px;background:#fafafa"
				>
					{#each PERMISSION_MODULES as mod (mod)}
						<label style="display:flex;align-items:center;gap:8px;color:#444;font-size:13px">
							<input
								type="checkbox"
								name={`perm_${mod}`}
								bind:checked={permissions[mod]}
								data-testid={`staff-permission-${mod}`}
							/>
							{moduleLabel(mod)}
						</label>
					{/each}
				</div>
			</div>

			<div class="mt-5 flex justify-end gap-2">
				<LoadingButton variant="secondary" onclick={() => (modalOpen = false)} disabled={saving}>
					Cancel
				</LoadingButton>
				<button
					type="submit"
					class="hp-btn hp-btn-primary"
					disabled={saving}
					aria-busy={saving}
					data-testid="staff-save"
				>
					Save
				</button>
			</div>
		</form>
	</Modal>

	<form
		method="POST"
		action="?/deactivate"
		id="staff-deactivate-form"
		use:enhance={() => {
			deactivating = true;
			return async ({ result, update }) => {
				deactivating = false;
				deactivateOpen = false;
				if (result.type === 'success') {
					toast.success('Administrator deactivated.');
					await invalidateAll();
				}
				await update({ reset: false });
			};
		}}
	>
		<input type="hidden" name="id" value={deactivateTarget?.id ?? ''} />
	</form>
	<ConfirmDialog
		bind:open={deactivateOpen}
		danger
		loading={deactivating}
		title="Deactivate this administrator?"
		message="They will no longer be able to sign in. You can re-enable the account later by editing it and ticking “Account active”."
		confirmLabel="Deactivate"
		onConfirm={() => {
			(document.getElementById('staff-deactivate-form') as HTMLFormElement | null)?.requestSubmit();
		}}
	/>
{/if}

<style>
	.hp-rowbtn {
		background: none;
		border: none;
		padding: 0;
		font: inherit;
		color: var(--hp-link, #337ab7);
		cursor: pointer;
	}
	.hp-rowbtn:hover {
		text-decoration: underline;
	}
</style>
