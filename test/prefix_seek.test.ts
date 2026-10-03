import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { SSTableBuilder, SSTableReader } from '../src/sstable.ts';
import { prefixUpperBound } from '../src/prefix_iterator.ts';

const TENANTS = ['tenant_A', 'tenant_B', 'tenant_C', 'tenant_D', 'tenant_E'];
const AUDIT_PER_TENANT = 20_000; // sorts before "order_" ('a' < 'o')
const ORDERS_PER_TENANT = 60_000;
const SESSIONS_PER_TENANT = 2_000; // sorts after "order_" ('s' > 'o')
const TOTAL_RECORDS =
  TENANTS.length * (AUDIT_PER_TENANT + ORDERS_PER_TENANT + SESSIONS_PER_TENANT + 1);

const pad = (n: number) => String(n).padStart(6, '0');

function orderKey(tenant: string, n: number): string {
  return `${tenant}:order_${pad(n)}`;
}

/** Build the acceptance-test SSTable: hundreds of thousands of multi-tenant records. */
function buildAcceptanceTable(): { file: string; reader: SSTableReader } {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'prefix-seek-'));
  const file = path.join(dir, 'tenants.sst');
  const builder = new SSTableBuilder(file, 4096);
  for (const tenant of TENANTS) {
    for (let i = 0; i < AUDIT_PER_TENANT; i++) {
      builder.add(`${tenant}:audit_${pad(i)}`, `audit-entry-${i}`);
    }
    for (let i = 0; i < ORDERS_PER_TENANT; i++) {
      builder.add(orderKey(tenant, i), `{"orderId":${i},"tenant":"${tenant}"}`);
    }
    builder.add(`${tenant}:profile`, `{"name":"${tenant}"}`);
    for (let i = 0; i < SESSIONS_PER_TENANT; i++) {
      builder.add(`${tenant}:session_${pad(i)}`, `session-${i}`);
    }
  }
  const written = builder.finish();
  assert.equal(written, TOTAL_RECORDS);
  return { file, reader: SSTableReader.open(file) };
}

test('prefixUpperBound computes the prefix successor', () => {
  assert.equal(prefixUpperBound(Buffer.from('tenant_A:order_'))!.toString(), 'tenant_A:order`');
  assert.deepEqual(prefixUpperBound(Buffer.from([0x61, 0x62, 0xff])), Buffer.from([0x61, 0x63]));
  assert.equal(prefixUpperBound(Buffer.from([0xff, 0xff])), null);
});

test('acceptance: PrefixSeek skips blocks, streams zero-copy, truncates at boundary', () => {
  const { reader } = buildAcceptanceTable();
  const prefix = 'tenant_A:order_';

  const started = performance.now();
  const iter = reader.prefixSeek(prefix);
  let count = 0;
  let lastKey = '';
  let firstKey = '';
  let sharedBacking = true;
  let prevValueBuf: ArrayBufferLike | null = null;
  for (const entry of iter) {
    const key = entry.key.toString();
    if (count === 0) firstKey = key;
    assert.ok(key.startsWith(prefix), `out-of-prefix key emitted: ${key}`);
    assert.ok(key > lastKey, 'keys must be emitted in increasing order');
    lastKey = key;
    // zero-copy: every value is a view onto the same file-image ArrayBuffer
    if (prevValueBuf !== null && entry.value.buffer !== prevValueBuf) sharedBacking = false;
    prevValueBuf = entry.value.buffer;
    if (count === 7) {
      assert.equal(entry.value.toString(), `{"orderId":7,"tenant":"tenant_A"}`);
    }
    count++;
  }
  const elapsed = performance.now() - started;

  // 1. exact result set: every order of tenant_A, nothing else
  assert.equal(count, ORDERS_PER_TENANT);
  assert.equal(firstKey, orderKey('tenant_A', 0));
  assert.equal(lastKey, orderKey('tenant_A', ORDERS_PER_TENANT - 1));

  // 2. zero-copy confirmed: all values share one backing store
  assert.ok(sharedBacking, 'values must be zero-copy views into the file image');

  // 3. fast positioning: preceding audit blocks and all later tenants'
  //    blocks are never read. tenant_A's orders span far fewer blocks than
  //    the whole table.
  const scanned = iter.blocksScanned;
  assert.equal(reader.stats.dataBlocksRead, scanned);
  assert.ok(
    scanned <= Math.ceil(ORDERS_PER_TENANT / 70) + 2,
    `scan read ${scanned} blocks, expected only the blocks holding the prefix`,
  );
  assert.ok(
    scanned < reader.numBlocks / TENANTS.length,
    `scan read ${scanned}/${reader.numBlocks} blocks: preceding/foreign blocks not skipped`,
  );

  // 4. boundary truncation: the scan stops at tenant_A:profile / tenant_B
  //    and never touches the remaining tenants' blocks.
  const tenantABlocksUpperBound = Math.ceil(
    (AUDIT_PER_TENANT + ORDERS_PER_TENANT + SESSIONS_PER_TENANT + 1) / 70,
  ) + 2;
  assert.ok(scanned <= tenantABlocksUpperBound);

  console.log(
    `    [acceptance] ${TOTAL_RECORDS.toLocaleString()} records, ` +
      `${reader.numBlocks} blocks total; scan emitted ${count.toLocaleString()} keys ` +
      `reading only ${scanned} blocks in ${elapsed.toFixed(1)}ms`,
  );
});

test('acceptance: PrefixSeek on a middle tenant skips all preceding blocks', () => {
  const { reader } = buildAcceptanceTable();
  const iter = reader.prefixSeek('tenant_C:order_');
  let count = 0;
  for (const entry of iter) {
    assert.ok(entry.key.toString().startsWith('tenant_C:order_'));
    count++;
  }
  assert.equal(count, ORDERS_PER_TENANT);
  // tenant_A and tenant_B occupy roughly 2/5 of the table; none of their
  // blocks may be read.
  assert.ok(
    iter.blocksScanned < reader.numBlocks / TENANTS.length,
    `read ${iter.blocksScanned}/${reader.numBlocks} blocks`,
  );
});

test('prefix seek edge cases', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'prefix-edge-'));
  const file = path.join(dir, 'edge.sst');
  const builder = new SSTableBuilder(file, 256);
  const keys = [
    'user:1001:email', 'user:1001:name', 'user:1001:phone',
    'user:1002:email', 'user:1002:name',
    'user:1010:cart',
  ];
  keys.forEach((k, i) => builder.add(k, `v${i}`));
  builder.finish();
  const reader = SSTableReader.open(file);

  // exact prefix in the middle of the table
  // (zero-copy contract: key views are valid only until the next next() call,
  //  so stringify while iterating)
  const got: string[] = [];
  for (const e of reader.prefixSeek('user:1001:')) got.push(e.key.toString());
  assert.deepEqual(got, ['user:1001:email', 'user:1001:name', 'user:1001:phone']);

  // prefix matching a single full key
  const one: string[] = [];
  for (const e of reader.prefixSeek('user:1010:cart')) one.push(e.key.toString());
  assert.deepEqual(one, ['user:1010:cart']);

  // prefix with no matches (falls between existing keys)
  const countOf = (p: string) => {
    let n = 0;
    for (const _ of reader.prefixSeek(p)) n++;
    return n;
  };
  assert.equal(countOf('user:1009:'), 0);
  // prefix beyond the end of the table
  assert.equal(countOf('zzz'), 0);
  // prefix shorter than any key
  assert.equal(countOf('user:'), keys.length);
});
