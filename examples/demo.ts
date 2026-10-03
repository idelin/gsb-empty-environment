import { DictionaryEncodedColumn } from "../src/column.ts";

const col = new DictionaryEncodedColumn();

col.onUpgrade((e) => {
  console.log(
    `[热重构] uint${e.fromWidth} -> uint${e.toWidth}` +
      `（触发时字典基数=${e.dictionarySize}, 已有行数=${e.rowCount}）`,
  );
});

console.log(`初始存储位宽: uint${col.storageWidth}`);

// 连续写入 260 个新字符串
for (let i = 0; i < 260; i++) {
  col.append(`word-${i.toString().padStart(4, "0")}`);
  if (i === 100) {
    console.log(`写入 ${i + 1} 行后: 位宽=uint${col.storageWidth}, 字典基数=${col.dictionarySize}`);
  }
}

console.log(`写入 260 行后: 位宽=uint${col.storageWidth}, 字典基数=${col.dictionarySize}`);

// 升级后继续读写，验证查询结果
console.log(`get(0)   = ${col.get(0)}`);
console.log(`get(255) = ${col.get(255)}`);
console.log(`get(256) = ${col.get(256)}`);
console.log(`get(259) = ${col.get(259)}`);
console.log(`codeOf("word-0256") = ${col.codeOf("word-0256")}`);

// 重复值复用既有编码
col.append("word-0000");
console.log(`追加重复值后字典基数仍为: ${col.dictionarySize}`);

console.log("统计:", col.stats());
