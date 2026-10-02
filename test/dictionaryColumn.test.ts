import { test } from "node:test";
import assert from "node:assert/strict";
import {
  DictionaryEncodedColumn,
  type MigrationEvent,
} from "../src/dictionaryColumn.ts";

test("初始状态：uint8 存储、空字典", () => {
  const col = new DictionaryEncodedColumn();
  const s = col.stats;
  assert.equal(s.widthBits, 8);
  assert.equal(s.rows, 0);
  assert.equal(s.dictionarySize, 0);
  assert.equal(s.migrationCount, 0);
});

test("小基数写入：保持 uint8，重复值复用码字", () => {
  const col = new DictionaryEncodedColumn();
  col.append("apple");
  col.append("banana");
  col.append("apple");
  assert.equal(col.stats.widthBits, 8);
  assert.equal(col.stats.dictionarySize, 2);
  assert.equal(col.codeAt(0), col.codeAt(2));
  assert.deepEqual([...col.scan()], ["apple", "banana", "apple"]);
});

test("验收：连续写入 260 个新字符串，突破 255 门限后自动热迁移到 uint16，查询持续正确", () => {
  const col = new DictionaryEncodedColumn();
  const events: MigrationEvent[] = [];
  col.onMigration((e) => events.push(e));

  const values = Array.from({ length: 260 }, (_, i) => `word-${i}`);
  const widthSeen: number[] = [];

  for (let i = 0; i < values.length; i++) {
    col.append(values[i]);
    widthSeen.push(col.stats.widthBits);
    // 写入过程中持续抽样查询，验证服务不中断、结果正确
    assert.equal(col.get(i), values[i]);
    if (i > 0) assert.equal(col.get(i - 1), values[i - 1]);
    assert.equal(col.get(0), "word-0");
  }

  // 前 256 个不同值（码字 0..255）仍在 uint8 内
  assert.ok(widthSeen.slice(0, 256).every((w) => w === 8));
  // 第 257 个新词（码字 256）触发升级
  assert.ok(widthSeen.slice(256).every((w) => w === 16));
  assert.equal(col.stats.widthBits, 16);

  // 迁移恰好发生一次，且重写了当时已存在的 256 行位向量
  assert.equal(events.length, 1);
  assert.equal(events[0].fromWidth, 8);
  assert.equal(events[0].toWidth, 16);
  assert.equal(events[0].rowsRewritten, 256);
  assert.equal(events[0].dictionarySize, 257);
  assert.equal(events[0].triggerValue, "word-256");

  // 迁移后全量回读校验
  assert.equal(col.stats.rows, 260);
  assert.equal(col.stats.dictionarySize, 260);
  for (let i = 0; i < values.length; i++) {
    assert.equal(col.get(i), values[i]);
  }
  assert.deepEqual([...col.scan()], values);
});

test("迁移后继续混合读写：新值与旧值交错，结果一致", () => {
  const col = new DictionaryEncodedColumn();
  const expected: string[] = [];
  for (let i = 0; i < 300; i++) {
    const v = i % 7 === 0 ? "repeat" : `v-${i}`;
    col.append(v);
    expected.push(v);
  }
  assert.equal(col.stats.widthBits, 16);
  assert.equal(col.stats.migrationCount, 1);
  for (let i = 0; i < expected.length; i++) {
    assert.equal(col.get(i), expected[i]);
  }
});

test("行容量倍增扩容与位宽迁移互不干扰", () => {
  const col = new DictionaryEncodedColumn(4);
  for (let i = 0; i < 1000; i++) col.append(`k-${i}`);
  const s = col.stats;
  assert.equal(s.rows, 1000);
  assert.equal(s.widthBits, 16);
  assert.ok(s.capacity >= 1000);
  assert.equal(s.migrationCount, 1);
  for (let i = 0; i < 1000; i++) assert.equal(col.get(i), `k-${i}`);
});

test("越界读取抛出 RangeError", () => {
  const col = new DictionaryEncodedColumn();
  col.append("x");
  assert.throws(() => col.get(1), RangeError);
  assert.throws(() => col.get(-1), RangeError);
  assert.throws(() => col.codeAt(5), RangeError);
});
