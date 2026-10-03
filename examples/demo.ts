import { promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import { LSMTree } from "../src/index.ts";

const dir = await fs.mkdtemp(path.join(os.tmpdir(), "lsm-demo-"));
const tree = await LSMTree.open(dir, {
  memtableFlushThreshold: Number.MAX_SAFE_INTEGER,
  l0CompactionThreshold: Number.MAX_SAFE_INTEGER,
  levelCapacities: [Number.MAX_SAFE_INTEGER, Number.MAX_SAFE_INTEGER, Number.MAX_SAFE_INTEGER],
  maxLevels: 4,
});

function show(title: string): void {
  console.log(`\n=== ${title} ===`);
  for (const { level, files } of tree.inspect()) {
    if (files.length === 0) continue;
    for (const file of files) {
      const entries = file.entries
        .map((e) => `${e.key}=${e.value === null ? "␃TOMBSTONE" : e.value}(seq${e.seq})`)
        .join("  ");
      console.log(`  L${level} sst-${file.id}: ${entries}`);
    }
  }
}

console.log("1) 插入 K1=V1 并 Flush，逐层压实到最底层 L3");
await tree.put("K1", "V1");
await tree.put("F1", "filler");
await tree.flush();
await tree.compactLevel(0);
await tree.compactLevel(1);
await tree.compactLevel(2);
show("K1=V1 已下沉至 L3");
console.log(`   get(K1) => ${await tree.get("K1")}`);

console.log("\n2) 删除 K1（写入 Tombstone）");
await tree.delete("K1");
console.log(`   get(K1) => ${await tree.get("K1")}   <-- 立即 NotFound`);

await tree.flush();
show("Tombstone 落盘到 L0");

await tree.compactLevel(0);
show("第 1 轮 Compaction：Tombstone 下沉 L0 -> L1（L3 有旧数据，必须保留）");
await tree.compactLevel(1);
show("第 2 轮 Compaction：Tombstone 下沉 L1 -> L2（仍非最底层，保留）");
await tree.compactLevel(2);
show("第 3 轮 Compaction：L2 -> L3 最底层合并，Tombstone 与旧值 V1 被物理擦除");

console.log(`\n   get(K1) => ${await tree.get("K1")}`);
console.log(`   scan()  => ${JSON.stringify(await tree.scan())}`);
const files = await fs.readdir(dir);
console.log(`   磁盘文件: ${files.join(", ")}`);
console.log(`   findOnDisk(K1) => ${JSON.stringify(tree.findOnDisk("K1"))}  <-- 无任何痕迹`);

await fs.rm(dir, { recursive: true, force: true });
