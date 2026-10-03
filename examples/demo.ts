/**
 * 验收样例：10 万行 URL 字符串列上批量执行
 *   url LIKE 'https://api.%'
 * 向量化算子在紧凑字节数组上批量比对前置字节，瞬时生成匹配位图。
 *
 * 运行：node --experimental-strip-types examples/demo.ts
 */

import { StringColumn } from "../src/column.ts";
import { like, matchesRegex, startsWith } from "../src/operators.ts";

const ROWS = 100_000;

function generateUrls(count: number): string[] {
  const urls: string[] = new Array(count);
  for (let i = 0; i < count; i++) {
    const mod = i % 10;
    if (mod < 2) {
      urls[i] = `https://api.service-${i % 500}.example.com/v${1 + (i % 3)}/users/${i}`;
    } else if (mod < 5) {
      urls[i] = `https://www.example.com/page/${i}?ref=home`;
    } else if (mod < 7) {
      urls[i] = `http://api.legacy.example.net/items/${i}`;
    } else if (mod < 9) {
      urls[i] = `https://cdn.static.example.io/assets/img-${i}.png`;
    } else {
      urls[i] = `https://apiary.docs.example.org/page/${i}`; // 注意：不以 https://api. 开头
    }
  }
  return urls;
}

function timeIt<T>(label: string, fn: () => T): T {
  const start = performance.now();
  const result = fn();
  const ms = performance.now() - start;
  console.log(`${label}: ${ms.toFixed(2)} ms`);
  return result;
}

// 1. 构建紧凑字符串列（字节数组 + 偏移数组）
const urls = generateUrls(ROWS);
const column = timeIt(`构建列（${ROWS.toLocaleString()} 行）`, () => StringColumn.from(urls));
console.log(
  `列存储: ${column.data.byteLength.toLocaleString()} 字节数据 + ` +
    `${column.offsets.byteLength.toLocaleString()} 字节偏移（${column.rowCount.toLocaleString()} 行）`,
);

// 2. 向量化 LIKE 过滤：url LIKE 'https://api.%'
const bitmap = timeIt("like(column, 'https://api.%')", () =>
  like(column, "https://api.%"),
);
const hits = bitmap.cardinality();
console.log(`命中行数: ${hits.toLocaleString()} / ${ROWS.toLocaleString()}`);

// 3. 与原生逐行 JS 过滤对照，验证结果一致
const expected = urls.filter((u) => u.startsWith("https://api."));
console.log(`原生 filter 命中: ${expected.length.toLocaleString()}（一致性: ${
  expected.length === hits && bitmap.toIndices().every((r, i) => column.get(r) === expected[i])
    ? "OK"
    : "MISMATCH"
}）`);

// 4. 位图压缩统计
const compressed = bitmap.toCompressed();
console.log(
  `位图: ${bitmap.wordCount.toLocaleString()} 字（${(bitmap.wordCount * 4).toLocaleString()} 字节，每行 1 bit），` +
    `RLE 压缩后 ${compressed.compressedWordCount.toLocaleString()} 字` +
    `（本例命中均匀分布、游程短，RLE 不受益；稀疏/连续命中时压缩显著）`,
);

// 5. 其他算子演示
const regexHits = timeIt("matchesRegex(column, /^https:\\/\\/api\\.[^/]+\\/v2\\//)", () =>
  matchesRegex(column, /^https:\/\/api\.[^/]+\/v2\//),
).cardinality();
console.log(`正则 ^https://api\\.[^/]+/v2/ 命中: ${regexHits.toLocaleString()}`);

const prefixHits = timeIt("startsWith(column, 'https://')", () =>
  startsWith(column, "https://"),
).cardinality();
console.log(`前缀 https:// 命中: ${prefixHits.toLocaleString()}`);

// 6. 抽样物化前 5 条命中行
console.log("前 5 条命中:", column.collect(bitmap).slice(0, 5));
