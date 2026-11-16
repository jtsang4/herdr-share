import { describe, expect, test } from 'vitest';
import { AuthError, Channel, decodeKey, TO_BRIDGE, TO_GUEST } from './crypto.ts';

const hex = (b: Uint8Array) => Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
const fromHex = (s: string) => new Uint8Array(s.match(/../g)!.map((h) => parseInt(h, 16)));

describe('e2ee', () => {
	const key = new Uint8Array(32).map((_, i) => i);
	const cid = new Uint8Array(16).map((_, i) => 0xa0 + i);

	// The same vector as TestKnownVector in internal/e2ee/e2ee_test.go.
	const VECTOR = '0000000000000001000102030405060708090a0b2f6b4370a728c2d292f1ec0e09f2c9d9a40e';

	test('matches the Go implementation', () => {
		const bridge = new Channel(key, cid, TO_GUEST, () => new Uint8Array(12).map((_, i) => i));
		const sealed = bridge.seal(new TextEncoder().encode('hi'));
		expect(hex(sealed)).toBe(VECTOR);
		const guest = new Channel(key, cid, TO_BRIDGE);
		expect(new TextDecoder().decode(guest.open(fromHex(VECTOR)))).toBe('hi');
	});

	test('round trips both ways and rejects replay and reflection', () => {
		const bridge = new Channel(key, cid, TO_GUEST);
		const guest = new Channel(key, cid, TO_BRIDGE);
		const f = guest.seal(new Uint8Array([1, 2, 3]));
		expect(Array.from(bridge.open(f))).toEqual([1, 2, 3]);
		expect(() => bridge.open(f)).toThrow(AuthError);
		const g = guest.seal(new Uint8Array([4]));
		expect(() => guest.open(g)).toThrow(AuthError);
	});

	test('rejects tampering and other connections', () => {
		const bridge = new Channel(key, cid, TO_GUEST);
		const f = bridge.seal(new Uint8Array([9, 9, 9]));
		const other = new Channel(key, new Uint8Array(16), TO_BRIDGE);
		expect(() => other.open(f)).toThrow(AuthError);
		const guest = new Channel(key, cid, TO_BRIDGE);
		const bad = f.slice();
		bad[bad.length - 1] ^= 1;
		expect(() => guest.open(bad)).toThrow(AuthError);
	});

	test('decodes link keys', () => {
		const k = decodeKey('AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8');
		expect(hex(k)).toBe(hex(key));
		expect(() => decodeKey('abc')).toThrow();
	});
});
