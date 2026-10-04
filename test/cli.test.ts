import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const CLI = new URL('../src/cli.ts', import.meta.url).pathname;

const rec = (fields: unknown[]) => ({ kind: 'record', fields });
const fld = (id: number, name: string, required: boolean, type: unknown, extra: object = {}) => ({
  id, name, required, type, ...extra,
});
const int = (min: number, max: number) => ({ kind: 'int64', min, max });
const str = (maxLen: number) => ({ kind: 'string', maxLen });

function setup() {
  const dir = mkdtempSync(join(tmpdir(), 'schema-tool-'));
  const write = (name: string, v: unknown) => {
    writeFileSync(join(dir, name), JSON.stringify(v));
    return join(dir, name);
  };
  const run = (...args: string[]) => spawnSync(process.execPath, [CLI, ...args], { encoding: 'utf8' });
  return { write, run };
}

const oldS = rec([fld(1, 'n', true, int(0, 100)), fld(2, 's', false, str(5))]);
const newOk = rec([fld(1, 'n', true, int(0, 200)), fld(2, 's', false, str(8))]);
const newBad = rec([fld(1, 'n', true, int(0, 50))]);
const unsupported = rec([fld(1, 's', true, { kind: 'string' })]);

test('cli check: compatible / incompatible / unsupported exit codes', () => {
  const { write, run } = setup();
  const o = write('old.json', oldS);
  const ok = write('ok.json', newOk);
  const bad = write('bad.json', newBad);
  const uns = write('uns.json', unsupported);

  const r1 = run('check', o, ok);
  assert.equal(r1.status, 0);
  assert.equal(JSON.parse(r1.stdout).result, 'compatible');

  const r2 = run('check', o, bad);
  assert.equal(r2.status, 1);
  const parsed = JSON.parse(r2.stdout);
  assert.equal(parsed.result, 'incompatible');
  assert.equal(parsed.encoding, '{"1":51}');

  const r3 = run('check', o, uns);
  assert.equal(r3.status, 2);
  assert.equal(JSON.parse(r3.stdout).result, 'unsupported');
});

test('cli convert: success and atomic failure', () => {
  const { write, run } = setup();
  const o = write('old.json', oldS);
  const ok = write('ok.json', newOk);
  const msg = write('msg.json', { '1': 42, '2': 'hi', '77': { 'x': 1 } });
  const r1 = run('convert', o, ok, msg);
  assert.equal(r1.status, 0);
  const parsed = JSON.parse(r1.stdout);
  assert.deepEqual(parsed.record.fields, { '1': 42, '2': 'hi' });
  assert.deepEqual(parsed.record.unknown, { '77': { 'x': 1 } });

  const badMsg = write('badmsg.json', { '1': 42, '2': null });
  const r2 = run('convert', o, ok, badMsg);
  assert.equal(r2.status, 1);
  assert.equal(JSON.parse(r2.stdout).ok, false);
});

test('cli verify: exhaustive cross-check agrees', () => {
  const { write, run } = setup();
  const o = write('old.json', rec([fld(1, 'n', true, int(0, 3))]));
  const bad = write('bad.json', rec([fld(1, 'n', true, int(0, 2))]));
  const r = run('verify', o, bad);
  assert.equal(r.status, 0);
  assert.equal(JSON.parse(r.stdout).result, 'match');
});

test('cli usage error exits 2', () => {
  const { run } = setup();
  assert.equal(run().status, 2);
  assert.equal(run('check', 'only-one.json').status, 2);
});
