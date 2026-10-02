import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { LsmEngine } from '../src/engine.ts';

function tmpdir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'lsm-engine-'));
}

test('prefix seek merges memtable and multiple SSTables, newest wins', () => {
  const engine = new LsmEngine(tmpdir(), 256);

  // Batch 1 -> SSTable 1
  for (let i = 0; i < 50; i++) engine.put(`tenant_A:order_${String(i).padStart(3, '0')}`, `old-${i}`);
  engine.flush();
  // Batch 2 -> SSTable 2, overwriting some keys
  for (let i = 10; i < 30; i++) engine.put(`tenant_A:order_${String(i).padStart(3, '0')}`, `new-${i}`);
  engine.flush();
  // Memtable: one more overwrite plus a fresh key, and another tenant.
  engine.put('tenant_A:order_005', 'mem-5');
  engine.put('tenant_A:order_100', 'mem-100');
  engine.put('tenant_B:order_000', 'b-0');

  const it = engine.prefixSeek('tenant_A:order_');
  const got = new Map<string, string>();
  while (it.valid()) {
    got.set(it.key().toString(), it.value().toString());
    it.next();
  }

  assert.equal(got.size, 51); // 0..49 plus 100
  assert.equal(got.get('tenant_A:order_005'), 'mem-5'); // memtable wins
  assert.equal(got.get('tenant_A:order_010'), 'new-10'); // newer SSTable wins
  assert.equal(got.get('tenant_A:order_040'), 'old-40'); // untouched
  assert.equal(got.get('tenant_A:order_100'), 'mem-100');
  assert.ok(!got.has('tenant_B:order_000')); // boundary truncation
  engine.close();
});

test('prefix seek sees unflushed memtable data only via the memtable source', () => {
  const engine = new LsmEngine(tmpdir(), 256);
  engine.put('user:1001:email', 'a@b.c');
  const it = engine.prefixSeek('user:1001:');
  assert.equal(it.valid(), true);
  assert.equal(it.key().toString(), 'user:1001:email');
  it.next();
  assert.equal(it.valid(), false);
  engine.close();
});
