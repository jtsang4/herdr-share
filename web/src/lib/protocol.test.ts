import { describe, expect, test } from 'vitest';
import { Channel, TO_GUEST } from './crypto.ts';
import {
	basePath,
	ENV_FRAME,
	ENV_INIT,
	encodeApp,
	isSharePath,
	MSG_CONTROL,
	MSG_INPUT,
	MSG_OUTPUT,
	parseLocation,
	type ShareEvent,
	ShareSession,
	type SocketLike
} from './protocol.ts';

const KEY = 'AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8';
const ID = 'abcdefghijklmnopqrstuv';

describe('links', () => {
	test('parses a share link at the root and behind a prefix', () => {
		const a = parseLocation({
			protocol: 'https:',
			host: 'r.example',
			pathname: `/s/${ID}`,
			hash: `#${KEY}`
		});
		expect(a.wsURL).toBe(`wss://r.example/api/v1/shares/${ID}/guest`);
		expect(a.shareID).toBe(ID);
		const b = parseLocation({
			protocol: 'http:',
			host: '10.0.0.2:8080',
			pathname: `/share/s/${ID}`,
			hash: `#${KEY}`
		});
		expect(b.wsURL).toBe(`ws://10.0.0.2:8080/share/api/v1/shares/${ID}/guest`);
	});

	test('rejects links without a key or id', () => {
		expect(() =>
			parseLocation({ protocol: 'https:', host: 'r', pathname: `/s/${ID}`, hash: '' })
		).toThrow(/key/);
		expect(() =>
			parseLocation({ protocol: 'https:', host: 'r', pathname: '/s/short', hash: `#${KEY}` })
		).toThrow();
	});

	test('recognizes share paths and base paths', () => {
		expect(isSharePath(`/s/${ID}`)).toBe(true);
		expect(isSharePath('/')).toBe(false);
		expect(basePath(`/x/s/${ID}`)).toBe('/x');
		expect(basePath('/')).toBe('');
	});
});

class FakeSocket implements SocketLike {
	binaryType = '';
	readyState = 1;
	sent: (Uint8Array | string)[] = [];
	#onMessage: ((ev: { data: unknown }) => void)[] = [];
	#onClose: ((ev: { code: number; reason: string }) => void)[] = [];
	addEventListener(type: 'message', fn: (ev: { data: unknown }) => void): void;
	addEventListener(type: 'close', fn: (ev: { code: number; reason: string }) => void): void;
	addEventListener(
		type: 'message' | 'close',
		fn: ((ev: { data: unknown }) => void) | ((ev: { code: number; reason: string }) => void)
	): void {
		if (type === 'message') this.#onMessage.push(fn as (ev: { data: unknown }) => void);
		else this.#onClose.push(fn as (ev: { code: number; reason: string }) => void);
	}
	receive(data: unknown) {
		for (const fn of this.#onMessage) fn({ data });
	}
	send(d: Uint8Array | string) {
		this.sent.push(d);
	}
	close(code = 1000) {
		this.readyState = 3;
		for (const fn of this.#onClose) fn({ code, reason: '' });
	}
}

function setup() {
	const key = new Uint8Array(32).map((_, i) => i);
	const cid = new Uint8Array(16).map((_, i) => 0xa0 + i);
	const events: ShareEvent[] = [];
	const sockets: FakeSocket[] = [];
	const session = new ShareSession({
		link: { wsURL: 'ws://x', key, shareID: ID },
		name: 'Ann',
		onEvent: (e) => events.push(e),
		socket: () => {
			const s = new FakeSocket();
			sockets.push(s);
			return s;
		},
		backoff: () => 0
	});
	const bridge = new Channel(key, cid, TO_GUEST);
	// Reads what the guest sealed, as the bridge would.
	const fromBridge = new Channel(key, cid, TO_GUEST);
	const seal = (pt: Uint8Array) => {
		const f = bridge.seal(pt);
		const out = new Uint8Array(1 + f.length);
		out[0] = ENV_FRAME;
		out.set(f, 1);
		return out.buffer;
	};
	const init = () => {
		const f = bridge.seal(encodeApp({ t: 'init' }));
		const out = new Uint8Array(1 + 16 + f.length);
		out[0] = ENV_INIT;
		out.set(cid, 1);
		out.set(f, 17);
		return out.buffer;
	};
	return { session, events, sockets, seal, init, fromBridge };
}

describe('session', () => {
	test('answers init with hello, then delivers welcome, output and mode', () => {
		const { session, events, sockets, seal, init, fromBridge } = setup();
		session.connect();
		const ws = sockets[0];
		ws.receive(JSON.stringify({ type: 'joined', guest: 1 }));
		ws.receive(init());
		expect(ws.sent).toHaveLength(1);
		const hello = fromBridge.open((ws.sent[0] as Uint8Array).subarray(1));
		expect(hello[0]).toBe(MSG_CONTROL);
		expect(JSON.parse(new TextDecoder().decode(hello.subarray(1)))).toMatchObject({
			t: 'hello',
			name: 'Ann'
		});

		ws.receive(
			seal(encodeApp({ t: 'welcome', mode: 'view', cols: 90, rows: 30, title: 'claude' }))
		);
		ws.receive(seal(new Uint8Array([MSG_OUTPUT, 104, 105])));
		ws.receive(seal(encodeApp({ t: 'mode', mode: 'control' })));
		expect(events.map((e) => e.type)).toEqual(['connecting', 'welcome', 'output', 'mode']);

		session.type('ls\r');
		const input = fromBridge.open((ws.sent[1] as Uint8Array).subarray(1));
		expect(input[0]).toBe(MSG_INPUT);
		expect(new TextDecoder().decode(input.subarray(1))).toBe('ls\r');
	});

	test('a wrong key is reported as a bad link and does not reconnect', () => {
		const { session, events, sockets } = setup();
		session.connect();
		const bogus = new Uint8Array(1 + 16 + 40);
		bogus[0] = ENV_INIT;
		sockets[0].receive(bogus.buffer);
		expect(events.at(-1)).toMatchObject({ type: 'bad_link' });
		expect(sockets).toHaveLength(1);
	});

	test('reconnects after a dropped connection, but not after the share ends', async () => {
		const { session, events, sockets } = setup();
		session.connect();
		sockets[0].close(1006);
		await new Promise((r) => setTimeout(r, 5));
		expect(sockets).toHaveLength(2);
		sockets[1].receive(JSON.stringify({ type: 'ended', reason: 'stopped' }));
		expect(events.at(-1)).toMatchObject({ type: 'ended', reason: 'stopped' });
		await new Promise((r) => setTimeout(r, 5));
		expect(sockets).toHaveLength(2);
	});

	test('a missing share ends without retrying', async () => {
		const { session, events, sockets } = setup();
		session.connect();
		sockets[0].close(4004);
		await new Promise((r) => setTimeout(r, 5));
		expect(events.at(-1)).toMatchObject({ type: 'ended', reason: 'not_found' });
		expect(sockets).toHaveLength(1);
	});
});
