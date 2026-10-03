import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { LSMEngine } from '../src/engine.ts';

function tmpDir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'lsm-engine-'));
}

test('engine merges memtable and sstables for prefix scans', () => {
  const engine = new LSMEngine({ dir: tmpDir(), memtableFlushBytes: 8 * 1024 });
  for (let i = 0; i < 2000; i++) {
    engine.put(`user:1001:attr_${String(i).padStart(4, '0')}`, `value-${i}`);
  }
  engine.flush(); // force one sstable
  for (let i = 2000; i < 2100; i++) {
    engine.put(`user:1001:attr_${String(i).padStart(4, '0')}`, `value-${i}`);
  }
  // overwrite a flushed key in the memtable: newest must win
  engine.put('user:1001:attr_0007', 'value-OVERWRITTEN');

  const keys: string[] = [];
  const values = new Map<string, string>();
  for (const { key, value } of engine.prefixSeek('user:1001:')) {
    keys.push(key.toString());
    values.set(key.toString(), value.toString());
  }
  assert.equal(keys.length, 2100);
  assert.deepEqual([...keys].sort(), keys); // merged output stays ordered
  assert.equal(values.get('user:1001:attr_0007'), 'value-OVERWRITTEN');
  assert.equal(values.get('user:1001:attr_2099'), 'value-2099');
  assert.equal(engine.get('user:1001:attr_0007')?.toString(), 'value-OVERWRITTEN');
  engine.close();
});

test('engine auto-flushes and scans across multiple sstables', () => {
  const engine = new LSMEngine({ dir: tmpDir(), memtableFlushBytes: 16 * 1024 });
  for (const tenant of ['tenant_A', 'tenant_B']) {
    for (let i = 0; i < 3000; i++) {
      engine.put(`${tenant}:order_${String(i).padStart(5, '0')}`, `order-${tenant}-${i}`);
    }
  }
  engine.flush();
  assert.ok(engine.sstables.length >= 2, 'expected multiple sstables');

  let count = 0;
  let last = '';
  for (const { key } of engine.prefixSeek('tenant_B:order_')) {
    const k = key.toString();
    assert.ok(k.startsWith('tenant_B:order_'));
    assert.ok(k > last);
    last = k;
    count++;
  }
  assert.equal(count, 3000);
  engine.close();
});
