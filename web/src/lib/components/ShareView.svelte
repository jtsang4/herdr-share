<script lang="ts">
	import { onMount } from 'svelte';
	import Notice from './Notice.svelte';
	import StatusBar from './StatusBar.svelte';
	import TerminalView from './TerminalView.svelte';
	import { t } from '#lib/i18n.ts';
	import { ShareState } from '#lib/state/share.svelte.ts';

	const share = new ShareState();

	onMount(() => {
		document.documentElement.lang = t.lang;
		const params = new URLSearchParams(location.search);
		const fromLink = params.get('name');
		if (fromLink) localStorage.setItem('herdr-share.name', fromLink);
		share.start(location, fromLink ?? localStorage.getItem('herdr-share.name') ?? '');
		// Read-only hooks for automated end-to-end tests.
		(window as unknown as { herdrShare: unknown }).herdrShare = {
			get phase() {
				return share.phase;
			},
			get mode() {
				return share.mode;
			},
			get grid() {
				return { cols: share.cols, rows: share.rows };
			},
			text: () => share.text()
		};
		return () => share.stop();
	});

	let banner = $derived(
		share.phase === 'live'
			? share.reconnecting
				? t.reconnecting
				: share.hostOffline
					? t.waitingHost
					: share.notice
			: null
	);
</script>

<svelte:head>
	<title>{share.title ? `${share.title} · herdr-share` : 'herdr-share'}</title>
</svelte:head>

<div class="page">
	<StatusBar {share} />
	<TerminalView {share} />
</div>

{#if banner}
	<div class="banner" role="status" aria-live="polite">{banner}</div>
{/if}

{#if share.phase === 'connecting'}
	<Notice title={t.connecting} busy />
{:else if share.phase === 'ended'}
	<Notice title={share.endTitle} body={share.endBody} />
{:else if share.phase === 'bad_link'}
	<Notice title={share.endTitle} body={share.endBody} tone="error" />
{/if}

<style>
	.page {
		height: 100dvh;
		display: flex;
		flex-direction: column;
	}

	.banner {
		position: fixed;
		top: calc(var(--bar-h) + var(--s-3));
		left: 50%;
		transform: translateX(-50%);
		max-width: calc(100vw - 32px);
		padding: var(--s-2) var(--s-4);
		border-radius: 999px;
		background: var(--fg);
		color: var(--bg);
		font-size: var(--text-xs);
		font-weight: 500;
		box-shadow: var(--shadow-pop);
		z-index: 20;
		animation: drop var(--dur-3) var(--ease-out);
	}

	@keyframes drop {
		from {
			opacity: 0;
			transform: translate(-50%, -6px);
		}
	}
</style>
