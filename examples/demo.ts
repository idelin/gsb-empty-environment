/**
 * Demo: prefix range scan with index-block fast positioning.
 *
 *  [1] build a 410k-record multi-tenant SSTable
 *  [2] PrefixSeek('tenant_A:order_') — skip irrelevant blocks, zero-copy
 *      stream, stop at the tenant boundary
 *  [3] point lookup through the same index path
 *  [4] LSM engine: memtable + SSTable merged prefix scan
 *
 * Run:  node examples/demo.ts
 */
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { LSMEngine, SSTableBuilder, SSTableReader } from '../src/index.ts';

const TENANTS = ['tenant_A', 'tenant_B', 'tenant_C', 'tenant_D', 'tenant_E'];
const AUDIT_PER_TENANT = 20_000; // sorts before "order_"
const ORDERS_PER_TENANT = 60_000;
const SESSIONS_PER_TENANT = 2_000; // sorts after "order_"
const pad = (n: number) => String(n).padStart(6, '0');

const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'lsm-demo-'));
const tableFile = path.join(dir, 'tenants.sst');

console.log(`[1] building SSTable with multi-tenant records (dir: ${dir})`);
let t0 = performance.now();
const builder = new SSTableBuilder(tableFile, 4096);
let inserted = 0;
for (const tenant of TENANTS) {
  for (let i = 0; i < AUDIT_PER_TENANT; i++) {
    builder.add(`${tenant}:audit_${pad(i)}`, `audit-entry-${i}`);
    inserted++;
  }
  for (let i = 0; i < ORDERS_PER_TENANT; i++) {
    builder.add(`${tenant}:order_${pad(i)}`, `{"orderId":${i},"tenant":"${tenant}","amount":${(i % 997) + 1}}`);
    inserted++;
  }
  builder.add(`${tenant}:profile`, `{"name":"${tenant}"}`);
  inserted++;
  for (let i = 0; i < SESSIONS_PER_TENANT; i++) {
    builder.add(`${tenant}:session_${pad(i)}`, `session-${i}`);
    inserted++;
  }
}
builder.finish();
const reader = SSTableReader.open(tableFile);
console.log(`    inserted ${inserted.toLocaleString()} records in ${(performance.now() - t0).toFixed(0)}ms`);
console.log(`    table: ${reader.numBlocks.toLocaleString()} data blocks, ` +
  `${(fs.statSync(tableFile).size / 1024 / 1024).toFixed(1)} MiB`);

console.log(`\n[2] PrefixSeek('tenant_A:order_')`);
t0 = performance.now();
const iter = reader.prefixSeek('tenant_A:order_');
let count = 0;
for (const { key, value } of iter) {
  if (count < 3) console.log(`    ${key.toString()}  ->  ${value.toString()}`);
  count++;
}
const elapsed = performance.now() - t0;
console.log(`    ... (${count.toLocaleString()} keys total, zero-copy views into the file image)`);
console.log(`    data blocks read: ${iter.blocksScanned} of ${reader.numBlocks} ` +
  `(${(100 * iter.blocksScanned / reader.numBlocks).toFixed(1)}%) — preceding audit blocks and ` +
  `tenant_B..E blocks skipped via index-block binary search`);
console.log(`    scan stopped at the first key of tenant_A:profile / tenant_B range`);
console.log(`    elapsed: ${elapsed.toFixed(1)}ms (${Math.round(count / (elapsed / 1000)).toLocaleString()} keys/s)`);

console.log(`\n[3] point lookup via the same index path`);
console.log(`    get('tenant_A:order_000042') -> ${reader.get('tenant_A:order_000042')?.toString()}`);
console.log(`    get('tenant_Z:order_000042') -> ${reader.get('tenant_Z:order_000042')}`);

console.log(`\n[4] LSM engine: memtable + SSTables merged prefix scan`);
const engineDir = path.join(dir, 'engine');
const engine = new LSMEngine({ dir: engineDir, memtableFlushBytes: 256 * 1024 });
for (let i = 0; i < 30_000; i++) {
  engine.put(`user:1001:attr_${String(i).padStart(5, '0')}`, `value-${i}`);
}
engine.flush();
for (let i = 30_000; i < 31_000; i++) {
  engine.put(`user:1001:attr_${String(i).padStart(5, '0')}`, `value-${i}`);
}
engine.put('user:1001:attr_00007', 'value-OVERWRITTEN'); // memtable shadows flushed key
t0 = performance.now();
let merged = 0;
let overwritten = '';
for (const { key, value } of engine.prefixSeek('user:1001:')) {
  if (key.toString() === 'user:1001:attr_00007') overwritten = value.toString();
  merged++;
}
console.log(`    ${engine.sstables.length} SSTable(s) + memtable; merged ${merged.toLocaleString()} keys ` +
  `in ${(performance.now() - t0).toFixed(0)}ms`);
console.log(`    user:1001:attr_00007 -> ${overwritten} (newest write wins)`);

engine.close();
fs.rmSync(dir, { recursive: true, force: true });
