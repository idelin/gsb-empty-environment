import { test, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, appendFileSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { LSMEngine } from '../src/engine.ts';

let dir: string;

beforeEach(() => {
  dir = mkdtempSync(join(tmpdir(), 'lsm-cf-'));
});

afterEach(() => {
  rmSync(dir, { recursive: true, force: true });
});

test('create CF_Default and CF_Metadata in one LSM instance', () => {
  const engine = LSMEngine.open(dir);
  engine.createColumnFamily('CF_Default');
  engine.createColumnFamily('CF_Metadata');
  assert.deepEqual(engine.listColumnFamilies().sort(), [
    'CF_Default',
    'CF_Metadata',
  ]);
  assert.throws(() => engine.createColumnFamily('CF_Default'), /exists/);
});

test('column families are logically isolated', () => {
  const engine = LSMEngine.open(dir);
  const cfDefault = engine.createColumnFamily('CF_Default');
  const cfMeta = engine.createColumnFamily('CF_Metadata');

  engine.put(cfDefault, 'user:1', 'alice');
  engine.put(cfMeta, 'table:users:version', '7');

  // CF_Metadata only sees its own data.
  assert.equal(engine.get(cfMeta, 'table:users:version'), '7');
  assert.equal(engine.get(cfMeta, 'user:1'), undefined);
  // And vice versa.
  assert.equal(engine.get(cfDefault, 'user:1'), 'alice');
  assert.equal(engine.get(cfDefault, 'table:users:version'), undefined);
});

test('single atomic transaction writes to both column families', () => {
  const engine = LSMEngine.open(dir);
  const cfDefault = engine.createColumnFamily('CF_Default');
  const cfMeta = engine.createColumnFamily('CF_Metadata');

  engine.writeBatch([
    { cf: cfDefault, key: 'user:2', value: 'bob' },
    { cf: cfMeta, key: 'table:users:last_id', value: '2' },
  ]);

  assert.equal(engine.get(cfDefault, 'user:2'), 'bob');
  assert.equal(engine.get(cfMeta, 'table:users:last_id'), '2');
  // Isolation still holds for batch-written keys.
  assert.equal(engine.get(cfMeta, 'user:2'), undefined);
  assert.equal(engine.get(cfDefault, 'table:users:last_id'), undefined);
});

test('per-column-family snapshot is point-in-time and independent', () => {
  const engine = LSMEngine.open(dir);
  const cfDefault = engine.createColumnFamily('CF_Default');
  const cfMeta = engine.createColumnFamily('CF_Metadata');

  engine.put(cfDefault, 'k', 'v1');
  const snap = engine.snapshot(cfDefault);
  engine.put(cfDefault, 'k', 'v2');
  engine.put(cfMeta, 'm', 'x');

  assert.equal(engine.get(cfDefault, 'k', { snapshot: snap }), 'v1');
  assert.equal(engine.get(cfDefault, 'k'), 'v2');
  // Snapshot of CF_Default cannot be used against CF_Metadata.
  assert.throws(() => engine.get(cfMeta, 'm', { snapshot: snap }), /different column family/);
});

test('flush writes per-CF SSTables without touching other CFs', () => {
  const engine = LSMEngine.open(dir);
  const cfDefault = engine.createColumnFamily('CF_Default');
  const cfMeta = engine.createColumnFamily('CF_Metadata');

  engine.put(cfDefault, 'a', '1');
  engine.put(cfMeta, 'b', '2');
  engine.flush(cfDefault);

  assert.equal(engine.sstableCount(cfDefault), 1);
  assert.equal(engine.sstableCount(cfMeta), 0);
  assert.equal(engine.get(cfDefault, 'a'), '1'); // served from SSTable
  assert.equal(engine.get(cfMeta, 'b'), '2'); // still served from MemTable
});

test('recovery from shared WAL aligns both column families atomically', () => {
  let engine = LSMEngine.open(dir);
  const cfDefault = engine.createColumnFamily('CF_Default');
  const cfMeta = engine.createColumnFamily('CF_Metadata');

  engine.put(cfDefault, 'plain', 'before-batch');
  engine.writeBatch([
    { cf: cfDefault, key: 'user:3', value: 'carol' },
    { cf: cfMeta, key: 'table:users:last_id', value: '3' },
  ]);
  engine.flush(cfMeta); // exercise SSTable reload path too

  engine = LSMEngine.open(dir);
  const rDefault = { name: 'CF_Default' };
  const rMeta = { name: 'CF_Metadata' };
  assert.equal(engine.get(rDefault, 'plain'), 'before-batch');
  assert.equal(engine.get(rDefault, 'user:3'), 'carol');
  assert.equal(engine.get(rMeta, 'table:users:last_id'), '3');
  assert.equal(engine.get(rMeta, 'user:3'), undefined);
});

test('crash mid-batch: uncommitted txn is discarded for ALL column families', () => {
  const engine = LSMEngine.open(dir);
  const cfDefault = engine.createColumnFamily('CF_Default');
  const cfMeta = engine.createColumnFamily('CF_Metadata');
  engine.put(cfDefault, 'stable', 'yes');
  engine.put(cfMeta, 'meta:stable', 'yes');

  // Simulate a crash: batch records hit the shared WAL but the commit
  // marker never does (process died between the two appends).
  appendFileSync(
    join(dir, 'wal.log'),
    JSON.stringify({ t: 'data', txn: 99, cf: 'CF_Default', key: 'user:4', value: 'dave', seq: 100 }) + '\n' +
    JSON.stringify({ t: 'data', txn: 99, cf: 'CF_Metadata', key: 'table:users:last_id', value: '4', seq: 101 }) + '\n',
  );

  const recovered = LSMEngine.open(dir);
  assert.equal(recovered.get({ name: 'CF_Default' }, 'stable'), 'yes');
  assert.equal(recovered.get({ name: 'CF_Metadata' }, 'meta:stable'), 'yes');
  assert.equal(recovered.get({ name: 'CF_Default' }, 'user:4'), undefined);
  assert.equal(recovered.get({ name: 'CF_Metadata' }, 'table:users:last_id'), undefined);

  // Both CFs stay aligned at the last committed state, and new writes
  // continue with a consistent sequence.
  recovered.writeBatch([
    { cf: { name: 'CF_Default' }, key: 'user:5', value: 'erin' },
    { cf: { name: 'CF_Metadata' }, key: 'table:users:last_id', value: '5' },
  ]);
  assert.equal(recovered.get({ name: 'CF_Default' }, 'user:5'), 'erin');
  assert.equal(recovered.get({ name: 'CF_Metadata' }, 'table:users:last_id'), '5');
});

test('shared WAL is a single physical file containing both CFs', () => {
  const engine = LSMEngine.open(dir);
  const cfDefault = engine.createColumnFamily('CF_Default');
  const cfMeta = engine.createColumnFamily('CF_Metadata');
  engine.writeBatch([
    { cf: cfDefault, key: 'x', value: '1' },
    { cf: cfMeta, key: 'y', value: '2' },
  ]);

  const lines = readFileSync(join(dir, 'wal.log'), 'utf8').trim().split('\n');
  const records = lines.map((l) => JSON.parse(l));
  const cfsInWal = new Set(records.filter((r) => r.t === 'data').map((r) => r.cf));
  assert.deepEqual([...cfsInWal].sort(), ['CF_Default', 'CF_Metadata']);
  assert.equal(records.at(-1).t, 'commit');
});
