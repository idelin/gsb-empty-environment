import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { LsmEngine } from '../src/engine.ts';

const TENANTS = ['tenant_A', 'tenant_B', 'tenant_C', 'tenant_D', 'tenant_E'];
const ORDERS_PER_TENANT = 60_000; // 5 * 60_000 = 300_000 order records

function makeValue(key: string): string {
  return `{"key":"${key}","payload":"${'x'.repeat(64)}"}`;
}

/**
 * Acceptance boundary: hundreds of thousands of multi-tenant records;
 * PrefixSeek('tenant_A:order_') must skip all preceding blocks, stream the
 * tenant's orders zero-copy, and stop the moment the next tenant appears.
 */
function buildEngine(): { engine: LsmEngine; dir: string } {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'lsm-prefix-'));
  const engine = new LsmEngine(dir, 4096);
  // Interleave tenants and record kinds, then flush once into one SSTable.
  for (let i = 0; i < ORDERS_PER_TENANT; i++) {
    const id = String(i).padStart(6, '0');
    for (const tenant of TENANTS) {
      engine.put(`${tenant}:order_${id}`, makeValue(`${tenant}:order_${id}`));
    }
  }
  for (const tenant of TENANTS) {
    engine.put(`${tenant}:profile`, `profile-of-${tenant}`);
    engine.put(`${tenant}:session_0001`, `session-of-${tenant}`);
  }
  engine.flush();
  return { engine, dir };
}

test('PrefixSeek streams exactly the tenant prefix and truncates at the boundary', () => {
  const { engine } = buildEngine();
  const statsBefore = engine.stats();
  assert.equal(statsBefore.tables, 1);
  const totalBlocks = statsBefore.totalBlocks;
  assert.ok(totalBlocks > 1000, `expected a multi-thousand-block table, got ${totalBlocks}`);

  engine.resetStats();
  const started = process.hrtime.bigint();
  const it = engine.prefixSeek('tenant_A:order_');

  let count = 0;
  let firstKey = '';
  let lastKey = '';
  const keyBuffers = new Set<object>();
  while (it.valid()) {
    const key = it.key();
    const value = it.value();
    if (count === 0) {
      firstKey = key.toString();
    } else {
      assert.ok(key.toString() > lastKey, 'keys must be emitted in order');
    }
    keyBuffers.add(key.buffer); // zero-copy: keys are views into block buffers
    assert.ok(key.toString().startsWith('tenant_A:order_'));
    assert.equal(value.toString(), makeValue(key.toString()));
    lastKey = key.toString();
    count++;
    it.next();
  }
  const elapsedMs = Number(process.hrtime.bigint() - started) / 1e6;

  assert.equal(count, ORDERS_PER_TENANT);
  assert.equal(firstKey, 'tenant_A:order_000000');
  assert.equal(lastKey, `tenant_A:order_${String(ORDERS_PER_TENANT - 1).padStart(6, '0')}`);

  const stats = engine.stats();
  // Boundary truncation: the scan reads only the blocks holding
  // tenant_A:order_* (plus at most one boundary block), never the ~80% of
  // the table that belongs to other tenants.
  const sampleKey = 'tenant_A:order_000000';
  const entryBytes = 1 + sampleKey.length + 1 + makeValue(sampleKey).length + 4;
  const expectedBlocks = Math.ceil((ORDERS_PER_TENANT * entryBytes) / 4096);
  assert.ok(
    stats.dataBlocksRead <= expectedBlocks + 2,
    `read ${stats.dataBlocksRead} blocks, expected about ${expectedBlocks}`,
  );
  assert.ok(
    stats.dataBlocksRead < totalBlocks / 4,
    `must skip most of the table: read ${stats.dataBlocksRead} of ${totalBlocks} blocks`,
  );
  // Zero-copy: 60k emitted keys are views into only the handful of block
  // buffers actually read (one ArrayBuffer per data block), never copied.
  assert.ok(
    keyBuffers.size >= stats.dataBlocksRead - 1 && keyBuffers.size <= stats.dataBlocksRead,
    `expected ~${stats.dataBlocksRead} distinct key buffers, got ${keyBuffers.size}`,
  );
  console.log(
    `    scanned ${count} keys in ${elapsedMs.toFixed(1)}ms, ` +
      `blocks read ${stats.dataBlocksRead}/${totalBlocks}`,
  );

  // Same shape for a tenant in the middle of the keyspace: the engine must
  // skip every preceding tenant's blocks, not read through them.
  engine.resetStats();
  const mid = engine.prefixSeek('tenant_D:order_');
  let midCount = 0;
  let midFirst = '';
  while (mid.valid()) {
    if (midCount === 0) midFirst = mid.key().toString();
    midCount++;
    mid.next();
  }
  const midStats = engine.stats();
  assert.equal(midCount, ORDERS_PER_TENANT);
  assert.equal(midFirst, 'tenant_A:order_000000'.replace('tenant_A', 'tenant_D'));
  assert.ok(
    midStats.dataBlocksRead <= expectedBlocks + 2,
    `tenant_D scan read ${midStats.dataBlocksRead} blocks, expected about ${expectedBlocks} ` +
      `(preceding tenants' blocks must be skipped via the index)`,
  );
  engine.close();
});

test('PrefixSeek on user:1001: returns only that user\'s sub-attributes', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'lsm-user-'));
  const engine = new LsmEngine(dir, 1024);
  for (let u = 1000; u <= 1002; u++) {
    engine.put(`user:${u}:email`, `u${u}@example.com`);
    engine.put(`user:${u}:name`, `name-${u}`);
    engine.put(`user:${u}:tags:001`, 'tag-a');
    engine.put(`user:${u}:tags:002`, 'tag-b');
  }
  engine.flush();

  const it = engine.prefixSeek('user:1001:');
  const got: string[] = [];
  while (it.valid()) {
    got.push(`${it.key().toString()}=${it.value().toString()}`);
    it.next();
  }
  assert.deepEqual(got, [
    'user:1001:email=u1001@example.com',
    'user:1001:name=name-1001',
    'user:1001:tags:001=tag-a',
    'user:1001:tags:002=tag-b',
  ]);
  engine.close();
});

test('PrefixSeek edge cases: missing prefix, prefix past the end, empty match', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'lsm-edge-'));
  const engine = new LsmEngine(dir, 1024);
  for (let i = 0; i < 100; i++) engine.put(`k${String(i).padStart(3, '0')}`, `v${i}`);
  engine.flush();

  engine.resetStats();
  assert.equal(engine.prefixSeek('missing:').valid(), false);
  assert.equal(engine.prefixSeek('zzz').valid(), false);
  // A miss between existing keys must not scan the whole table.
  assert.ok(engine.stats().dataBlocksRead <= 2);
  engine.close();
});
