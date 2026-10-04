import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const CLI = new URL('../src/cli.ts', import.meta.url).pathname;

function run(args: string[]): { status: number; stdout: string; stderr: string } {
  const r = spawnSync(process.execPath, [CLI, ...args], { encoding: 'utf8' });
  return { status: r.status ?? -1, stdout: r.stdout, stderr: r.stderr };
}

function write(dir: string, name: string, data: unknown): string {
  const p = join(dir, name);
  writeFileSync(p, JSON.stringify(data));
  return p;
}

const rec = (fields: unknown[]): unknown => ({ kind: 'record', fields });
const fld = (id: number, type: unknown, opts: Record<string, unknown> = {}): unknown => ({
  id,
  type,
  required: true,
  ...opts,
});
const int = (min: string, max: string): unknown => ({ kind: 'int64', min, max });

test('CLI check: compatible (exit 0)', () => {
  const dir = mkdtempSync(join(tmpdir(), 'schema-cli-'));
  const oldP = write(dir, 'old.json', { version: 1, root: rec([fld(1, int('0', '9'), { name: 'a' })]) });
  const newP = write(dir, 'new.json', { version: 2, root: rec([fld(1, int('0', '9'), { name: 'b' })]) });
  const r = run(['check', oldP, newP]);
  assert.equal(r.status, 0, r.stderr);
  assert.deepEqual(JSON.parse(r.stdout), { result: 'compatible' });
});

test('CLI check: incompatible (exit 1) with counterexample', () => {
  const dir = mkdtempSync(join(tmpdir(), 'schema-cli-'));
  const oldP = write(dir, 'old.json', { version: 1, root: rec([fld(1, int('0', '9'))]) });
  const newP = write(dir, 'new.json', { version: 2, root: rec([fld(1, int('0', '5'))]) });
  const r = run(['check', oldP, newP]);
  assert.equal(r.status, 1);
  const out = JSON.parse(r.stdout);
  assert.equal(out.result, 'incompatible');
  assert.deepEqual(out.counterexample, { '1': 6 });
});

test('CLI check: unsupported (exit 2)', () => {
  const dir = mkdtempSync(join(tmpdir(), 'schema-cli-'));
  const oldP = write(dir, 'old.json', { version: 1, root: rec([fld(1, { kind: 'string' })]) });
  const newP = write(dir, 'new.json', { version: 2, root: rec([fld(1, { kind: 'string', maxLength: 1 })]) });
  const r = run(['check', oldP, newP]);
  assert.equal(r.status, 2);
  assert.equal(JSON.parse(r.stdout).result, 'unsupported');
});

test('CLI convert: success with unknown region (exit 0)', () => {
  const dir = mkdtempSync(join(tmpdir(), 'schema-cli-'));
  const oldP = write(dir, 'old.json', { version: 1, root: rec([fld(1, int('0', '9'))]) });
  const newP = write(dir, 'new.json', { version: 2, root: rec([fld(1, int('0', '9'), { name: 'renamed' })]) });
  const msgP = write(dir, 'msg.json', { '1': 4, '77': 'kept' });
  const r = run(['convert', oldP, newP, msgP]);
  assert.equal(r.status, 0, r.stderr);
  assert.deepEqual(JSON.parse(r.stdout), { ok: true, data: { '1': 4 }, unknown: { '$/77': 'kept' } });
});

test('CLI convert: atomic failure (exit 1, no partial record)', () => {
  const dir = mkdtempSync(join(tmpdir(), 'schema-cli-'));
  const oldP = write(dir, 'old.json', { version: 1, root: rec([fld(1, int('0', '9'))]) });
  const newP = write(dir, 'new.json', { version: 2, root: rec([fld(1, int('0', '5'))]) });
  const msgP = write(dir, 'msg.json', { '1': 9 });
  const r = run(['convert', oldP, newP, msgP]);
  assert.equal(r.status, 1);
  const out = JSON.parse(r.stdout);
  assert.equal(out.ok, false);
  assert.ok(!('data' in out));
});

test('CLI verify: exhaustive consistency (exit 0)', () => {
  const dir = mkdtempSync(join(tmpdir(), 'schema-cli-'));
  const oldP = write(dir, 'old.json', { version: 1, root: rec([fld(1, int('0', '3'))]) });
  const newP = write(dir, 'new.json', { version: 2, root: rec([fld(1, int('0', '2'))]) });
  const r = run(['verify', oldP, newP]);
  assert.equal(r.status, 0, r.stderr);
  const out = JSON.parse(r.stdout);
  assert.equal(out.consistent, true);
  assert.equal(out.exhaustive, true);
});

test('CLI validate: lint a schema', () => {
  const dir = mkdtempSync(join(tmpdir(), 'schema-cli-'));
  const good = write(dir, 'good.json', { version: 1, root: rec([fld(1, int('0', '9'))]) });
  assert.equal(run(['validate', good]).status, 0);
  const bad = write(dir, 'bad.json', { version: 1, root: rec([fld(1, { kind: 'int64', min: '0' })]) });
  assert.equal(run(['validate', bad]).status, 2);
});

test('CLI usage error (exit 3)', () => {
  assert.equal(run(['check']).status, 3);
  assert.equal(run(['nonsense']).status, 3);
});
