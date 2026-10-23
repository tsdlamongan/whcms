<script lang="ts">
	import { enhance } from '$app/forms';
	import { toast } from '$lib/stores/toast.svelte';
	import type { SubmitFunction } from '@sveltejs/kit';
	import { untrack } from 'svelte';

	/** Initial values for the form (domain.Server JSON tags; secrets are write-only). */
	interface ServerFormInitial {
		name?: string;
		module?: string;
		hostname?: string;
		port?: number;
		username?: string;
		use_ssl?: boolean;
		nameserver1?: string;
		nameserver2?: string;
		nameserver3?: string;
		nameserver4?: string;
		max_accounts?: number;
		package_prefix?: string;
		ip_address?: string;
		group_id?: number | null;
		active?: boolean;
	}

	interface GroupOption {
		id: number;
		name: string;
	}

	/** Shape returned by the ?/test action (provisioning.TestConnectionResult + flags). */
	interface TestActionData {
		ok?: boolean;
		message?: string;
		version?: string;
		hostname?: string;
		nameservers?: string[];
		errorMessage?: string;
	}

	interface Props {
		/** Form action, e.g. "?/save". */
		action: string;
		/** Test-connection action, e.g. "?/test". Omit to hide the Test button. */
		testAction?: string;
		groups: GroupOption[];
		initial?: ServerFormInitial;
		/** Toast message key on success (resolved to English text locally). */
		successKey: string;
		/** Label on the primary submit button (edit pages say "Save Changes"). */
		submitLabel?: string;
	}

	let {
		action,
		testAction,
		groups,
		initial = {},
		successKey,
		submitLabel = 'Save'
	}: Props = $props();

	const init = untrack(() => initial);

	let name = $state(init.name ?? '');
	let module = $state(init.module ?? 'cpanel');
	let hostname = $state(init.hostname ?? '');
	let port = $state(init.port ?? 2087);
	let username = $state(init.username ?? '');
	let password = $state('');
	let apiToken = $state('');
	let useSsl = $state(init.use_ssl ?? true);
	let ns1 = $state(init.nameserver1 ?? '');
	let ns2 = $state(init.nameserver2 ?? '');
	let ns3 = $state(init.nameserver3 ?? '');
	let ns4 = $state(init.nameserver4 ?? '');
	let maxAccounts = $state(init.max_accounts ?? 0);
	let packagePrefix = $state(init.package_prefix ?? '');
	let ipAddress = $state(init.ip_address ?? '');
	let groupId = $state(init.group_id ? String(init.group_id) : '');
	let active = $state(init.active ?? true);

	// Real WHM reseller accounts commonly need every package name prefixed
	// with the reseller's own username ("root" never does) - auto-derive it
	// from Username so admins don't have to know/type the convention by hand.
	// Stops once the admin edits the prefix directly, so it never clobbers an
	// intentional value (including an already-saved one on an edit page).
	let packagePrefixTouched = $state((init.package_prefix ?? '').trim() !== '');
	function syncPackagePrefixFromUsername(value: string) {
		if (packagePrefixTouched) return;
		const u = value.trim();
		packagePrefix = u && u.toLowerCase() !== 'root' ? `${u}_` : '';
	}

	let submitting = $state(false);
	let testing = $state(false);
	let testResult = $state<{ ok: boolean; message: string } | null>(null);

	const SUCCESS_MESSAGES: Record<string, string> = {
		'adminops.servers.saveSuccess': 'Server saved successfully.'
	};
	const ERROR_MESSAGES: Record<string, string> = {
		'adminops.servers.fillRequired': 'Fill in all required fields.'
	};

	// The known default management ports; switching module/SSL updates the port
	// only while it still holds one of these (never a value the admin typed).
	const KNOWN_PORTS = new Set([2082, 2083, 2086, 2087, 2222]);
	function defaultPortFor(mod: string, ssl: boolean): number {
		if (mod === 'directadmin') return 2222;
		return ssl ? 2087 : 2086;
	}
	function syncPort() {
		if (!port || port <= 0 || KNOWN_PORTS.has(port)) {
			port = defaultPortFor(module, useSsl);
		}
	}

	/** Apply Test-Connection metadata to blank fields; returns how many were filled. */
	function applyAutofill(d: TestActionData): number {
		let filled = 0;
		const ns = Array.isArray(d.nameservers) ? d.nameservers : [];
		const slots: Array<[string, (v: string) => void]> = [
			[ns1, (v) => (ns1 = v)],
			[ns2, (v) => (ns2 = v)],
			[ns3, (v) => (ns3 = v)],
			[ns4, (v) => (ns4 = v)]
		];
		ns.slice(0, 4).forEach((val, i) => {
			if (val && !slots[i][0].trim()) {
				slots[i][1](val);
				filled++;
			}
		});
		if (!port || port <= 0) {
			port = defaultPortFor(module, useSsl);
			filled++;
		}
		return filled;
	}

	const submitHandler: SubmitFunction = ({ action: submittedAction, submitter }) => {
		const formAction = submitter?.getAttribute('formaction') ?? submittedAction.search;
		const isTest = formAction.includes('test');
		if (isTest) {
			testing = true;
		} else {
			submitting = true;
		}
		return async ({ result, update }) => {
			testing = false;
			submitting = false;

			if (isTest) {
				// Never invalidate/reset on a test - keep the admin's input + autofill.
				if (result.type === 'success') {
					const d = (result.data ?? {}) as TestActionData;
					const filled = applyAutofill(d);
					const base = d.version
						? `Connection successful (${d.version}).`
						: 'Connection successful.';
					const msg = filled > 0 ? `${base} Some values have been auto-filled.` : base;
					testResult = { ok: true, message: msg };
					toast.success(msg);
				} else if (result.type === 'failure') {
					const d = (result.data ?? {}) as TestActionData;
					const msg = d.errorMessage
						? `Connection failed — ${d.errorMessage}`
						: 'Connection failed.';
					testResult = { ok: false, message: msg };
					toast.error(msg);
				} else if (result.type === 'error') {
					testResult = { ok: false, message: 'Connection failed.' };
					toast.error('Connection failed.');
				}
				return;
			}

			// Save: reset:false so a successful save never blanks the (bound) form.
			if (result.type === 'success') {
				toast.success(SUCCESS_MESSAGES[successKey] ?? 'Saved successfully.');
			} else if (result.type === 'failure') {
				const d = result.data as { errorKey?: string; errorMessage?: string } | undefined;
				toast.error(
					d?.errorKey
						? (ERROR_MESSAGES[d.errorKey] ?? 'Something went wrong.')
						: (d?.errorMessage ?? 'Something went wrong.')
				);
			}
			await update({ reset: false });
		};
	};
</script>

<form method="POST" {action} data-testid="server-form" use:enhance={submitHandler}>
	{#if testResult}
		<div class={testResult.ok ? 'hp-alert-green' : 'hp-alert-red'} data-testid="server-test-result">
			<i
				class={testResult.ok ? 'fas fa-check-circle' : 'fas fa-exclamation-triangle'}
				style="margin-right:8px"
			></i>{testResult.message}
		</div>
	{/if}

	<div class="hp-formrow">
		<label for="field-name">Name<span style="color:#d9534f"> *</span></label>
		<div class="hp-field">
			<input
				id="field-name"
				name="name"
				class="hp-input"
				bind:value={name}
				required
				data-testid="server-field-name"
			/>
		</div>
	</div>
	<div class="hp-formrow">
		<label for="field-module">Module<span style="color:#d9534f"> *</span></label>
		<div class="hp-field">
			<select
				id="field-module"
				name="module"
				class="hp-select"
				bind:value={module}
				onchange={syncPort}
				required
				data-testid="server-field-module"
			>
				<option value="cpanel">cPanel/WHM</option>
				<option value="directadmin">DirectAdmin</option>
			</select>
		</div>
	</div>
	<div class="hp-formrow">
		<label for="field-hostname">Hostname<span style="color:#d9534f"> *</span></label>
		<div class="hp-field">
			<input
				id="field-hostname"
				name="hostname"
				class="hp-input"
				bind:value={hostname}
				placeholder="server1.example.com"
				required
				data-testid="server-field-hostname"
			/>
		</div>
	</div>
	<div class="hp-formrow">
		<label for="field-port">Port<span style="color:#d9534f"> *</span></label>
		<div class="hp-field">
			<input
				id="field-port"
				name="port"
				type="number"
				class="hp-input"
				bind:value={port}
				required
				data-testid="server-field-port"
			/>
		</div>
	</div>
	<div class="hp-formrow">
		<label for="field-username">Username<span style="color:#d9534f"> *</span></label>
		<div class="hp-field">
			<input
				id="field-username"
				name="username"
				class="hp-input"
				bind:value={username}
				oninput={(e) => syncPackagePrefixFromUsername(e.currentTarget.value)}
				required
				data-testid="server-field-username"
			/>
		</div>
	</div>
	<div class="hp-formrow">
		<label for="field-group_id">Group</label>
		<div class="hp-field">
			<select
				id="field-group_id"
				name="group_id"
				class="hp-select"
				bind:value={groupId}
				data-testid="server-field-group_id"
			>
				<option value="">No group</option>
				{#each groups as g (g.id)}
					<option value={String(g.id)}>{g.name}</option>
				{/each}
			</select>
		</div>
	</div>
	<div class="hp-formrow">
		<label for="field-password">Password</label>
		<div class="hp-field">
			<input
				id="field-password"
				name="password"
				type="password"
				class="hp-input"
				bind:value={password}
				autocomplete="new-password"
				data-testid="server-field-password"
			/>
			<div class="hp-help" style="margin-top:3px">Leave blank to keep the current password.</div>
		</div>
	</div>
	<div class="hp-formrow">
		<label for="field-api_token">API token</label>
		<div class="hp-field">
			<input
				id="field-api_token"
				name="api_token"
				type="password"
				class="hp-input"
				bind:value={apiToken}
				autocomplete="off"
				data-testid="server-field-api_token"
			/>
			<div class="hp-help" style="margin-top:3px">Leave blank to keep the current token.</div>
		</div>
	</div>
	<div class="hp-formrow">
		<label for="field-nameserver1">Nameserver 1</label>
		<div class="hp-field">
			<input
				id="field-nameserver1"
				name="nameserver1"
				class="hp-input"
				bind:value={ns1}
				placeholder="ns1.example.com"
				data-testid="server-field-nameserver1"
			/>
		</div>
	</div>
	<div class="hp-formrow">
		<label for="field-nameserver2">Nameserver 2</label>
		<div class="hp-field">
			<input
				id="field-nameserver2"
				name="nameserver2"
				class="hp-input"
				bind:value={ns2}
				placeholder="ns2.example.com"
				data-testid="server-field-nameserver2"
			/>
		</div>
	</div>
	<div class="hp-formrow">
		<label for="field-nameserver3">Nameserver 3</label>
		<div class="hp-field">
			<input
				id="field-nameserver3"
				name="nameserver3"
				class="hp-input"
				bind:value={ns3}
				data-testid="server-field-nameserver3"
			/>
		</div>
	</div>
	<div class="hp-formrow">
		<label for="field-nameserver4">Nameserver 4</label>
		<div class="hp-field">
			<input
				id="field-nameserver4"
				name="nameserver4"
				class="hp-input"
				bind:value={ns4}
				data-testid="server-field-nameserver4"
			/>
		</div>
	</div>
	<div class="hp-formrow">
		<label for="field-max_accounts">Max accounts</label>
		<div class="hp-field">
			<input
				id="field-max_accounts"
				name="max_accounts"
				type="number"
				class="hp-input"
				bind:value={maxAccounts}
				data-testid="server-field-max_accounts"
			/>
			<div class="hp-help" style="margin-top:3px">0 = unlimited.</div>
		</div>
	</div>
	<div class="hp-formrow">
		<label for="field-package_prefix">Package name prefix</label>
		<div class="hp-field">
			<input
				id="field-package_prefix"
				name="package_prefix"
				class="hp-input"
				bind:value={packagePrefix}
				oninput={() => (packagePrefixTouched = true)}
				data-testid="server-field-package_prefix"
			/>
			<div class="hp-help" style="margin-top:3px">
				Optional. Some real WHM reseller accounts require every package name to carry the reseller's
				own prefix (e.g. "reseller_") — leave blank for a dedicated/non-reseller server. Auto-filled
				from Username (unless it's "root") until you edit this field directly.
			</div>
		</div>
	</div>
	<div class="hp-formrow">
		<label for="field-ip_address">IP address</label>
		<div class="hp-field">
			<input
				id="field-ip_address"
				name="ip_address"
				class="hp-input"
				bind:value={ipAddress}
				placeholder="203.0.113.10"
				data-testid="server-field-ip_address"
			/>
			<div class="hp-help" style="margin-top:3px">
				Required for DirectAdmin: a valid IP from the reseller's own pool, used when creating
				hosting accounts (DirectAdmin has no auto-assign-shared-IP behavior like WHM). Not needed
				for cPanel/WHM servers.
			</div>
		</div>
	</div>
	<div class="hp-formrow">
		<label for="field-use_ssl">&nbsp;</label>
		<div class="hp-field" style="flex:1 1 auto">
			<label class="hp-checkline" style="padding:2px 0">
				<input
					id="field-use_ssl"
					type="checkbox"
					name="use_ssl"
					bind:checked={useSsl}
					onchange={syncPort}
					data-testid="server-field-use_ssl"
				/>
				Use SSL
			</label>
			<label class="hp-checkline" style="padding:2px 0">
				<input
					type="checkbox"
					name="active"
					bind:checked={active}
					data-testid="server-field-active"
				/>
				Active
			</label>
		</div>
	</div>

	<div class="hp-form-actions">
		<span style="display:contents" data-testid="server-save-button">
			<button type="submit" class="hp-btn hp-btn-primary" disabled={submitting || testing}>
				{submitting ? 'Saving…' : submitLabel}
			</button>
		</span>
		{#if testAction}
			<span style="display:contents" data-testid="server-test-button">
				<button
					type="submit"
					class="hp-btn"
					formaction={testAction}
					formnovalidate
					disabled={submitting || testing}
				>
					<i class="fas fa-plug"></i>{testing ? 'Testing…' : 'Test Connection'}
				</button>
			</span>
		{/if}
	</div>
</form>

<style>
	.hp-alert-green {
		background: #dff0d8;
		border: 1px solid #d6e9c6;
		border-radius: 4px;
		padding: 12px 14px;
		margin-bottom: 14px;
		color: #3c763d;
	}
</style>
