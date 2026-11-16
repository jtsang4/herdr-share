import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtempSync, readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';

import {
  assetName,
  binaryCandidates,
  ensureBinary,
  findChecksum,
  isDevelopmentVersion,
  resolveTarget
} from '../lib/launcher.js';

test('resolveTarget maps Node platforms to Go targets', () => {
  assert.deepEqual(resolveTarget('darwin', 'arm64'), { os: 'darwin', arch: 'arm64', exe: '' });
  assert.deepEqual(resolveTarget('linux', 'x64'), { os: 'linux', arch: 'amd64', exe: '' });
  assert.deepEqual(resolveTarget('win32', 'x64'), { os: 'windows', arch: 'amd64', exe: '.exe' });
  assert.throws(() => resolveTarget('aix', 'ppc64'));
});

test('assetName matches the goreleaser binary archive naming', () => {
  assert.equal(assetName('1.2.3', resolveTarget('linux', 'arm64')), 'herdr-share_1.2.3_linux_arm64');
  assert.equal(assetName('1.2.3', resolveTarget('win32', 'arm64')), 'herdr-share_1.2.3_windows_arm64.exe');
});

test('the binary lives in the package, the cache only as a fallback', () => {
  const [vendored, cached] = binaryCandidates('1.0.0', { root: '/pkg', env: { XDG_CACHE_HOME: '/c' }, target: resolveTarget('linux', 'x64') });
  assert.equal(vendored, join('/pkg', 'vendor', 'herdr-share'));
  assert.ok(cached.includes(join('herdr-share', '1.0.0')));
});

test('isDevelopmentVersion detects unreleased builds', () => {
  assert.equal(isDevelopmentVersion('0.0.0-development'), true);
  assert.equal(isDevelopmentVersion('dev'), true);
  assert.equal(isDevelopmentVersion('1.0.0'), false);
});

test('findChecksum reads goreleaser checksums.txt', () => {
  const body = 'abc123  herdr-share_1.0.0_linux_amd64\nDEF456  herdr-share_1.0.0_darwin_arm64\n';
  assert.equal(findChecksum(body, 'herdr-share_1.0.0_darwin_arm64'), 'def456');
  assert.equal(findChecksum(body, 'missing'), null);
});

// Serves a fake release and checks download, verification and reuse.
async function withRelease(files, fn) {
  const server = createServer((req, res) => {
    const body = files[req.url];
    if (body === undefined) {
      res.writeHead(404).end();
      return;
    }
    res.writeHead(200).end(body);
  });
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  try {
    await fn(`http://127.0.0.1:${server.address().port}`);
  } finally {
    server.close();
  }
}

test('ensureBinary downloads into the package, verifies it, and reuses it', async () => {
  const target = resolveTarget();
  const name = assetName('1.2.3', target);
  const binary = Buffer.from('#!/bin/sh\necho fake\n');
  const sum = createHash('sha256').update(binary).digest('hex');
  const root = mkdtempSync(join(tmpdir(), 'hs-npm-'));
  mkdirSync(join(root, 'vendor'));
  await withRelease({ '/v1.2.3/checksums.txt': `${sum}  ${name}\n`, [`/v1.2.3/${name}`]: binary }, async (base) => {
    const env = { HERDR_SHARE_DOWNLOAD_BASE: base };
    const bin = await ensureBinary('1.2.3', { root, env, target, quiet: true });
    assert.equal(bin, join(root, 'vendor', `herdr-share${target.exe}`));
    assert.deepEqual(readFileSync(bin), binary);
    // Second call: no download needed (the server would still answer, so
    // prove reuse by corrupting what it would serve).
    writeFileSync(bin, binary);
    assert.equal(await ensureBinary('1.2.3', { root, env: { HERDR_SHARE_DOWNLOAD_BASE: 'http://127.0.0.1:1' }, target, quiet: true }), bin);
  });
});

test('ensureBinary refuses a binary whose checksum does not match', async () => {
  const target = resolveTarget();
  const name = assetName('1.2.4', target);
  const root = mkdtempSync(join(tmpdir(), 'hs-npm-'));
  await withRelease({ '/v1.2.4/checksums.txt': `${'0'.repeat(64)}  ${name}\n`, [`/v1.2.4/${name}`]: 'tampered' }, async (base) => {
    await assert.rejects(ensureBinary('1.2.4', { root, env: { HERDR_SHARE_DOWNLOAD_BASE: base }, target, quiet: true }), /checksum mismatch/);
  });
});
