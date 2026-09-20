<script lang="ts">
	import { appName } from '$lib/appName';
	import { t } from '$lib/i18n';
	import AnnouncementForm from '../AnnouncementForm.svelte';
	import type { PageProps } from './$types';

	let { form }: PageProps = $props();

	const errorText = $derived(form?.errorKey ? t(form.errorKey) : (form?.errorMessage ?? null));
</script>

<svelte:head>
	<title>New Announcement — {appName} Admin</title>
</svelte:head>

<h1 class="hp-h1">Add New Announcement</h1>
<p class="hp-lead">Publish news &amp; updates to the client area announcements page.</p>

<AnnouncementForm {errorText} submitLabel="Create Announcement" />
