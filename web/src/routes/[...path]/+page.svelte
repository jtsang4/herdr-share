<script lang="ts">
	import { page } from '$app/state';
	import Landing from '#lib/components/Landing.svelte';
	import ShareView from '#lib/components/ShareView.svelte';
	import { isSharePath } from '#lib/protocol.ts';

	// The relay may sit under a path prefix, so match the end of the path.
	let isShare = $derived(isSharePath(page.url.pathname));
</script>

{#if isShare}
	<ShareView />
{:else}
	<Landing />
{/if}
