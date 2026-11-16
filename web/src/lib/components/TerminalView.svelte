<script lang="ts">
	import '@xterm/xterm/css/xterm.css';
	import { Terminal } from '@xterm/xterm';
	import { Unicode11Addon } from '@xterm/addon-unicode11';
	import { WebLinksAddon } from '@xterm/addon-web-links';
	import type { Attachment } from 'svelte/attachments';
	import { fitFont } from '#lib/fit.ts';
	import type { ShareState } from '#lib/state/share.svelte.ts';

	let { share }: { share: ShareState } = $props();

	const mount: Attachment<HTMLElement> = (node) => {
		const term = new Terminal({
			allowProposedApi: true,
			cursorBlink: false,
			disableStdin: true,
			fontFamily: getComputedStyle(document.documentElement).getPropertyValue('--font-mono'),
			fontSize: 14,
			scrollback: 5000,
			theme: {
				background: '#101114',
				foreground: '#e4e4e7',
				cursor: '#e4e4e7',
				cursorAccent: '#101114',
				selectionBackground: '#3a3a46'
			}
		});
		const unicode = new Unicode11Addon();
		term.loadAddon(unicode);
		term.unicode.activeVersion = '11';
		term.loadAddon(
			new WebLinksAddon((_e, uri) => window.open(uri, '_blank', 'noopener,noreferrer'))
		);
		term.open(node);

		// The stage around the frame is the room the terminal may fill.
		const stage = node.parentElement;
		const refit = () => {
			const screen = node.querySelector<HTMLElement>('.xterm-screen');
			if (!screen || !stage) return;
			const style = getComputedStyle(stage);
			const padX = parseFloat(style.paddingLeft) + parseFloat(style.paddingRight);
			const padY = parseFloat(style.paddingTop) + parseFloat(style.paddingBottom);
			// The terminal frame adds its own padding and border.
			fitFont(term, screen, stage.clientWidth - padX - 18, stage.clientHeight - padY - 18);
		};
		const detach = share.attachTerminal(term, refit);
		const observer = new ResizeObserver(refit);
		if (stage) observer.observe(stage);
		refit();

		return () => {
			observer.disconnect();
			detach();
			term.dispose();
		};
	};
</script>

<div class="stage">
	<div
		class={['frame', share.canType && 'typing', share.phase === 'ended' && 'ended']}
		{@attach mount}
	></div>
</div>

<style>
	.stage {
		flex: 1;
		min-height: 0;
		display: flex;
		padding: var(--s-5);
		overflow: auto;
	}

	.frame {
		/* Safe centering: a grid too large for the window scrolls instead of
		   being cut off at the top and left. */
		margin: auto;
		padding: 8px;
		background: var(--term-bg);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow-term);
		transition:
			box-shadow var(--dur-3) var(--ease),
			opacity var(--dur-3) var(--ease);
	}

	.frame.typing {
		box-shadow:
			0 0 0 1px var(--amber-dot),
			0 0 0 4px var(--amber-soft),
			var(--shadow-term);
	}

	.frame.ended {
		opacity: 0.4;
	}

	.frame :global(.xterm-viewport) {
		background-color: var(--term-bg) !important;
	}
</style>
