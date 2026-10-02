import { test } from "node:test";
import assert from "node:assert/strict";
import { Buzhash } from "../src/buzhash.ts";
import { pseudoRandomBytes } from "../src/util.ts";

test("buzhash 确定性：相同输入产生相同哈希", () => {
  const a = new Buzhash(48);
  const b = new Buzhash(48);
  const data = Buffer.from("hello content defined chunking");
  for (const byte of data) {
    assert.equal(a.update(byte), b.update(byte));
  }
});

test("buzhash 滚动性质：滑动窗口重算与增量更新一致", () => {
  const windowSize = 16;
  const data = Buffer.alloc(256);
  for (let i = 0; i < 256; i++) data[i] = (i * 37 + 11) & 0xff;

  // 增量滚动
  const rolling = new Buzhash(windowSize);
  const rollingValues: number[] = [];
  for (const byte of data) rollingValues.push(rolling.update(byte));

  // 对每个完整窗口位置，用全新实例重算
  for (let end = windowSize; end <= data.length; end++) {
    const fresh = new Buzhash(windowSize);
    let h = 0;
    for (let i = end - windowSize; i < end; i++) h = fresh.update(data[i]);
    assert.equal(
      rollingValues[end - 1],
      h,
      `窗口 [${end - windowSize}, ${end}) 滚动值与重算值不一致`
    );
  }
});

test("buzhash 雪崩：相邻内容哈希分布不同", () => {
  const hashes = new Set<number>();
  const bh = new Buzhash(48);
  const data = pseudoRandomBytes(1000, 1);
  for (const byte of data) hashes.add(bh.update(byte));
  // 1000 个连续字节应产生大量不同哈希值
  assert.ok(hashes.size > 900, `哈希重复过多: ${hashes.size}/1000`);
});
