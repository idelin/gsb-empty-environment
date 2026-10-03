/**
 * 运行样例：构建 10000 条记录的 B+ 树，执行 Scan(2500, 7500)。
 * 运行方式：node --experimental-strip-types demo.ts   （Node >= 22.6）
 */
import { BufferPool, DiskManager } from './src/buffer_pool.ts';
import { BPlusTree, LEAF_CAPACITY } from './src/btree.ts';

const pool = new BufferPool(new DiskManager(), 8);
const tree = new BPlusTree(pool);

console.log(`插入 10000 条记录 (key: 0..9999, value: key * 10)，叶子容量 ${LEAF_CAPACITY}...`);
for (let i = 0; i < 10_000; i++) {
  tree.insert(i, i * 10);
}
console.log(`建树完成：根页 #${tree.rootId}，缓冲池驻留 ${pool.residentPages}/${pool.capacity} 页，pinned = ${pool.pinnedCount}`);

console.log('\n执行 Scan(2500, 7500) ...');
const fetchBefore = pool.fetchCount;
const diskReadBefore = pool.diskReadCount;

const results: number[] = [];
for (const entry of tree.scan(2500, 7500)) {
  results.push(entry.key);
}

console.log(`扫描结果：共 ${results.length} 条记录`);
console.log(`  前 5 条: ${results.slice(0, 5).join(', ')}`);
console.log(`  后 5 条: ${results.slice(-5).join(', ')}`);
console.log(`  扫描期间 fetchPage ${pool.fetchCount - fetchBefore} 次（含跨页），磁盘读 ${pool.diskReadCount - diskReadBefore} 次`);
console.log(`  扫描结束后 BufferPool pinned = ${pool.pinnedCount}（0 表示无页面泄漏）`);

const sortedUnique = results.every((k, i) => i === 0 || k > results[i - 1]);
console.log(`\n校验：5000 条 = ${results.length === 5000}，严格递增不重复 = ${sortedUnique}`);
