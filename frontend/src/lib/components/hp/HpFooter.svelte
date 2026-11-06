<script lang="ts">
	import { appName } from '$lib/appName';

	const year = new Date().getFullYear();
</script>

<footer class="hp-foot">
	<span>Copyright © {appName} {year}. All Rights Reserved.</span>
	<span>
		<a href="/admin">Report a Bug</a> | <a href="/admin">Documentation</a> |
		<a href="/admin">Contact Us</a>
	</span>
</footer>
