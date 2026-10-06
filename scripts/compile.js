// Offline-friendly compiler launcher: prefer a local typescript install,
// fall back to typescript packages already present on this machine.
const { existsSync } = require('fs');
const { spawnSync } = require('child_process');
const { join } = require('path');

const candidates = [
  join(__dirname, '..', 'node_modules', 'typescript', 'lib', 'tsc.js'),
  '/Users/delin/git/onlinehub-web/node_modules/typescript/lib/tsc.js',
  '/Users/delin/git/ChatGPT-Next-Web/node_modules/typescript/lib/tsc.js',
];

const tsc = candidates.find((p) => existsSync(p));
if (!tsc) {
  console.error('No TypeScript compiler found. Install typescript or add its tsc.js path to scripts/compile.js');
  process.exit(1);
}

const res = spawnSync(process.execPath, [tsc, '-p', join(__dirname, '..', 'tsconfig.json')], {
  stdio: 'inherit',
});
process.exit(res.status === null ? 1 : res.status);
