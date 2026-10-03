#!/usr/bin/env node
/** Re-exec a TypeScript file with a Node that supports type stripping. */
import { spawnSync } from 'node:child_process';
import { existsSync, readdirSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

function supportsTs([major, minor]) {
  return major > 22 || (major === 22 && minor >= 6);
}
function versionOf(bin) {
  const out = spawnSync(bin, ['--version'], { encoding: 'utf8' }).stdout ?? '';
  const m = /v(\d+)\.(\d+)/.exec(out);
  return m ? [Number(m[1]), Number(m[2])] : null;
}
function findNewerNode() {
  const nvmDir = join(homedir(), '.nvm', 'versions', 'node');
  if (!existsSync(nvmDir)) return null;
  for (const dir of readdirSync(nvmDir).sort().reverse()) {
    const bin = join(nvmDir, dir, 'bin', 'node');
    const ver = existsSync(bin) && versionOf(bin);
    if (ver && supportsTs(ver)) return bin;
  }
  return null;
}

const file = process.argv[2];
if (!file) {
  console.error('usage: node scripts/run.mjs <file.ts>');
  process.exit(1);
}
let nodeBin = process.execPath;
let ver = versionOf(nodeBin);
if (!supportsTs(ver)) {
  nodeBin = findNewerNode();
  if (!nodeBin) {
    console.error(`Node >= 22.6 required (current: ${process.versions.node})`);
    process.exit(1);
  }
  ver = versionOf(nodeBin);
}
const needsFlag = ver[0] === 22 || (ver[0] === 23 && ver[1] < 6);
const flags = needsFlag ? ['--experimental-strip-types'] : [];
const result = spawnSync(nodeBin, [...flags, file], { stdio: 'inherit' });
process.exit(result.status ?? 1);
