/**
 * 运行样例：Tombstone 的完整生命周期。
 * 运行方式：node examples/demo.ts   （Node.js >= 23.6，原生运行 TypeScript）
 */
import { LSMTree } from '../src/lsm-tree.ts';
import { isTombstone } from '../src/tombstone.ts';

const tree = new LSMTree({ memtableFlushThreshold: 4, maxTablesPerLevel: 2, maxLevels: 4 });

function show(label: string): void {
  const stats = tree.stats();
  const layers = [
    `MemTable(${stats.memtable.entries}条/${stats.memtable.tombstones}墓碑)`,
    ...stats.levels.map((l, i) => `L${i}(${l.tables}表/${l.entries}条/${l.tombstones}墓碑)`),
  ].join('  ');
  console.log(`\n[${label}]`);
  console.log(`  get('K1') => ${JSON.stringify(tree.get('K1')) ?? 'undefined (NotFound)'}`);
  console.log(`  物理布局: ${layers}`);
  const trace = [...tree.allEntries()].filter((r) => r.entry.key === 'K1');
  if (trace.length > 0) {
    console.log(
      `  K1 物理痕迹: ${trace
        .map((r) => `${r.location === 'memtable' ? 'MemTable' : 'L' + r.location}:${isTombstone(r.entry.value) ? 'TOMBSTONE' : r.entry.value}`)
        .join(', ')}`,
    );
  } else {
    console.log('  K1 物理痕迹: 无（已彻底擦除）');
  }
}

console.log('=== LSM Tombstone 生命周期演示 ===');

tree.put('K1', 'V1');
tree.flush();
show('1. put K1=V1 并 Flush');

tree.delete('K1');
show('2. delete K1（Tombstone 在 MemTable，查询立即 NotFound）');

tree.flush();
show('3. Tombstone 落盘到 L0');

for (let round = 1; round <= 3; round++) {
  for (let i = 0; i < 8; i++) tree.put(`filler-r${round}-${i}`, `x${round}-${i}`);
  tree.flush();
  show(`4.${round} 第 ${round} 轮 Compaction（Tombstone 逐级下沉）`);
}

tree.compactAll();
show('5. 抵达 Bottom-most 层：Tombstone 被物理擦除，空间释放');

console.log(`\n最终校验: 全库 Tombstone 数 = ${tree.countTombstones()}`);
console.log(`Range Scan 样例: ${JSON.stringify(tree.scan('filler-r1-0', 'filler-r1-3'))}`);
