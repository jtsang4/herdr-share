// End-to-end encryption, mirroring internal/e2ee in Go.
//
// Sealed frame: seq u64 BE | nonce [12] | AES-256-GCM(ciphertext || tag)
// Additional data: "herdr-share/1" | dir u8 | cid [16] | seq u64 BE
//
// @noble/ciphers is used instead of WebCrypto so the page also works over
// plain http:// on private networks, where browsers hide crypto.subtle.
import { gcm } from '@noble/ciphers/aes.js';

export const KEY_SIZE = 32;
export const CID_SIZE = 16;
const SEQ = 8;
const NONCE = 12;
const TAG = 16;
export const OVERHEAD = SEQ + NONCE + TAG;

export const TO_GUEST = 0x42; // 'B': sealed by the bridge
export const TO_BRIDGE = 0x47; // 'G': sealed by a guest

const LABEL = new TextEncoder().encode('herdr-share/1');

export class AuthError extends Error {
	constructor(message = 'frame failed authentication') {
		super(message);
		this.name = 'AuthError';
	}
}

export function decodeKey(s: string): Uint8Array {
	const b64 = s.replace(/-/g, '+').replace(/_/g, '/');
	const padded = b64 + '='.repeat((4 - (b64.length % 4)) % 4);
	let bin: string;
	try {
		bin = atob(padded);
	} catch {
		throw new Error('invalid key encoding');
	}
	if (bin.length !== KEY_SIZE) throw new Error('invalid key length');
	const out = new Uint8Array(KEY_SIZE);
	for (let i = 0; i < KEY_SIZE; i++) out[i] = bin.charCodeAt(i);
	return out;
}

function additionalData(dir: number, cid: Uint8Array, seq: bigint): Uint8Array {
	const ad = new Uint8Array(LABEL.length + 1 + CID_SIZE + SEQ);
	ad.set(LABEL, 0);
	ad[LABEL.length] = dir;
	ad.set(cid, LABEL.length + 1);
	new DataView(ad.buffer).setBigUint64(LABEL.length + 1 + CID_SIZE, seq);
	return ad;
}

function randomNonce(): Uint8Array {
	const n = new Uint8Array(NONCE);
	crypto.getRandomValues(n);
	return n;
}

/** One connection's channel: seals as `send`, opens the other direction. */
export class Channel {
	private sendSeq = 0n;
	private recvSeq = 0n;
	private readonly recv: number;

	constructor(
		private readonly key: Uint8Array,
		readonly cid: Uint8Array,
		private readonly send: number,
		private readonly nonce: () => Uint8Array = randomNonce
	) {
		if (key.length !== KEY_SIZE) throw new Error('bad key size');
		if (cid.length !== CID_SIZE) throw new Error('bad cid size');
		this.recv = send === TO_BRIDGE ? TO_GUEST : TO_BRIDGE;
	}

	seal(plaintext: Uint8Array): Uint8Array {
		this.sendSeq += 1n;
		const seq = this.sendSeq;
		const nonce = this.nonce();
		const ct = gcm(this.key, nonce, additionalData(this.send, this.cid, seq)).encrypt(plaintext);
		const out = new Uint8Array(SEQ + NONCE + ct.length);
		new DataView(out.buffer).setBigUint64(0, seq);
		out.set(nonce, SEQ);
		out.set(ct, SEQ + NONCE);
		return out;
	}

	open(frame: Uint8Array): Uint8Array {
		if (frame.length < OVERHEAD) throw new AuthError();
		const seq = new DataView(frame.buffer, frame.byteOffset, frame.byteLength).getBigUint64(0);
		const nonce = frame.subarray(SEQ, SEQ + NONCE);
		let pt: Uint8Array;
		try {
			pt = gcm(this.key, nonce, additionalData(this.recv, this.cid, seq)).decrypt(
				frame.subarray(SEQ + NONCE)
			);
		} catch {
			throw new AuthError();
		}
		if (seq <= this.recvSeq) throw new AuthError('frame replayed or out of order');
		this.recvSeq = seq;
		return pt;
	}
}
