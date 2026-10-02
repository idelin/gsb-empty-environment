import { DictionaryEncodedColumn } from "../src/dictionaryColumn.ts";

const col = new DictionaryEncodedColumn();
col.onMigration((e) => {
  console.log(
    `[hot-migration] uint${e.fromWidth} -> uint${e.toWidth} | ` +
      `重写字向量行数=${e.rowsRewritten} | 字典基数=${e.dictionarySize} | ` +
      `触发词="${e.triggerValue}"`,
  );
});

console.log("== 写入 260 个新字符串 ==");
for (let i = 0; i < 260; i++) {
  col.append(`word-${i}`);
  if (i % 64 === 0 || i >= 254) {
    const s = col.stats;
    console.log(
      `  rows=${String(s.rows).padStart(3)} dict=${String(s.dictionarySize).padStart(3)} ` +
        `width=uint${s.widthBits} capacity=${s.capacity}`,
    );
  }
}

console.log("\n== 迁移后抽样查询 ==");
for (const i of [0, 1, 254, 255, 256, 257, 259]) {
  console.log(`  get(${i}) = ${col.get(i)}`);
}

console.log("\n== 继续追加重复与新增混合写入 ==");
col.append("word-0");
col.append("brand-new");
console.log(`  get(260) = ${col.get(260)}`);
console.log(`  get(261) = ${col.get(261)}`);

console.log("\n最终统计:", col.stats);
