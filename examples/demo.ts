import { SlottedPage } from "../src/slotted-page.ts";

const show = (label: string, page: SlottedPage) => {
  const s = page.stats();
  console.log(
    `${label}\n  槽位=${s.slotCount} 存活=${s.liveCount} 墓碑=${s.tombstoneCount}` +
      ` 连续空闲=${s.contiguousFree}B 碎片=${s.fragmentedFree}B` +
      ` 总空闲=${s.totalFree}B 利用率=${(s.utilization * 100).toFixed(1)}%`,
  );
};

const page = new SlottedPage(); // 4KB
show("① 空页", page);

// 写入长短不一的记录
const ids: number[] = [];
ids.push(page.insert("短记录"));
ids.push(page.insert("x".repeat(600)));
ids.push(page.insert("中等长度的记录，包含一些中文内容。"));
ids.push(page.insert("y".repeat(1200)));
ids.push(page.insert("z".repeat(300)));
show("② 写入 5 条长短不一的记录", page);

// 删除中间记录 → 产生碎片
page.delete(ids[1]); // 600B
page.delete(ids[3]); // 1200B
show("③ 删除中间两条记录（产生 1800B 碎片）", page);

// 碎片整理：存活数据向页尾对齐，重写槽位偏移，空闲空间收缩为连续区间
page.compact();
show("④ compact() 碎片整理后", page);

// 整理后数据完好
console.log("⑤ 存活记录校验:");
for (const id of [ids[0], ids[2], ids[4]]) {
  const rec = page.getString(id)!;
  console.log(`  slot ${id}: ${rec.length > 40 ? rec.slice(0, 20) + `…(${rec.length}B)` : rec}`);
}

// 利用回收后的连续空间插入大记录
const big = page.insert("w".repeat(1500));
show(`⑥ 利用回收空间插入 1500B 大记录 (slot ${big})`, page);
