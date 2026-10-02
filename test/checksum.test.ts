import { test } from 'node:test';
import assert from 'node:assert/strict';
import { rowChecksum, xorChecksum, ZERO_CHECKSUM, canonicalize } from '../src/rowHash.ts';
import { ChecksumIndex } from '../src/checksumIndex.ts';
import { TableStore } from '../src/store.ts';

test('行校验和对字段顺序不敏感，对字段值敏感', () => {
  const a = rowChecksum({ id: 1, name: 'x', balance: 9.99 });
  const b = rowChecksum({ balance: 9.99, id: 1, name: 'x' });
  const c = rowChecksum({ id: 1, name: 'x', balance: 10.0 });
  assert.equal(a, b);
  assert.notEqual(a, c);
  assert.match(a, /^[0-9a-f]{32}$/);
});

test('canonicalize 输出稳定', () => {
  assert.equal(canonicalize({ id: 7, b: 2, a: 1 }), 'a=1|b=2|id=7');
});

test('XOR 校验和满足自逆与交换律', () => {
  const x = rowChecksum({ id: 1, v: 'a' });
  const y = rowChecksum({ id: 2, v: 'b' });
  assert.equal(xorChecksum(x, x), ZERO_CHECKSUM);
  assert.equal(xorChecksum(x, y), xorChecksum(y, x));
  assert.equal(xorChecksum(xorChecksum(x, y), y), x);
});

test('增量维护的滚动 XOR 与全量重算结果一致', () => {
  const store = new TableStore('db');
  const index = new ChecksumIndex('side', { partitionSize: 64, bucketSize: 8 });
  index.track(store);

  for (let id = 0; id < 500; id++) store.upsert({ id, v: `row-${id}` });
  for (let id = 0; id < 500; id += 3) store.upsert({ id, v: `updated-${id}` });
  for (let id = 1; id < 500; id += 7) store.delete(id);

  // 全量重算：把每行校验和按分区异或，与索引的增量结果比对
  const expected = new Map<number, string>();
  for (const [id, sum] of store.recomputeChecksums()) {
    const pid = Math.floor(id / 64);
    expected.set(pid, xorChecksum(expected.get(pid) ?? ZERO_CHECKSUM, sum));
  }
  assert.deepEqual(index.digest(), expected);
  assert.equal(index.rowCount, store.size);
});

test('删除全部行后分区摘要归零（空分区被回收）', () => {
  const index = new ChecksumIndex('side', { partitionSize: 16, bucketSize: 4 });
  index.applyUpsert(1, rowChecksum({ id: 1, v: 1 }));
  index.applyUpsert(2, rowChecksum({ id: 2, v: 2 }));
  assert.equal(index.digest().size, 1);
  index.applyDelete(1);
  index.applyDelete(2);
  assert.equal(index.digest().size, 0);
  assert.equal(index.rowCount, 0);
});

test('重复 upsert 同一行不产生漂移（幂等）', () => {
  const index = new ChecksumIndex('side');
  const sum = rowChecksum({ id: 42, v: 'stable' });
  index.applyUpsert(42, sum);
  const before = index.digest().get(0);
  index.applyUpsert(42, sum);
  index.applyUpsert(42, sum);
  assert.equal(index.digest().get(0), before);
  assert.equal(index.rowCount, 1);
});
