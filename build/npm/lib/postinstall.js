// Downloads the native binary while the package is being installed, so the
// first run is instant and a global install in a root-owned folder works.
// Failures are not fatal: the launcher retries on first run.
import { readFileSync } from 'node:fs';

import { ensureBinary, isDevelopmentVersion } from './launcher.js';

const { version } = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8'));

if (!isDevelopmentVersion(version)) {
  try {
    await ensureBinary(version, { quiet: true });
  } catch (err) {
    process.stderr.write(`herdr-share: could not download the binary now (${err.message}); it will be fetched on first run\n`);
  }
}
