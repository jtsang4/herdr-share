// The guest side of a share: relay connection, end-to-end channel, and the
// messages exchanged with the sharer's bridge. Mirrors internal/wire in Go.
import { Channel, CID_SIZE, decodeKey, TO_BRIDGE } from './crypto.ts';

export const ENV_INIT = 0x49; // 'I'
export const ENV_FRAME = 0x46; // 'F'
export const MSG_CONTROL = 1;
export const MSG_OUTPUT = 2;
export const MSG_INPUT = 3;

export type Mode = 'view' | 'control';

export interface AppMessage {
	t: string;
	mode?: Mode;
	title?: string;
	label?: string;
	terminal?: string;
	cols?: number;
	rows?: number;
	expires_at?: number;
	viewers?: number;
	version?: string;
	name?: string;
	client?: string;
	reason?: string;
	message?: string;
}

export interface RelayMessage {
	type: string;
	guest?: number;
	expires_at?: number;
	reason?: string;
	message?: string;
}

export type ShareEvent =
	| { type: 'connecting'; attempt: number }
	| { type: 'welcome'; msg: AppMessage }
	| { type: 'output'; data: Uint8Array }
	| { type: 'resize'; cols: number; rows: number }
	| { type: 'mode'; mode: Mode }
	| { type: 'title'; title: string }
	| { type: 'expires'; at: number }
	| { type: 'viewers'; count: number }
	| { type: 'host_offline' }
	| { type: 'host_online' }
	| { type: 'ended'; reason: string; message?: string }
	| { type: 'bad_link'; message: string };

export interface ShareLink {
	wsURL: string;
	key: Uint8Array;
	shareID: string;
}

const SHARE_PATH = /^(.*)\/s\/([A-Za-z0-9_-]{16,64})\/?$/;

/** Whether a page path is a share page (<base>/s/<id>). */
export function isSharePath(pathname: string): boolean {
	return SHARE_PATH.test(pathname);
}

/** The relay's base path for a page path ("" at the root, "/share" behind a prefix). */
export function basePath(pathname: string): string {
	const m = pathname.match(SHARE_PATH);
	if (m) return m[1];
	return pathname.replace(/\/+$/, '');
}

/** Reads the share id and key from the page URL: <base>/s/<id>#<key>. */
export function parseLocation(loc: {
	protocol: string;
	host: string;
	pathname: string;
	hash: string;
}): ShareLink {
	const m = loc.pathname.match(SHARE_PATH);
	if (!m) throw new Error('This is not a share link.');
	const [, base, shareID] = m;
	const fragment = loc.hash.replace(/^#/, '');
	if (!fragment)
		throw new Error('The link is missing its key (the part after #). Copy the whole link.');
	const key = decodeKey(fragment);
	const scheme = loc.protocol === 'https:' ? 'wss:' : 'ws:';
	return { wsURL: `${scheme}//${loc.host}${base}/api/v1/shares/${shareID}/guest`, key, shareID };
}

const enc = new TextEncoder();
const dec = new TextDecoder();

export function encodeApp(m: AppMessage): Uint8Array {
	const body = enc.encode(JSON.stringify(m));
	const out = new Uint8Array(1 + body.length);
	out[0] = MSG_CONTROL;
	out.set(body, 1);
	return out;
}

export function encodeInput(data: Uint8Array): Uint8Array {
	const out = new Uint8Array(1 + data.length);
	out[0] = MSG_INPUT;
	out.set(data, 1);
	return out;
}

/** The part of WebSocket the session uses (tests pass a fake). */
export interface SocketLike {
	binaryType: string;
	readyState: number;
	addEventListener(type: 'message', fn: (ev: { data: unknown }) => void): void;
	addEventListener(type: 'close', fn: (ev: { code: number; reason: string }) => void): void;
	send(data: Uint8Array | string): void;
	close(code?: number, reason?: string): void;
}

export interface SessionOptions {
	link: ShareLink;
	name?: string;
	client?: string;
	onEvent: (e: ShareEvent) => void;
	socket?: (url: string) => SocketLike;
	/** Milliseconds before reconnect attempt n (1-based). */
	backoff?: (attempt: number) => number;
}

const INPUT_CHUNK = 16 * 1024;

/** A live guest connection that reconnects until the share ends. */
export class ShareSession {
	private ws: SocketLike | null = null;
	private ch: Channel | null = null;
	private ended = false;
	private attempt = 0;
	private timer: ReturnType<typeof setTimeout> | null = null;

	constructor(private readonly o: SessionOptions) {}

	connect(): void {
		if (this.ended) return;
		this.attempt++;
		this.o.onEvent({ type: 'connecting', attempt: this.attempt });
		const make = this.o.socket ?? ((url: string) => new WebSocket(url) as unknown as SocketLike);
		const ws = make(this.o.link.wsURL);
		ws.binaryType = 'arraybuffer';
		this.ws = ws;
		this.ch = null;
		ws.addEventListener('message', (ev) => this.onMessage(ev.data));
		ws.addEventListener('close', (ev) => this.onClose(ws, ev.code));
	}

	/** Sends keyboard input; the bridge drops it unless the share allows typing. */
	type(data: string | Uint8Array): void {
		if (!this.ch || !this.ws || this.ws.readyState !== 1) return;
		const bytes = typeof data === 'string' ? enc.encode(data) : data;
		for (let i = 0; i < bytes.length; i += INPUT_CHUNK) {
			this.sendPlain(encodeInput(bytes.subarray(i, i + INPUT_CHUNK)));
		}
	}

	close(): void {
		this.ended = true;
		if (this.timer) clearTimeout(this.timer);
		this.ws?.close(1000, 'bye');
	}

	private sendPlain(pt: Uint8Array): void {
		if (!this.ch || !this.ws) return;
		const sealed = this.ch.seal(pt);
		const env = new Uint8Array(1 + sealed.length);
		env[0] = ENV_FRAME;
		env.set(sealed, 1);
		this.ws.send(env);
	}

	private end(reason: string, message?: string): void {
		if (this.ended) return;
		this.ended = true;
		this.o.onEvent({ type: 'ended', reason, message });
		this.ws?.close(1000, 'ended');
	}

	private onMessage(data: unknown): void {
		if (typeof data === 'string') {
			let m: RelayMessage;
			try {
				m = JSON.parse(data);
			} catch {
				return;
			}
			switch (m.type) {
				case 'joined':
					this.attempt = 0;
					break;
				case 'host_offline':
					this.o.onEvent({ type: 'host_offline' });
					break;
				case 'host_online':
					this.o.onEvent({ type: 'host_online' });
					break;
				case 'ended':
					this.end(m.reason ?? 'stopped', m.message);
					break;
			}
			return;
		}
		const buf = new Uint8Array(data as ArrayBuffer);
		if (buf.length === 0) return;
		if (buf[0] === ENV_INIT) {
			if (buf.length < 1 + CID_SIZE) return;
			const ch = new Channel(this.o.link.key, buf.slice(1, 1 + CID_SIZE), TO_BRIDGE);
			let pt: Uint8Array;
			try {
				pt = ch.open(buf.subarray(1 + CID_SIZE));
			} catch {
				this.ended = true;
				this.o.onEvent({
					type: 'bad_link',
					message:
						"This link's key does not match the share. Ask for the link again and copy all of it."
				});
				this.ws?.close(1000, 'bad key');
				return;
			}
			if (pt[0] !== MSG_CONTROL) return;
			this.ch = ch;
			this.sendPlain(
				encodeApp({ t: 'hello', name: this.o.name ?? '', client: this.o.client ?? 'web' })
			);
			return;
		}
		if (buf[0] !== ENV_FRAME || !this.ch) return;
		let pt: Uint8Array;
		try {
			pt = this.ch.open(buf.subarray(1));
		} catch {
			// A frame we cannot authenticate: drop the connection and start over.
			this.ws?.close(4002, 'auth');
			return;
		}
		if (pt.length === 0) return;
		if (pt[0] === MSG_OUTPUT) {
			this.o.onEvent({ type: 'output', data: pt.subarray(1) });
			return;
		}
		if (pt[0] !== MSG_CONTROL) return;
		let m: AppMessage;
		try {
			m = JSON.parse(dec.decode(pt.subarray(1)));
		} catch {
			return;
		}
		switch (m.t) {
			case 'welcome':
				this.o.onEvent({ type: 'welcome', msg: m });
				break;
			case 'resize':
				if (m.cols && m.rows) this.o.onEvent({ type: 'resize', cols: m.cols, rows: m.rows });
				break;
			case 'mode':
				if (m.mode) this.o.onEvent({ type: 'mode', mode: m.mode });
				break;
			case 'title':
				this.o.onEvent({ type: 'title', title: m.title ?? '' });
				break;
			case 'expires':
				this.o.onEvent({ type: 'expires', at: m.expires_at ?? 0 });
				break;
			case 'viewers':
				this.o.onEvent({ type: 'viewers', count: m.viewers ?? 0 });
				break;
			case 'end':
				this.end(m.reason ?? 'stopped', m.message);
				break;
		}
	}

	private onClose(ws: SocketLike, code: number): void {
		if (ws !== this.ws) return;
		this.ws = null;
		this.ch = null;
		if (this.ended) return;
		if (code === 4004) {
			this.end('not_found');
			return;
		}
		if (code === 4000 || code === 4010) {
			this.end(code === 4010 ? 'kicked' : 'stopped');
			return;
		}
		const delay = (this.o.backoff ?? ((n: number) => Math.min(10000, 500 * 2 ** Math.min(n, 5))))(
			this.attempt
		);
		this.timer = setTimeout(() => this.connect(), delay);
	}
}
