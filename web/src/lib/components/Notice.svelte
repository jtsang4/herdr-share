<script lang="ts">
	import Icon from './Icon.svelte';

	let {
		title,
		body = '',
		busy = false,
		tone = 'neutral'
	}: { title: string; body?: string; busy?: boolean; tone?: 'neutral' | 'error' } = $props();
</script>

<div class="backdrop">
	<div class="card" role="status" aria-live="polite">
		{#if busy}
			<span class="spinner" aria-hidden="true"></span>
		{:else}
			<span class={['icon', tone]}
				><Icon name={tone === 'error' ? 'alert' : 'stop'} size={18} /></span
			>
		{/if}
		<h1>{title}</h1>
		{#if body}<p>{body}</p>{/if}
	</div>
</div>

<style>
	.backdrop {
		position: fixed;
		inset: var(--bar-h) 0 0 0;
		display: grid;
		place-items: center;
		background: var(--backdrop);
		backdrop-filter: blur(2px);
		z-index: 10;
		animation: fade var(--dur-3) var(--ease-out);
	}

	.card {
		width: min(400px, calc(100vw - 32px));
		padding: var(--s-6);
		text-align: center;
		background: var(--surface);
		border-radius: var(--radius-xl);
		box-shadow: var(--shadow-pop);
	}

	h1 {
		margin: var(--s-3) 0 var(--s-1);
		font-size: var(--text-lg);
		font-weight: 600;
		letter-spacing: -0.01em;
	}

	p {
		margin: 0;
		color: var(--fg-2);
	}

	.icon {
		display: inline-grid;
		place-items: center;
		width: 36px;
		height: 36px;
		border-radius: 50%;
		color: var(--fg-2);
		background: var(--surface-2);
		border: 1px solid var(--border);
	}

	.icon.error {
		color: var(--red);
		background: var(--red-soft);
		border-color: transparent;
	}

	.spinner {
		display: inline-block;
		width: 22px;
		height: 22px;
		border-radius: 50%;
		border: 2px solid var(--border-strong);
		border-top-color: var(--fg);
		animation: spin 0.8s linear infinite;
	}

	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}

	@keyframes fade {
		from {
			opacity: 0;
		}
	}
</style>
