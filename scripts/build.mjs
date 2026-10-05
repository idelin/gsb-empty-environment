#!/usr/bin/env node
/**
 * Build TypeScript sources to plain JS for Node using the locally available
 * Bun transpiler (no network / no npm install needed). Output:
 *   dist/cli.js            - the CLI
 *   dist-test/*.test.js    - bundled test executables
 */
import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

const root = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..');

function findBun() {
  const candidates = [
    process.env.BUN_BIN,
    path.join(os.homedir(), '.bun', 'bin', 'bun'),
    '/usr/local/bin/bun',
    'bun',
  ].filter(Boolean);
  for (const c of candidates) {
    try {
      execFileSync(c, ['--version'], { stdio: 'pipe' });
      return c;
    } catch {
      /* try next */
    }
  }
  throw new Error('bun not found; set BUN_BIN to the bun executable');
}

const bun = findBun();

function build(entry, outFlag, outPath) {
  execFileSync(
    bun,
    ['build', entry, '--target=node', '--format=cjs', outFlag, outPath],
    { cwd: root, stdio: ['ignore', 'pipe', 'inherit'] },
  );
}

fs.rmSync(path.join(root, 'dist'), { recursive: true, force: true });
fs.rmSync(path.join(root, 'dist-test'), { recursive: true, force: true });

build('src/cli.ts', '--outfile', 'dist/cli.js');

const tests = fs
  .readdirSync(path.join(root, 'test'))
  .filter((f) => f.endsWith('.test.ts'));
for (const t of tests) {
  build(path.join('test', t), '--outfile', path.join('dist-test', t.replace(/\.ts$/, '.js')));
}

console.log('built dist/cli.js and ' + tests.length + ' test bundles');
