import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const cli = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'src', 'cli.ts');

function run(args: string[]): { code: number; out: string } {
  try {
    const out = execFileSync(process.execPath, [cli, ...args], { encoding: 'utf8' });
    return { code: 0, out };
  } catch (err) {
    const e = err as { status: number; stdout: string; stderr: string };
    return { code: e.status, out: (e.stdout ?? '') + (e.stderr ?? '') };
  }
}

test('cli: equivalent expressions exit 0', () => {
  const r = run(['--width', '8', '(x + y) - y', 'x']);
  assert.equal(r.code, 0);
  assert.match(r.out, /equal/);
});

test('cli: non-equivalent expressions exit 1 with smallest counterexample', () => {
  const r = run(['--width', '8', 'x & y', 'x | y']);
  assert.equal(r.code, 1);
  assert.match(r.out, /not-equal/);
  assert.match(r.out, /x=0/);
  assert.match(r.out, /y=1/);
});

test('cli: unknown on exhausted budget exits 2', () => {
  const r = run(['--width', '8', '--engine', 'symbolic', '--budget-props', '0', 'x + y', 'y + x']);
  assert.equal(r.code, 2);
  assert.match(r.out, /unknown/);
});

test('cli: parse error exits 3', () => {
  const r = run(['--width', '8', 'x +', 'x']);
  assert.equal(r.code, 3);
});
