<script lang="ts">
	import { onMount } from 'svelte';
	import Logo from './Logo.svelte';
	import { t } from '#lib/i18n.ts';
	import { basePath } from '#lib/protocol.ts';

	type Info = { version: string; protocol: number; auth_required: boolean };
	let info = $state<Info | null>(null);

	onMount(() => {
		document.documentElement.lang = t.lang;
		const ctl = new AbortController();
		fetch(`${basePath(location.pathname)}/api/v1/info`, { signal: ctl.signal })
			.then((r) => (r.ok ? r.json() : null))
			.then((v: Info | null) => (info = v))
			.catch(() => {});
		return () => ctl.abort();
	});
</script>

<svelte:head>
	<title>{t.landingTitle}</title>
</svelte:head>

<main>
	<Logo size={36} />
	<h1>{t.landingTitle}</h1>
	<p>{t.landingBody}</p>
	<p class="muted">{t.landingPrivacy}</p>
	{#if info}
		<dl>
			<dt>{t.version}</dt>
			<dd>{info.version} · protocol {info.protocol}</dd>
			<dt>{t.auth}</dt>
			<dd>{info.auth_required ? t.authToken : t.authOpen}</dd>
		</dl>
	{/if}
	<a href="https://github.com/jtsang4/herdr-share" rel="noopener noreferrer">{t.source} ↗</a>
</main>

<style>
	main {
		max-width: 520px;
		margin: 0 auto;
		padding: 18vh var(--s-6) var(--s-8);
	}

	h1 {
		margin: var(--s-5) 0 var(--s-3);
		font-size: 22px;
		font-weight: 600;
		letter-spacing: -0.015em;
	}

	p {
		margin: 0 0 var(--s-3);
		color: var(--fg-2);
		font-size: var(--text-md);
		line-height: 1.6;
	}

	.muted {
		color: var(--fg-3);
	}

	dl {
		display: grid;
		grid-template-columns: max-content 1fr;
		gap: var(--s-1) var(--s-4);
		margin: var(--s-6) 0;
		padding: var(--s-4) 0;
		border-top: 1px solid var(--border);
		border-bottom: 1px solid var(--border);
		font-size: var(--text-xs);
	}

	dt {
		color: var(--fg-3);
	}

	dd {
		margin: 0;
		font-family: var(--font-mono);
	}

	a {
		color: var(--fg-2);
		font-size: var(--text-xs);
		text-decoration: none;
		border-bottom: 1px solid var(--border-strong);
	}

	a:hover {
		color: var(--fg);
	}
</style>
