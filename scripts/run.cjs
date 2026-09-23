#!/usr/bin/env node
'use strict';

const { spawnSync } = require('node:child_process');
const fs = require('node:fs');

const args = process.argv.slice(2);
const candidates = [
  process.env.NODE_BIN,
  '/opt/homebrew/bin/node',
  '/usr/local/bin/node',
  process.execPath
].filter(Boolean);

function majorVersion(file) {
  const result = spawnSync(file, ['--version'], { encoding: 'utf8' });
  if (result.status !== 0) return 0;
  const match = /v(\d+)\.(\d+)/.exec(result.stdout.trim());
  if (!match) return 0;
  return Number(match[1]) + Number(match[2]) / 100;
}

const home = process.env.HOME;
if (home) {
  const nvmRoot = `${home}/.nvm/versions/node`;
  if (fs.existsSync(nvmRoot)) {
    for (const entry of fs.readdirSync(nvmRoot).sort().reverse()) {
      candidates.push(`${nvmRoot}/${entry}/bin/node`);
    }
  }
}

const node = candidates.find((file) => majorVersion(file) >= 22.06);
if (!node) {
  console.error('Node.js >= 22.6 is required for native TypeScript execution.');
  process.exit(1);
}

const result = spawnSync(node, args, { stdio: 'inherit' });
process.exit(result.status ?? 1);
