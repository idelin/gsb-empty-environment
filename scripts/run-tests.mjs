#!/usr/bin/env node
/**
 * Zero-dependency test launcher.
 * TypeScript type-stripping requires Node >= 22.6 (>= 23.6 for flagless).
 * If the current node is too old, try to locate a newer one under common
 * version-manager paths and re-exec with it.
 */
import { spawnSync } from 'node:child_process';
import { existsSync, readdirSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

const REQUIRED = [22, 6];

function supportsTs(version) {
  const [major, minor] = version;
  return major > REQUIRED[0] || (major === REQUIRED[0] && minor >= REQUIRED[1]);
}

function findNewerNode() {
  const candidates = [];
  const nvmDir = join(homedir(), '.nvm', 'versions', 'node');
  if (existsSync(nvmDir)) {
    for (const dir of readdirSync(nvmDir)) {
      const bin = join(nvmDir, dir, 'bin', 'node');
      if (existsSync(bin)) candidates.push(bin);
    }
  }
  for (const bin of candidates) {
    const out = spawnSync(bin, ['--version'], { encoding: 'utf8' });
    const match = /v(\d+)\.(\d+)/.exec(out.stdout ?? '');
    if (match && supportsTs([Number(match[1]), Number(match[2])])) return bin;
  }
  return null;
}

const current = process.versions.node.split('.').map(Number);
let nodeBin = process.execPath;
if (!supportsTs(current)) {
  const newer = findNewerNode();
  if (!newer) {
    console.error(
      `Node >= ${REQUIRED.join('.')} is required for TypeScript type stripping ` +
        `(current: ${process.versions.node}). No newer node found.`,
    );
    process.exit(1);
  }
  console.log(`current node ${process.versions.node} too old; re-exec with ${newer}`);
  nodeBin = newer;
}

const root = new URL('..', import.meta.url).pathname;
// Node 22.6-23.5 require the explicit flag; 23.6+ strips types by default.
const nodeVer = spawnSync(nodeBin, ['--version'], { encoding: 'utf8' }).stdout.trim();
const [maj, min] = nodeVer.slice(1).split('.').map(Number);
const needsFlag = maj === 22 || (maj === 23 && min < 6);
const typeFlags = needsFlag ? ['--experimental-strip-types'] : [];

const testFiles = readdirSync(join(root, 'tests'))
  .filter((f) => f.endsWith('.test.ts'))
  .map((f) => join('tests', f));
const result = spawnSync(nodeBin, [...typeFlags, '--test', ...testFiles], {
  stdio: 'inherit',
  cwd: root,
});
process.exit(result.status ?? 1);
