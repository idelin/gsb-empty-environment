import { mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import { stripTypes } from './strip-types.mjs';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const out = join(root, '.build');
const emitOnly = process.argv.includes('--emit-only');

rmSync(out, { recursive: true, force: true });
mkdirSync(join(out, 'src'), { recursive: true });
mkdirSync(join(out, 'test'), { recursive: true });
writeFileSync(join(out, 'package.json'), JSON.stringify({ type: 'module' }) + '\n');

function emit(dirName) {
  const dir = join(root, dirName);
  for (const f of readdirSync(dir)) {
    if (!f.endsWith('.ts')) continue;
    const js = stripTypes(readFileSync(join(dir, f), 'utf8'));
    writeFileSync(join(out, dirName, f.replace(/\.ts$/, '.js')), js);
  }
}

emit('src');
emit('test');
console.log('transpiled src/ and test/ into .build/');

if (emitOnly) process.exit(0);

const unit = spawnSync('node', [join(out, 'test', 'diff.test.js')], { stdio: 'inherit' });
if (unit.status !== 0) {
  console.error('unit tests failed');
  process.exit(1);
}

// CLI smoke tests against the emitted build.
const cli = join(out, 'src', 'cli.js');
const fixtureEq = {
  options: { observable: ['go'] },
  left: [{ action: 'go', result: 1 }],
  right: [{ action: 'log' }, { action: 'go', result: 1 }],
};
const fixtureDiv = {
  options: { observable: ['go'] },
  left: [{ action: 'go', result: 1 }],
  right: [{ action: 'go', result: 2 }],
};
writeFileSync(join(out, 'fixture-eq.json'), JSON.stringify(fixtureEq));
writeFileSync(join(out, 'fixture-div.json'), JSON.stringify(fixtureDiv));

function runCli(args, input) {
  return spawnSync('node', [cli].concat(args), { encoding: 'utf8', input: input });
}

let cliFailures = 0;
function check(cond, msg) {
  if (!cond) {
    cliFailures++;
    console.error('CLI FAIL - ' + msg);
  } else {
    console.log('ok   - ' + msg);
  }
}

const r1 = runCli([join(out, 'fixture-eq.json')]);
check(r1.status === 0, 'cli exits 0 for equal traces');
check(JSON.parse(r1.stdout).equal === true, 'cli reports equal: true');

const r2 = runCli([join(out, 'fixture-div.json')]);
check(r2.status === 1, 'cli exits 1 for divergent traces');
check(JSON.parse(r2.stdout).divergence.kind === 'value-mismatch', 'cli reports value-mismatch');

const r3 = runCli([], JSON.stringify(fixtureDiv));
check(r3.status === 1, 'cli reads from stdin when no file is given');

if (cliFailures > 0) process.exit(1);
console.log('all tests passed');
