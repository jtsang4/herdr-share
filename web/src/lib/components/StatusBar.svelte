<script lang="ts">
	import Icon from './Icon.svelte';
	import Logo from './Logo.svelte';
	import { formatRemaining, t } from '#lib/i18n.ts';
	import type { ShareState } from '#lib/state/share.svelte.ts';

	let { share }: { share: ShareState } = $props();

	let live = $derived(share.phase === 'live' || share.phase === 'ended');
	let timeLeft = $derived(
		share.remaining === null ? t.noExpiry : t.timeLeft(formatRemaining(share.remaining))
	);
</script>

<header class="bar">
	<div class="left">
		<Logo size={20} />
		<span class="title">{share.title || 'herdr-share'}</span>
		{#if share.label}
			<span class="label">{share.label}</span>
		{/if}
	</div>
	{#if live}
		<div class="right">
			<span class="chip e2ee" title={t.e2eeHint}>
				<Icon name="lock" size={13} />
				<span class="hide-sm">{t.e2ee}</span>
			</span>
			{#if share.viewers > 0}
				<span class="chip" data-testid="viewers">
					<Icon name="users" size={13} />
					{t.viewers(share.viewers)}
				</span>
			{/if}
			{#if share.phase === 'live'}
				<span class="chip hide-sm" data-testid="expires">
					<Icon name="clock" size={13} />
					{timeLeft}
				</span>
				<span
					class={['chip', 'mode', share.mode]}
					title={share.mode === 'control' ? t.canTypeHint : t.viewOnlyHint}
					data-testid="mode"
					data-mode={share.mode}
				>
					<Icon name={share.mode === 'control' ? 'keyboard' : 'eye'} size={13} />
					{share.mode === 'control' ? t.canType : t.viewOnly}
				</span>
			{/if}
		</div>
	{/if}
</header>

<style>
	.bar {
		flex: none;
		height: var(--bar-h);
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--s-4);
		padding: 0 var(--s-4);
		border-bottom: 1px solid var(--border);
		background: var(--surface);
	}

	.left,
	.right {
		display: flex;
		align-items: center;
		gap: var(--s-2);
		min-width: 0;
	}

	.title {
		font-weight: 600;
		font-size: var(--text-md);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
		margin-left: var(--s-1);
	}

	.label {
		color: var(--fg-3);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.chip {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		height: 26px;
		padding: 0 10px;
		border-radius: 999px;
		border: 1px solid var(--border);
		color: var(--fg-2);
		font-size: var(--text-xs);
		white-space: nowrap;
		font-variant-numeric: tabular-nums;
	}

	.e2ee {
		color: var(--green);
		border-color: transparent;
		background: var(--green-soft);
		cursor: help;
	}

	.mode.control {
		color: var(--amber);
		border-color: transparent;
		background: var(--amber-soft);
		font-weight: 600;
	}

	.mode.view {
		color: var(--fg);
	}

	@media (max-width: 640px) {
		.hide-sm,
		.label {
			display: none;
		}
	}
</style>
