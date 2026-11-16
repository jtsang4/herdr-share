import type { Terminal } from '@xterm/xterm';
import { endedText, t } from '#lib/i18n.ts';
import { type Mode, parseLocation, type ShareEvent, ShareSession } from '#lib/protocol.ts';

export type Phase = 'connecting' | 'live' | 'ended' | 'bad_link';

/** Everything the share page shows, fed by the guest session's events. */
export class ShareState {
	phase = $state<Phase>('connecting');
	title = $state('');
	label = $state('');
	mode = $state<Mode>('view');
	viewers = $state(0);
	expiresAt = $state(0);
	cols = $state(80);
	rows = $state(24);
	reconnecting = $state(false);
	hostOffline = $state(false);
	endTitle = $state('');
	endBody = $state('');
	notice = $state<string | null>(null);
	now = $state(Date.now());

	/** Seconds until the share expires; null without a limit. */
	remaining = $derived(this.expiresAt ? Math.max(0, this.expiresAt - this.now / 1000) : null);
	canType = $derived(this.phase === 'live' && this.mode === 'control');

	#term: Terminal | null = null;
	#pending: Uint8Array[] = [];
	#session: ShareSession | null = null;
	#clock: ReturnType<typeof setInterval> | null = null;
	#noticeTimer: ReturnType<typeof setTimeout> | null = null;
	#onGrid: (() => void) | null = null;

	start(loc: Location, name: string): void {
		let link;
		try {
			link = parseLocation(loc);
		} catch (err) {
			this.#fail((err as Error).message);
			return;
		}
		this.#clock = setInterval(() => (this.now = Date.now()), 15_000);
		this.#session = new ShareSession({
			link,
			name,
			client: 'web',
			onEvent: (e) => this.#onEvent(e)
		});
		this.#session.connect();
	}

	stop(): void {
		this.#session?.close();
		if (this.#clock) clearInterval(this.#clock);
		if (this.#noticeTimer) clearTimeout(this.#noticeTimer);
	}

	/** Connects the terminal widget; output that arrived before it is replayed. */
	attachTerminal(term: Terminal, onGrid: () => void): () => void {
		this.#term = term;
		this.#onGrid = onGrid;
		term.resize(this.cols, this.rows);
		for (const chunk of this.#pending) term.write(chunk);
		this.#pending = [];
		this.#syncInput();
		const data = term.onData((d) => {
			if (this.canType) this.#session?.type(d);
		});
		const binary = term.onBinary((d) => {
			if (this.canType) this.#session?.type(Uint8Array.from(d, (c) => c.charCodeAt(0) & 0xff));
		});
		return () => {
			data.dispose();
			binary.dispose();
			this.#term = null;
			this.#onGrid = null;
		};
	}

	/** The terminal's text, for automated end-to-end tests. */
	text(): string {
		const b = this.#term?.buffer.active;
		if (!b) return '';
		const lines: string[] = [];
		for (let i = 0; i < b.length; i++) lines.push(b.getLine(i)?.translateToString(true) ?? '');
		return lines.join('\n');
	}

	/** Lets the terminal take keystrokes only while the sharer allows typing. */
	#syncInput(): void {
		if (!this.#term) return;
		const can = this.phase === 'live' && this.mode === 'control';
		this.#term.options.disableStdin = !can;
		this.#term.options.cursorBlink = can;
		if (can) this.#term.focus();
	}

	#flash(message: string): void {
		this.notice = message;
		if (this.#noticeTimer) clearTimeout(this.#noticeTimer);
		this.#noticeTimer = setTimeout(() => (this.notice = null), 4000);
	}

	#fail(message: string): void {
		this.phase = 'bad_link';
		this.endTitle = t.badLinkTitle;
		this.endBody = message;
	}

	#grid(cols: number, rows: number): void {
		if (cols <= 0 || rows <= 0) return;
		this.cols = cols;
		this.rows = rows;
		this.#term?.resize(cols, rows);
		this.#onGrid?.();
	}

	#onEvent(e: ShareEvent): void {
		switch (e.type) {
			case 'connecting':
				this.reconnecting = this.phase === 'live';
				break;
			case 'welcome': {
				const m = e.msg;
				// A welcome starts a fresh paint: first join, or after a reconnect.
				this.#term?.reset();
				this.#pending = [];
				this.phase = 'live';
				this.reconnecting = false;
				this.hostOffline = false;
				this.title = m.title ?? '';
				this.label = m.label ?? '';
				this.expiresAt = m.expires_at ?? 0;
				this.now = Date.now();
				if (m.viewers) this.viewers = m.viewers;
				this.mode = m.mode === 'control' ? 'control' : 'view';
				this.#grid(m.cols ?? this.cols, m.rows ?? this.rows);
				this.#syncInput();
				break;
			}
			case 'output':
				if (this.#term) this.#term.write(e.data);
				else this.#pending.push(e.data.slice());
				break;
			case 'resize':
				this.#grid(e.cols, e.rows);
				break;
			case 'mode':
				if (e.mode !== this.mode) {
					this.mode = e.mode;
					this.#flash(e.mode === 'control' ? t.nowCanType : t.nowViewOnly);
					this.#syncInput();
				}
				break;
			case 'title':
				this.title = e.title;
				break;
			case 'expires':
				this.expiresAt = e.at;
				this.now = Date.now();
				break;
			case 'viewers':
				this.viewers = e.count;
				break;
			case 'host_offline':
				this.hostOffline = true;
				break;
			case 'host_online':
				this.hostOffline = false;
				break;
			case 'ended':
				this.phase = 'ended';
				this.reconnecting = false;
				this.hostOffline = false;
				this.endTitle = t.endedTitle;
				this.endBody = endedText(e.reason, e.message);
				this.#syncInput();
				if (this.#clock) clearInterval(this.#clock);
				break;
			case 'bad_link':
				this.#fail(e.message);
				break;
		}
	}
}
