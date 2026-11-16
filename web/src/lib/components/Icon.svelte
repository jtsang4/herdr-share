<script lang="ts">
	type Name = 'lock' | 'eye' | 'keyboard' | 'clock' | 'users' | 'alert' | 'stop' | 'link';
	let { name, size = 14 }: { name: Name; size?: number } = $props();
</script>

<svg
	width={size}
	height={size}
	viewBox="0 0 16 16"
	fill="none"
	stroke="currentColor"
	stroke-width="1.5"
	stroke-linecap="round"
	stroke-linejoin="round"
	aria-hidden="true"
>
	{#if name === 'lock'}
		<rect x="3" y="7" width="10" height="7" rx="1.5" />
		<path d="M5.5 7V5a2.5 2.5 0 0 1 5 0v2" />
	{:else if name === 'eye'}
		<path d="M1.5 8s2.4-4.5 6.5-4.5S14.5 8 14.5 8s-2.4 4.5-6.5 4.5S1.5 8 1.5 8Z" />
		<circle cx="8" cy="8" r="2" />
	{:else if name === 'keyboard'}
		<rect x="1.5" y="4" width="13" height="8" rx="1.5" />
		<path d="M4.5 7h.01M7 7h.01M9.5 7h.01M12 7h.01M5 9.5h6" />
	{:else if name === 'clock'}
		<circle cx="8" cy="8" r="6" />
		<path d="M8 5v3l2 1.5" />
	{:else if name === 'users'}
		<circle cx="6" cy="6" r="2.5" />
		<path d="M1.5 13.5c.5-2.3 2.3-3.5 4.5-3.5s4 1.2 4.5 3.5" />
		<path d="M10.5 3.6a2.5 2.5 0 0 1 0 4.8M12 10.3c1.3.5 2.2 1.6 2.5 3.2" />
	{:else if name === 'alert'}
		<circle cx="8" cy="8" r="6" />
		<path d="M8 5v3.5M8 11h.01" />
	{:else if name === 'stop'}
		<circle cx="8" cy="8" r="6" />
		<rect x="6" y="6" width="4" height="4" rx=".5" />
	{:else if name === 'link'}
		<path d="M6.5 9.5 9.5 6.5" />
		<path
			d="M7 4.5 8.2 3.3a2.6 2.6 0 0 1 3.6 3.6L10.5 8.2M9 11.5l-1.2 1.2a2.6 2.6 0 0 1-3.6-3.6L5.5 7.8"
		/>
	{/if}
</svg>
