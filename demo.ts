import { SlottedPage } from './src/slotted-page.ts';

const fmt = (p: SlottedPage, label: string) => {
  const s = p.stats();
  console.log(`\n== ${label} ==`);
  console.log(
    `  存活记录 ${s.liveRecords} 条 | 槽位 ${s.slotCount} 个(墓碑 ${s.tombstones}) | ` +
      `连续空闲 ${s.freeBytes}B | 碎片 ${s.fragmentedBytes}B | ` +
      `利用率 ${(s.utilization * 100).toFixed(1)}%`,
  );
};

const page = new SlottedPage();
fmt(page, '空页 (4KB)');

// 1. 写入长短不一的记录
const texts = [
  'short',
  'medium length record '.repeat(8),
  'x'.repeat(1200),
  'tiny',
  '中文可变长文本记录 '.repeat(20),
  'y'.repeat(600),
  'last one',
];
const ids = texts.map((t) => page.insert(t));
fmt(page, `写入 ${ids.length} 条长短不一的记录`);

// 2. 删除中间记录，制造碎片
page.delete(ids[1]);
page.delete(ids[2]);
page.delete(ids[4]);
fmt(page, '删除中间 3 条记录（产生碎片）');

// 3. 碎片整理：存活数据向页尾对齐，空闲空间合并
page.compact();
fmt(page, 'compact() 碎片整理后');

// 4. 验证数据完整性
const survivors = [0, 3, 5, 6];
const ok = survivors.every((i) => page.get(ids[i])!.toString() === texts[i]);
console.log(`\n存活记录校验: ${ok ? '全部一致 ✓' : '不一致 ✗'}`);
console.log(`已删除记录读取: ${page.get(ids[1]) === null ? '返回 null ✓' : '异常 ✗'}`);

// 5. 整理后继续写入（复用墓碑槽位）
const nid = page.insert('new record after compact');
console.log(`整理后写入新记录 -> 槽位 ${nid}（复用墓碑槽位）, 读回: "${page.get(nid)}"`);
fmt(page, '最终状态');
