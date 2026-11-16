<script lang="ts">
	let { size = 20 }: { size?: number } = $props();
</script>

<svg width={size} height={size} viewBox="0 0 32 32" aria-hidden="true">
	<rect width="32" height="32" rx="8" fill="var(--fg)" />
	<path
		d="M9 11.5 14 16l-5 4.5"
		fill="none"
		stroke="var(--bg)"
		stroke-width="2.4"
		stroke-linecap="round"
		stroke-linejoin="round"
	/>
	<circle cx="21.5" cy="20.5" r="3" fill="var(--amber-dot)" />
</svg>
