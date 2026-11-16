import type { Terminal } from '@xterm/xterm';

const MIN = 5;
const MAX = 20;

/**
 * Picks the largest font size (in half points, MIN..MAX) at which the
 * terminal's whole grid fits the available box. The sharer decides the grid;
 * the guest only scales it.
 */
export function fitFont(term: Terminal, screen: HTMLElement, availW: number, availH: number): void {
	let tries = 0;
	const step = () => {
		const w = screen.offsetWidth;
		const h = screen.offsetHeight;
		const fs = term.options.fontSize ?? 14;
		if (w <= 0 || h <= 0 || availW <= 0 || availH <= 0 || tries >= 5) return;
		tries++;
		const scale = Math.min(availW / w, availH / h);
		let next = Math.floor(fs * scale * 2) / 2;
		next = Math.max(MIN, Math.min(MAX, next));
		if (next !== fs) {
			term.options.fontSize = next;
			requestAnimationFrame(step);
		}
	};
	requestAnimationFrame(step);
}
