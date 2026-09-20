<script lang="ts">
	import { appName } from '$lib/appName';
	import { Alert, DateText, StatusBadge } from '$lib/components';
	import { t } from '$lib/i18n';
	import type { PageProps } from './$types';
	import type { NetworkIssue } from './+page.server';

	let { data }: PageProps = $props();

	type Variant = 'green' | 'yellow' | 'red' | 'gray' | 'blue';
	const statusVariant: Record<NetworkIssue['status'], Variant> = {
		investigating: 'yellow',
		identified: 'yellow',
		monitoring: 'blue',
		resolved: 'green',
		scheduled: 'gray'
	};

	const typeLabel = (v: string) => t(`portal.networkStatus.type.${v}`);
	const severityLabel = (v: string) => t(`portal.networkStatus.severity.${v}`);
	const statusLabel = (v: string) => t(`portal.networkStatus.statusLabel.${v}`);
</script>

<svelte:head>
	<title>{t('portal.networkStatus.title')} — {appName}</title>
</svelte:head>

<h1 class="ca-h1">{t('portal.networkStatus.title')}</h1>
<p class="ca-lead">{t('portal.networkStatus.subtitle')}</p>

{#if data.loadError}
	<Alert type="error">{data.loadError}</Alert>
{/if}

<div data-testid="network-status">
	<div style="overflow-x:auto;">
		<table class="ca-cart-table">
			<thead>
				<tr>
					<th>{t('portal.networkStatus.colTitle')}</th>
					<th>{t('portal.networkStatus.colType')}</th>
					<th>{t('portal.networkStatus.colSeverity')}</th>
					<th>{t('portal.networkStatus.colStatus')}</th>
					<th>{t('portal.networkStatus.colAffected')}</th>
					<th>{t('portal.networkStatus.colStarted')}</th>
				</tr>
			</thead>
			<tbody>
				{#if data.issues.length === 0}
					<tr>
						<td colspan="6" class="ca-cart-empty">
							<i class="fas fa-check-circle" aria-hidden="true" style="color:var(--ca-success);"
							></i>
							{t('portal.networkStatus.allOperational')}
						</td>
					</tr>
				{:else}
					{#each data.issues as issue (issue.id)}
						<tr>
							<td>
								<strong>{issue.title}</strong>
								{#if issue.body}
									<div class="ca-muted" style="font-size:0.85rem;margin-top:2px;">{issue.body}</div>
								{/if}
							</td>
							<td>{typeLabel(issue.type)}</td>
							<td>{severityLabel(issue.severity)}</td>
							<td>
								<StatusBadge
									status={issue.status}
									variant={statusVariant[issue.status]}
									label={statusLabel(issue.status)}
								/>
							</td>
							<td>{issue.affected || '—'}</td>
							<td><DateText value={issue.starts_at} mode="datetime" /></td>
						</tr>
					{/each}
				{/if}
			</tbody>
		</table>
	</div>
</div>
