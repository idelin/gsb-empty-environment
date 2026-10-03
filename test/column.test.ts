import { test } from "node:test";
import assert from "node:assert/strict";
import { StringColumn } from "../src/column.ts";

test("紧凑存储：字节数组 + 偏移数组切分每行", () => {
  const column = StringColumn.from(["abc", "", "de"]);
  assert.equal(column.rowCount, 3);
  assert.deepEqual([...column.offsets], [0, 3, 3, 5]);
  assert.deepEqual([...column.data], [...new TextEncoder().encode("abcde")]);
  assert.equal(column.get(0), "abc");
  assert.equal(column.get(1), "");
  assert.equal(column.get(2), "de");
});

test("支持空列与空字符串", () => {
  const empty = StringColumn.from([]);
  assert.equal(empty.rowCount, 0);
  const column = StringColumn.from(["", "", "x"]);
  assert.equal(column.byteLength(0), 0);
  assert.equal(column.byteLength(2), 1);
});

test("多字节 UTF-8 字符按字节存储、按行解码", () => {
  const column = StringColumn.from(["你好", "héllo", "🚀x"]);
  assert.equal(column.byteLength(0), 6);
  assert.equal(column.get(0), "你好");
  assert.equal(column.get(1), "héllo");
  assert.equal(column.get(2), "🚀x");
});

test("rowBytes 是零拷贝视图", () => {
  const column = StringColumn.from(["hello"]);
  const view = column.rowBytes(0);
  assert.equal(view.buffer, column.data.buffer);
  assert.deepEqual([...view], [...new TextEncoder().encode("hello")]);
});

test("builder 容量自动增长", () => {
  const builder = StringColumn.builder(1);
  const rows: string[] = [];
  for (let i = 0; i < 5000; i++) {
    const s = `row-${i}-${"x".repeat(i % 40)}`;
    rows.push(s);
    builder.append(s);
  }
  const column = builder.build();
  assert.equal(column.rowCount, rows.length);
  for (let i = 0; i < rows.length; i += 499) {
    assert.equal(column.get(i), rows[i]);
  }
});

test("非法布局直接抛错", () => {
  assert.throws(() => new StringColumn(new Uint8Array(0), new Uint32Array(0)));
  assert.throws(() => new StringColumn(new Uint8Array(3), new Uint32Array([1, 3])));
  assert.throws(() => new StringColumn(new Uint8Array(3), new Uint32Array([0, 2])));
});
