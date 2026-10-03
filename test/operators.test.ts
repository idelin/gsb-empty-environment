import { test } from "node:test";
import assert from "node:assert/strict";
import { StringColumn } from "../src/column.ts";
import {
  compileLike,
  contains,
  endsWith,
  equals,
  like,
  matchesRegex,
  startsWith,
} from "../src/operators.ts";

function columnOf(rows: string[]): StringColumn {
  return StringColumn.from(rows);
}

function assertMatches(
  column: StringColumn,
  bitmap: { indices(): Generator<number> },
  expected: string[],
): void {
  assert.deepEqual(column.collect(bitmap), expected);
}

test("startsWith 在字节层面批量比对", () => {
  const column = columnOf(["https://a", "http://b", "https://", "https", ""]);
  assertMatches(column, startsWith(column, "https://"), ["https://a", "https://"]);
  assertMatches(column, startsWith(column, ""), ["https://a", "http://b", "https://", "https", ""]);
  assertMatches(column, startsWith(column, "https://x"), []);
});

test("endsWith / contains / equals", () => {
  const column = columnOf(["a.png", "b.jpg", "cc.png", "png", ""]);
  assertMatches(column, endsWith(column, ".png"), ["a.png", "cc.png"]);
  assertMatches(column, contains(column, "png"), ["a.png", "cc.png", "png"]);
  assertMatches(column, equals(column, "png"), ["png"]);
  assertMatches(column, equals(column, ""), [""]);
});

test("多字节字符前缀按 UTF-8 字节比对", () => {
  const column = columnOf(["你好世界", "你好", "你", "hello"]);
  assertMatches(column, startsWith(column, "你好"), ["你好世界", "你好"]);
  assertMatches(column, startsWith(column, "你好世"), ["你好世界"]);
});

test("compileLike 为各类模式选择正确快路径", () => {
  assert.deepEqual(compileLike("https://api.%"), { kind: "prefix", literal: "https://api." });
  assert.deepEqual(compileLike("%.png"), { kind: "suffix", literal: ".png" });
  assert.deepEqual(compileLike("%foo%"), { kind: "contains", literal: "foo" });
  assert.deepEqual(compileLike("exact"), { kind: "equals", literal: "exact" });
  assert.equal(compileLike("a%b").kind, "regex");
  assert.equal(compileLike("a_c").kind, "regex");
});

test("like 前缀模式与原生语义一致", () => {
  const rows = [
    "https://api.example.com/v1",
    "https://api.",
    "https://apiary.example.com",
    "http://api.example.com",
    "https://api",
    "",
  ];
  const column = columnOf(rows);
  assertMatches(column, like(column, "https://api.%"), [
    "https://api.example.com/v1",
    "https://api.",
  ]);
});

test("like 后缀 / 包含 / 等值 / 下划线 / 转义", () => {
  const column = columnOf(["a.png", "b.jpg", "axc", "abc", "a_c", "100%", "100x"]);
  assertMatches(column, like(column, "%.png"), ["a.png"]);
  assertMatches(column, like(column, "%pn%"), ["a.png"]);
  assertMatches(column, like(column, "a_c"), ["axc", "abc", "a_c"]);
  assertMatches(column, like(column, "a\\_c"), ["a_c"]);
  assertMatches(column, like(column, "100\\%"), ["100%"]);
  assertMatches(column, like(column, "a%b%c"), ["abc"]); // a%b%c 无命中
  assertMatches(column, like(column, "a%c"), ["axc", "abc", "a_c"]);
});

test("matchesRegex 批量正则匹配", () => {
  const column = columnOf([
    "https://api.example.com/v2/users",
    "https://api.example.com/v1/users",
    "https://www.example.com/v2/",
  ]);
  assertMatches(column, matchesRegex(column, /^https:\/\/api\.[^/]+\/v2\//), [
    "https://api.example.com/v2/users",
  ]);
  // /g 标志不应污染批量结果
  assertMatches(column, matchesRegex(column, /v2/g), [
    "https://api.example.com/v2/users",
    "https://www.example.com/v2/",
  ]);
});

test("验收：10 万行 URL 列执行 url LIKE 'https://api.%'", () => {
  const ROWS = 100_000;
  const urls: string[] = new Array(ROWS);
  for (let i = 0; i < ROWS; i++) {
    urls[i] =
      i % 10 < 2
        ? `https://api.service-${i % 500}.example.com/v1/users/${i}`
        : i % 10 === 9
          ? `https://apiary.docs.example.org/page/${i}`
          : `https://www.example.com/page/${i}`;
  }
  const column = StringColumn.from(urls);
  assert.equal(column.rowCount, ROWS);

  const started = performance.now();
  const bitmap = like(column, "https://api.%");
  const elapsed = performance.now() - started;

  const expected = urls.filter((u) => u.startsWith("https://api."));
  assert.equal(bitmap.cardinality(), expected.length);
  assert.equal(bitmap.cardinality(), 20_000);
  assert.deepEqual(column.collect(bitmap), expected);
  console.log(
    `    10 万行 LIKE 'https://api.%' 向量化过滤耗时 ${elapsed.toFixed(2)} ms，` +
      `位图 ${bitmap.wordCount * 4} 字节`,
  );
  assert.ok(elapsed < 500, `向量化过滤应瞬时完成，实际 ${elapsed.toFixed(1)} ms`);
});
