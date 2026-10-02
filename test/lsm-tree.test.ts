import assert from 'node:assert/strict';
import { test } from 'node:test';
import { LSMTree, type PhysicalEntry } from '../src/lsm-tree.ts';
import { isTombstone } from '../src/tombstone.ts';

/** 某个 Key 在存储介质上的全部物理痕迹（含 Tombstone）。 */
function physicalTrace(tree: LSMTree, key: string): PhysicalEntry[] {
  return [...tree.allEntries()].filter((record) => record.entry.key === key);
}

test('插入 K1=V1 后删除 K1：Point Get 立即返回 NotFound', () => {
  const tree = new LSMTree();
  tree.put('K1', 'V1');
  tree.delete('K1');
  assert.equal(tree.get('K1'), undefined);
});

test('Tombstone 屏蔽已落盘 SSTable 中的旧值', () => {
  const tree = new LSMTree();
  tree.put('K1', 'V1');
  tree.flush(); // V1 沉入 L0 SSTable
  tree.delete('K1'); // Tombstone 仅在 MemTable
  assert.equal(tree.get('K1'), undefined);
  tree.flush(); // Tombstone 也落盘
  assert.equal(tree.get('K1'), undefined);
});

test('Range Scan 过滤 Tombstone，且最新版本获胜', () => {
  const tree = new LSMTree();
  for (let i = 1; i <= 5; i++) tree.put(`K${i}`, `V${i}`);
  tree.flush();
  tree.delete('K2'); // 删除已落盘的 Key
  tree.delete('K5'); // 删除已落盘的 Key
  tree.put('K4', 'V4-new'); // 覆盖已落盘的 Key
  tree.delete('K9'); // 删除从未存在的 Key

  assert.deepEqual(tree.scan('K1', 'K5'), [
    { key: 'K1', value: 'V1' },
    { key: 'K3', value: 'V3' },
    { key: 'K4', value: 'V4-new' },
  ]);
  // 全量 Scan 结果一致
  assert.deepEqual(tree.scan(), [
    { key: 'K1', value: 'V1' },
    { key: 'K3', value: 'V3' },
    { key: 'K4', value: 'V4-new' },
  ]);
});

test('中间层 Compaction 保留 Tombstone（更深层仍有更老版本）', () => {
  const tree = new LSMTree({ maxLevels: 3 });
  tree.put('K1', 'V0');
  tree.compactAll(); // V0 沉入 Bottom-most 层 L2
  assert.deepEqual(
    physicalTrace(tree, 'K1').map((r) => [r.location, r.entry.value]),
    [[2, 'V0']],
  );

  tree.delete('K1');
  tree.flush(); // Tombstone 进入 L0
  tree.compactLevel(0); // L0 -> L1（非 Bottom-most，Tombstone 必须保留）

  const trace = physicalTrace(tree, 'K1');
  assert.equal(trace.length, 2);
  const tombstone = trace.find((r) => isTombstone(r.entry.value));
  const oldValue = trace.find((r) => r.entry.value === 'V0');
  assert.ok(tombstone && tombstone.location === 1, 'Tombstone 应位于 L1');
  assert.ok(oldValue && oldValue.location === 2, '旧值 V0 应仍位于 L2');
  assert.equal(tree.get('K1'), undefined, '读路径仍被 Tombstone 屏蔽');

  tree.compactAll(); // 下沉至 Bottom-most 层：物理擦除
  assert.deepEqual(physicalTrace(tree, 'K1'), []);
  assert.equal(tree.countTombstones(), 0);
});

test('验收场景：多轮 Compaction 后 Tombstone 痕迹被彻底擦除', () => {
  const tree = new LSMTree({ memtableFlushThreshold: 4, maxTablesPerLevel: 2, maxLevels: 4 });

  // 1. 插入 K1=V1 并落盘
  tree.put('K1', 'V1');
  tree.flush();
  assert.equal(tree.get('K1'), 'V1');

  // 2. 删除 K1：写入 Tombstone，查询立即返回 NotFound
  tree.delete('K1');
  assert.equal(tree.get('K1'), undefined);

  // 3. Tombstone 落盘，物理痕迹可见（V1 与 Tombstone 并存于不同表）
  tree.flush();
  assert.ok(tree.countTombstones() >= 1, 'Tombstone 已写入存储');
  assert.ok(physicalTrace(tree, 'K1').some((r) => isTombstone(r.entry.value)));

  // 4. 多轮写入驱动自动 Compaction，记录逐级下沉
  for (let i = 0; i < 64; i++) {
    tree.put(`filler-${String(i).padStart(3, '0')}`, `x${i}`);
  }
  tree.compactAll(); // 确保所有数据沉入 Bottom-most 层

  // 5. Tombstone 与其遮蔽的 V1 均被物理擦除，不留任何痕迹
  assert.equal(tree.get('K1'), undefined);
  assert.equal(tree.countTombstones(), 0);
  assert.deepEqual(physicalTrace(tree, 'K1'), []);
  assert.deepEqual(tree.stats().levels.map((l) => l.tombstones), [0, 0, 0, 0]);

  // 6. 其余数据完好无损
  assert.equal(tree.get('filler-007'), 'x7');
  assert.equal(tree.scan().length, 64);
});

test('删除后重新写入：最新版本获胜且幸存于全量 Compaction', () => {
  const tree = new LSMTree();
  tree.put('K1', 'V1');
  tree.delete('K1');
  tree.put('K1', 'V2');
  assert.equal(tree.get('K1'), 'V2');

  tree.flush();
  tree.compactAll();
  assert.equal(tree.get('K1'), 'V2');
  const trace = physicalTrace(tree, 'K1');
  assert.equal(trace.length, 1, '历史版本与 Tombstone 均已收敛');
  assert.equal(trace[0].entry.value, 'V2');
});

test('删除不存在的 Key：Tombstone 照常写入并在 Bottom-most 层被清理', () => {
  const tree = new LSMTree();
  tree.delete('ghost');
  assert.equal(tree.get('ghost'), undefined);
  tree.flush();
  assert.equal(tree.countTombstones(), 1);
  tree.compactAll();
  assert.equal(tree.countTombstones(), 0);
  assert.deepEqual(physicalTrace(tree, 'ghost'), []);
});

test('Range Scan 与 Point Get 在 Compaction 前后结果一致', () => {
  const tree = new LSMTree({ memtableFlushThreshold: 2, maxTablesPerLevel: 2 });
  tree.put('a', '1');
  tree.put('b', '2');
  tree.put('c', '3');
  tree.delete('b');
  tree.put('d', '4');
  tree.delete('a');

  const before = tree.scan();
  assert.equal(tree.get('a'), undefined);
  assert.equal(tree.get('b'), undefined);
  assert.equal(tree.get('c'), '3');
  assert.equal(tree.get('d'), '4');

  tree.compactAll();
  assert.deepEqual(tree.scan(), before, 'Compaction 不改变逻辑视图');
  assert.deepEqual(tree.scan(), [
    { key: 'c', value: '3' },
    { key: 'd', value: '4' },
  ]);
});
