/**
 * Demo: prefix range scan over a multi-tenant LSM store.
 *
 *   node examples/demo.ts        (requires Node >= 22.6, works out of the
 *                                 box on Node >= 23.6)
 */
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { LsmEngine } from '../src/engine.ts';

const TENANTS = ['tenant_A', 'tenant_B', 'tenant_C', 'tenant_D', 'tenant_E'];
const ORDERS_PER_TENANT = 60_000; // 300_000 order records total
const BLOCK_SIZE = 4096;

function orderValue(tenant: string, id: string): string {
  return JSON.stringify({ tenant, order: id, amount: 123.45, payload: 'x'.repeat(64) });
}

const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'lsm-demo-'));
const engine = new LsmEngine(dir, BLOCK_SIZE);

console.log(`\n== building LSM store in ${dir} ==`);
const t0 = process.hrtime.bigint();
for (let i = 0; i < ORDERS_PER_TENANT; i++) {
  const id = String(i).padStart(6, '0');
  for (const tenant of TENANTS) {
    engine.put(`${tenant}:order_${id}`, orderValue(tenant, id));
  }
}
for (const tenant of TENANTS) {
  engine.put(`${tenant}:profile`, `profile-of-${tenant}`);
  engine.put(`${tenant}:session_0001`, `session-of-${tenant}`);
}
const info = engine.flush()!;
const t1 = process.hrtime.bigint();
console.log(
  `flushed ${info.entries.toLocaleString()} records -> ` +
    `${info.blocks.toLocaleString()} data blocks, ${(info.bytes / 1024 / 1024).toFixed(1)} MiB ` +
    `in ${Number(t1 - t0) / 1e6 | 0}ms`,
);

// --- PrefixSeek('tenant_A:order_') -------------------------------------
console.log(`\n== PrefixSeek('tenant_A:order_') ==`);
engine.resetStats();
const t2 = process.hrtime.bigint();
const it = engine.prefixSeek('tenant_A:order_');
let count = 0;
let first = '';
let last = '';
let checksum = 0;
while (it.valid()) {
  const key = it.key(); // zero-copy view into the current data block
  const value = it.value();
  if (count === 0) first = key.toString();
  last = key.toString();
  checksum += value.length;
  count++;
  it.next();
}
const t3 = process.hrtime.bigint();
const stats = engine.stats();
console.log(`entries returned : ${count.toLocaleString()}`);
console.log(`first key        : ${first}`);
console.log(`last key         : ${last}`);
console.log(`scan time        : ${(Number(t3 - t2) / 1e6).toFixed(1)}ms`);
console.log(
  `blocks read      : ${stats.dataBlocksRead} of ${stats.totalBlocks} ` +
    `(${((stats.dataBlocksRead / stats.totalBlocks) * 100).toFixed(1)}% of the table)`,
);
console.log(`bytes read       : ${(stats.dataBytesRead / 1024 / 1024).toFixed(1)} MiB`);
console.log(`value checksum   : ${checksum.toLocaleString()} bytes`);

// --- Same scan for a tenant in the middle of the keyspace ---------------
console.log(`\n== PrefixSeek('tenant_D:order_') (middle of keyspace) ==`);
engine.resetStats();
const t4 = process.hrtime.bigint();
const mid = engine.prefixSeek('tenant_D:order_');
let midCount = 0;
let midFirst = '';
let midLast = '';
while (mid.valid()) {
  if (midCount === 0) midFirst = mid.key().toString();
  midLast = mid.key().toString();
  midCount++;
  mid.next();
}
const t5 = process.hrtime.bigint();
const midStats = engine.stats();
console.log(`entries returned : ${midCount.toLocaleString()} (${midFirst} .. ${midLast})`);
console.log(`scan time        : ${(Number(t5 - t4) / 1e6).toFixed(1)}ms`);
console.log(
  `blocks read      : ${midStats.dataBlocksRead} of ${midStats.totalBlocks} ` +
    `-- tenants A..C skipped via index binary search, scan stops at tenant_E`,
);

// --- Boundary truncation demo ------------------------------------------
console.log(`\n== boundary truncation: PrefixSeek('tenant_A:profile') ==`);
engine.resetStats();
const p = engine.prefixSeek('tenant_A:profile');
while (p.valid()) {
  console.log(`${p.key().toString()} = ${p.value().toString()}`);
  p.next();
}
console.log(`blocks read      : ${engine.stats().dataBlocksRead} (single block, stopped before tenant_A:session*)`);

// --- user:1001: style sub-attribute scan --------------------------------
console.log(`\n== PrefixSeek('user:1001:') on a small user store ==`);
const userDir = fs.mkdtempSync(path.join(os.tmpdir(), 'lsm-demo-users-'));
const users = new LsmEngine(userDir, 1024);
for (let u = 1000; u <= 1002; u++) {
  users.put(`user:${u}:email`, `user${u}@example.com`);
  users.put(`user:${u}:name`, `name-${u}`);
  users.put(`user:${u}:tags:vip`, '1');
}
users.flush();
const u = users.prefixSeek('user:1001:');
while (u.valid()) {
  console.log(`${u.key().toString()} = ${u.value().toString()}`);
  u.next();
}
users.close();

engine.close();
fs.rmSync(dir, { recursive: true, force: true });
fs.rmSync(userDir, { recursive: true, force: true });
console.log('\ndone.');
