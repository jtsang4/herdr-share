// Resolves, downloads and verifies the native herdr-share binary.
//
// The npm package ships no binary. It downloads the release binary that
// matches this package's version into the package's own vendor/ folder (at
// install time through postinstall, else on first run), so the binary lives
// and goes with the package: in npx's cache when run through npx, in npm's
// global node_modules when installed with -g. Only when that folder is not
// writable does it fall back to the OS cache folder.

import { createHash } from 'node:crypto';
import { accessSync, chmodSync, constants, existsSync, mkdirSync, readFileSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

export const DEFAULT_DOWNLOAD_BASE = 'https://github.com/jtsang4/herdr-share/releases/download';

const OS_MAP = { darwin: 'darwin', linux: 'linux', win32: 'windows' };
const ARCH_MAP = { x64: 'amd64', arm64: 'arm64' };

/** The package's root folder. */
export const PACKAGE_ROOT = dirname(dirname(fileURLToPath(import.meta.url)));

/** Maps Node's platform/arch to Go's GOOS/GOARCH, or throws if unsupported. */
export function resolveTarget(platform = process.platform, arch = process.arch) {
  const os = OS_MAP[platform];
  const goarch = ARCH_MAP[arch];
  if (!os || !goarch) {
    throw new Error(`herdr-share does not provide a binary for ${platform}/${arch}`);
  }
  return { os, arch: goarch, exe: os === 'windows' ? '.exe' : '' };
}

/** Name of the raw binary release asset, matching .goreleaser.yaml. */
export function assetName(version, target) {
  return `herdr-share_${version}_${target.os}_${target.arch}${target.exe}`;
}

/** The OS cache folder, used only when the package folder is read-only. */
export function cacheDir(version, env = process.env, platform = process.platform) {
  let base;
  if (platform === 'win32') base = env.LOCALAPPDATA || join(homedir(), 'AppData', 'Local');
  else if (platform === 'darwin') base = join(homedir(), 'Library', 'Caches');
  else base = env.XDG_CACHE_HOME || join(homedir(), '.cache');
  return join(base, 'herdr-share', version);
}

/** Where the binary for `version` lives: the package's vendor/ folder, else the cache. */
export function binaryCandidates(version, { root = PACKAGE_ROOT, env = process.env, target = resolveTarget() } = {}) {
  const name = `herdr-share${target.exe}`;
  return [join(root, 'vendor', name), join(cacheDir(version, env), name)];
}

function writable(dir) {
  try {
    mkdirSync(dir, { recursive: true });
    accessSync(dir, constants.W_OK);
    return true;
  } catch {
    return false;
  }
}

/** Returns true for versions that have no published release (local builds). */
export function isDevelopmentVersion(v) {
  return !/^v?\d+\.\d+\.\d+/.test(String(v).trim()) || String(v).includes('development');
}

/** Extracts the sha256 for `name` from a goreleaser checksums.txt body. */
export function findChecksum(checksums, name) {
  for (const line of checksums.split('\n')) {
    const [sum, file] = line.trim().split(/\s+/);
    if (file === name) return sum.toLowerCase();
  }
  return null;
}

async function fetchOk(url) {
  const res = await fetch(url, { redirect: 'follow' });
  if (!res.ok) throw new Error(`download failed: ${url} (${res.status} ${res.statusText})`);
  return res;
}

// A stamp beside the binary records which version it is.
const stamp = (bin) => `${bin}.version`;

function hasVersion(bin, version) {
  try {
    return existsSync(bin) && readFileSync(stamp(bin), 'utf8').trim() === version;
  } catch {
    return false;
  }
}

/** Downloads, verifies and atomically installs the binary for `version` at `bin`. */
export async function download(version, bin, { env = process.env, target = resolveTarget() } = {}) {
  const base = (env.HERDR_SHARE_DOWNLOAD_BASE || DEFAULT_DOWNLOAD_BASE).replace(/\/+$/, '');
  const name = assetName(version, target);
  const dir = `${base}/v${version}`;

  const [checksums, binary] = await Promise.all([
    fetchOk(`${dir}/checksums.txt`).then((r) => r.text()),
    fetchOk(`${dir}/${name}`).then(async (r) => Buffer.from(await r.arrayBuffer()))
  ]);
  const expected = findChecksum(checksums, name);
  if (!expected) throw new Error(`no checksum for ${name} in checksums.txt`);
  const actual = createHash('sha256').update(binary).digest('hex');
  if (actual !== expected) {
    throw new Error(`checksum mismatch for ${name}: expected ${expected}, got ${actual}`);
  }

  mkdirSync(dirname(bin), { recursive: true });
  const tmp = `${bin}.download-${process.pid}`;
  writeFileSync(tmp, binary, { mode: 0o755 });
  chmodSync(tmp, 0o755);
  if (target.os === 'windows' && existsSync(bin)) {
    // A running .exe cannot be overwritten, but it can be renamed.
    const old = `${bin}.old-${Date.now()}`;
    renameSync(bin, old);
    try {
      rmSync(old, { force: true });
    } catch {
      // still running; left for a later cleanup
    }
  }
  renameSync(tmp, bin);
  writeFileSync(stamp(bin), version);
  return bin;
}

/**
 * Returns the path of the binary for `wanted`, downloading it into the package
 * (or, if that is read-only, the cache folder) when it is missing.
 */
export async function ensureBinary(wanted, { root = PACKAGE_ROOT, env = process.env, target = resolveTarget(), quiet = false } = {}) {
  const [vendored, cached] = binaryCandidates(wanted, { root, env, target });
  if (isDevelopmentVersion(wanted)) {
    for (const bin of [vendored, cached]) if (existsSync(bin)) return bin;
    throw new Error(`this is a development build of the herdr-share npm package; place a binary at ${vendored}`);
  }
  for (const bin of [vendored, cached]) if (hasVersion(bin, wanted)) return bin;
  const bin = writable(dirname(vendored)) ? vendored : cached;
  if (!quiet) process.stderr.write(`herdr-share: downloading v${wanted} for ${target.os}/${target.arch}\n`);
  return download(wanted, bin, { env, target });
}
