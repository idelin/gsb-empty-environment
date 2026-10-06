// Locates a TypeScript compiler and runs it. In a normal environment
// `npm install` provides typescript; here we also probe known caches.
const { spawnSync } = require('child_process');
const path = require('path');

const candidates = [];
try { candidates.push(require.resolve('typescript/bin/tsc')); } catch (e) {}
candidates.push(
  '/Users/delin/.config/yarn/global/node_modules/typescript/bin/tsc',
  '/usr/local/lib/node_modules/typescript/bin/tsc'
);

const tsc = candidates.find((p) => {
  try { require('fs').accessSync(p); return true; } catch (e) { return false; }
});

if (!tsc) {
  console.error('No TypeScript compiler found. Run `npm install` first.');
  process.exit(1);
}

const res = spawnSync(process.execPath, [tsc, '-p', path.join(__dirname, '..', 'tsconfig.json')], {
  stdio: 'inherit',
});
process.exit(res.status === null ? 1 : res.status);
