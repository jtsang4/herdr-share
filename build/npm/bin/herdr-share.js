#!/usr/bin/env node
import { spawn } from 'node:child_process';
import { existsSync, readFileSync, realpathSync } from 'node:fs';
import { dirname, join, sep } from 'node:path';

import { ensureBinary } from '../lib/launcher.js';

const { version } = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8'));

let bin;
try {
  bin = await ensureBinary(version);
} catch (err) {
  process.stderr.write(`herdr-share: ${err.message}\n`);
  process.exit(1);
}

// Tell the binary how it was started, so `mod install` can record a command
// that keeps working: `npx -y herdr-share@<version>` when run through npx,
// this launcher when installed globally.
const launcher = (() => {
  try {
    return realpathSync(process.argv[1]);
  } catch {
    return process.argv[1];
  }
})();
const env = { ...process.env, HERDR_SHARE_LAUNCHER: launcher, HERDR_SHARE_NODE: process.execPath };
if (launcher.includes(`${sep}_npx${sep}`)) {
  env.HERDR_SHARE_VIA_NPX = '1';
  const npx = join(dirname(process.execPath), process.platform === 'win32' ? 'npx.cmd' : 'npx');
  if (existsSync(npx)) env.HERDR_SHARE_NPX = npx;
}

const child = spawn(bin, process.argv.slice(2), { stdio: 'inherit', windowsHide: true, env });

for (const signal of ['SIGINT', 'SIGTERM', 'SIGHUP']) {
  process.on(signal, () => child.kill(signal));
}

child.on('error', (err) => {
  process.stderr.write(`herdr-share: failed to start ${bin}: ${err.message}\n`);
  process.exit(1);
});

child.on('exit', (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code ?? 1);
});
