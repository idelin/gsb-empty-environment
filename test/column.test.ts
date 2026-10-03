import { test } from "node:test";
import assert from "node:assert/strict";
import { DictionaryEncodedColumn } from "../src/column.ts";
import type { UpgradeEvent } from "../src/column.ts";

function makeWord(i: number): string {
  return `word-${i.toString().padStart(4, "0")}`;
}

test("初始状态：空列使用 uint8 存储", () => {
  const col = new DictionaryEncodedColumn();
  assert.equal(col.storageWidth, 8);
  assert.equal(col.length, 0);
  assert.equal(col.dictionarySize, 0);
  assert.equal(col.upgrades.length, 0);
});

test("低基数写入：保持 uint8，重复值不扩张字典", () => {
  const col = new DictionaryEncodedColumn();
  for (let i = 0; i < 100; i++) {
    col.append(makeWord(i % 10)); // 只有 10 个不同词汇
  }
  assert.equal(col.length, 100);
  assert.equal(col.dictionarySize, 10);
  assert.equal(col.storageWidth, 8);
  for (let i = 0; i < 100; i++) {
    assert.equal(col.get(i), makeWord(i % 10));
  }
});

test("门限边界：256 个不同词汇仍用 uint8，第 257 个触发升级", () => {
  const col = new DictionaryEncodedColumn();
  for (let i = 0; i < 256; i++) {
    col.append(makeWord(i));
  }
  // id 0..255 均可被 uint8 容纳
  assert.equal(col.dictionarySize, 256);
  assert.equal(col.storageWidth, 8);
  assert.equal(col.upgrades.length, 0);

  // 第 257 个不同词汇获得 id 256，突破 255 门限
  col.append(makeWord(256));
  assert.equal(col.dictionarySize, 257);
  assert.equal(col.storageWidth, 16);
  assert.equal(col.upgrades.length, 1);

  const event = col.upgrades[0];
  assert.equal(event.fromWidth, 8);
  assert.equal(event.toWidth, 16);
  assert.equal(event.dictionarySize, 257);
  assert.equal(event.rowCount, 256);
});

test("验收场景：连续写入 260 个新字符串，自动升级且查询持续正确", () => {
  const col = new DictionaryEncodedColumn();
  const events: UpgradeEvent[] = [];
  col.onUpgrade((e) => events.push(e));

  const expected: string[] = [];
  for (let i = 0; i < 260; i++) {
    const word = makeWord(i);
    const row = col.append(word);
    expected.push(word);
    assert.equal(row, i);
    // 每次写入后全量校验：热重构前后查询结果始终正确
    for (let j = 0; j <= i; j++) {
      assert.equal(col.get(j), expected[j]);
    }
  }

  assert.equal(col.length, 260);
  assert.equal(col.dictionarySize, 260);
  assert.equal(col.storageWidth, 16);
  assert.equal(events.length, 1, "升级应恰好发生一次");
  assert.deepEqual(
    { fromWidth: events[0].fromWidth, toWidth: events[0].toWidth },
    { fromWidth: 8, toWidth: 16 },
  );

  // 反向查询同样正确
  for (let i = 0; i < 260; i++) {
    assert.equal(col.codeOf(makeWord(i)), i);
  }
  assert.equal(col.codeOf("no-such-word"), -1);
});

test("升级后仍可继续写入与读取（服务不中断）", () => {
  const col = new DictionaryEncodedColumn();
  for (let i = 0; i < 257; i++) {
    col.append(makeWord(i));
  }
  assert.equal(col.storageWidth, 16);

  // 升级后继续混合写入新旧词汇
  for (let i = 257; i < 1000; i++) {
    col.append(makeWord(i));
  }
  for (let i = 0; i < 100; i++) {
    col.append(makeWord(i)); // 重复旧词
  }
  assert.equal(col.length, 1100);
  assert.equal(col.dictionarySize, 1000);
  assert.equal(col.storageWidth, 16);
  assert.equal(col.upgrades.length, 1);
  for (let i = 0; i < 1000; i++) {
    assert.equal(col.get(i), makeWord(i));
  }
});

test("位向量重写不丢数据：升级瞬间既有编码逐位一致", () => {
  const col = new DictionaryEncodedColumn();
  const snapshot: string[] = [];
  col.onUpgrade(() => {
    // 升级事件触发时（新向量已就位），立即全量回读校验
    for (let i = 0; i < col.length; i++) {
      assert.equal(col.get(i), snapshot[i]);
    }
  });
  for (let i = 0; i < 300; i++) {
    const word = makeWord(i);
    snapshot.push(word);
    col.append(word);
  }
  assert.equal(col.upgrades.length, 1);
});

test("越界访问抛出 RangeError", () => {
  const col = new DictionaryEncodedColumn();
  col.append("a");
  assert.throws(() => col.get(1), RangeError);
  assert.throws(() => col.get(-1), RangeError);
});

test("统计信息反映存储层状态", () => {
  const col = new DictionaryEncodedColumn();
  for (let i = 0; i < 260; i++) {
    col.append(makeWord(i));
  }
  const stats = col.stats();
  assert.equal(stats.rowCount, 260);
  assert.equal(stats.dictionarySize, 260);
  assert.equal(stats.storageWidth, 16);
  assert.equal(stats.upgradeCount, 1);
  assert.ok(stats.memoryBytes >= 260 * 2, "uint16 位向量每行至少 2 字节");
});
