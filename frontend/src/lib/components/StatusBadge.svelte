<script lang="ts">
	import { t } from '$lib/i18n';

	type Variant = 'green' | 'yellow' | 'red' | 'gray' | 'blue';

	interface Props {
		status: string;
		/** Override the semantic color mapping. */
		variant?: Variant;
		/** Override the label (defaults to t('status.<status>')). */
		label?: string;
	}

	let { status, variant, label }: Props = $props();

	// Semantic map per CONTRACTS.md §13: green active/paid, yellow pending/unpaid,
	// red overdue/suspended/terminated, gray cancelled.
	const map: Record<string, Variant> = {
		active: 'green',
		paid: 'green',
		success: 'green',
		open: 'green',
		sent: 'green',
		pending: 'yellow',
		unpaid: 'yellow',
		pending_transfer: 'yellow',
		on_hold: 'yellow',
		customer_reply: 'yellow',
		queued: 'yellow',
		overdue: 'red',
		suspended: 'red',
		rejected: 'red',
		terminated: 'red',
		failed: 'red',
		fraud: 'red',
		expired: 'red',
		cancelled: 'gray',
		inactive: 'gray',
		closed: 'gray',
		draft: 'gray',
		refunded: 'gray',
		answered: 'blue'
	};

	const classes: Record<Variant, string> = {
		green: 'bg-success/10 text-success ring-success/30',
		yellow: 'bg-warning/10 text-warning ring-warning/30',
		red: 'bg-danger/10 text-danger ring-danger/30',
		gray: 'bg-gray-500/10 text-gray-600 ring-gray-400/40',
		blue: 'bg-info/10 text-info ring-info/30'
	};

	const resolved = $derived(variant ?? map[status] ?? 'gray');
	const text = $derived(label ?? t(`status.${status}`));
</script>

<span
	class={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-semibold whitespace-nowrap ring-1 ring-inset ${classes[resolved]}`}
>
	{text}
</span>
