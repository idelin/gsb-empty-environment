import { test } from "node:test";
import assert from "node:assert/strict";
import {
  SlottedPage,
  PAGE_SIZE,
  PageFullError,
  RecordTooLargeError,
} from "../src/slotted-page.ts";

test("新页为空且空间全部连续可用", () => {
  const page = new SlottedPage();
  assert.equal(page.pageSize, PAGE_SIZE);
  assert.equal(page.slotCount, 0);
  assert.equal(page.liveCount, 0);
  assert.equal(page.contiguousFree, PAGE_SIZE - 10); // 仅页头占用
  assert.equal(page.fragmentedFree, 0);
});

test("写入长短不一的记录并可读回", () => {
  const page = new SlottedPage();
  const records = [
    "a",
    "hello slotted page",
    "x".repeat(500),
    "中文可变长记录🎉",
    "",
    "y".repeat(1500),
  ];
  const ids = records.map((r) => page.insert(r));
  assert.deepEqual(ids, [0, 1, 2, 3, 4, 5]);
  records.forEach((r, i) => assert.equal(page.getString(i), r));
  assert.equal(page.liveCount, records.length);
});

test("记录从页尾向页前填充，槽位从前向后增长", () => {
  const page = new SlottedPage();
  page.insert("AAAA");
  page.insert("BBBBBB");
  const buf = page.bytes;
  // 第一条记录紧贴页尾
  assert.equal(buf.subarray(PAGE_SIZE - 4).toString(), "AAAA");
  // 第二条记录位于第一条之前（更低偏移）
  assert.equal(buf.subarray(PAGE_SIZE - 4 - 6, PAGE_SIZE - 4).toString(), "BBBBBB");
});

test("删除中间记录产生碎片，空间不立即连续回收", () => {
  const page = new SlottedPage();
  page.insert("r0".padEnd(100, "0"));
  page.insert("r1".padEnd(200, "1"));
  page.insert("r2".padEnd(300, "2"));
  const before = page.contiguousFree;

  assert.equal(page.delete(1), true);
  assert.equal(page.get(1), null);
  assert.equal(page.liveCount, 2);
  assert.equal(page.tombstoneCount, 1);
  // 连续空闲不变，碎片增加 200
  assert.equal(page.contiguousFree, before);
  assert.equal(page.fragmentedFree, 200);
  assert.equal(page.totalFree, before + 200);
  // 重复删除返回 false
  assert.equal(page.delete(1), false);
});

test("compact 将存活数据向页尾对齐并重写槽位偏移", () => {
  const page = new SlottedPage();
  const r0 = "alpha".repeat(40); // 200B
  const r1 = "beta".repeat(100); // 400B
  const r2 = "gamma".repeat(60); // 300B
  const r3 = "delta".repeat(20); // 100B
  page.insert(r0);
  page.insert(r1);
  page.insert(r2);
  page.insert(r3);

  // 删除中间两条，制造空洞
  page.delete(1);
  page.delete(2);
  const fragBefore = page.fragmentedFree;
  assert.equal(fragBefore, 700);
  const freeBefore = page.contiguousFree;

  page.compact();

  // 碎片归零，连续空闲吸收了被删空间
  assert.equal(page.fragmentedFree, 0);
  assert.equal(page.contiguousFree, freeBefore + fragBefore);
  // 存活数据完整且内容不变
  assert.equal(page.getString(0), r0);
  assert.equal(page.getString(3), r3);
  // 存活数据向页尾对齐：物理上最靠页尾的 r0 紧贴页尾，r3 紧随其后
  const buf = page.bytes;
  assert.equal(buf.subarray(PAGE_SIZE - 200).toString(), r0);
  assert.equal(buf.subarray(PAGE_SIZE - 200 - 100, PAGE_SIZE - 200).toString(), r3);
  // 连续空闲 = 页大小 - 页头 - 4 个槽位 - 存活数据 300B
  assert.equal(page.contiguousFree, PAGE_SIZE - 10 - 4 * 5 - 300);
});

test("compact 后空间利用率最大化，可继续插入大记录", () => {
  const page = new SlottedPage();
  const big = "z".repeat(1000);
  const id0 = page.insert(big);
  const id1 = page.insert("m".repeat(800));
  const id2 = page.insert(big);
  page.delete(id1); // 中间 800B 空洞

  page.compact();
  // 连续空间足够容纳 800B 新记录
  const id3 = page.insert("n".repeat(800));
  assert.equal(page.getString(id0), big);
  assert.equal(page.getString(id2), big);
  assert.equal(page.getString(id3), "n".repeat(800));
});

test("插入时连续空间不足但碎片足够则自动 compact", () => {
  const page = new SlottedPage();
  page.insert("a".repeat(1500));
  page.insert("b".repeat(1500));
  page.delete(0);
  assert.equal(page.fragmentedFree, 1500);
  // 连续空闲不足 1200，但总空间够 → 自动整理
  const id = page.insert("c".repeat(1200));
  assert.equal(page.getString(id), "c".repeat(1200));
  assert.equal(page.fragmentedFree, 0);
});

test("tombstone 槽位可被复用", () => {
  const page = new SlottedPage();
  page.insert("one");
  page.insert("two");
  page.delete(0);
  const id = page.insert("three");
  assert.equal(id, 0); // 复用槽位 0
  assert.equal(page.getString(0), "three");
  assert.equal(page.slotCount, 2);
});

test("页满时抛出 PageFullError", () => {
  const page = new SlottedPage();
  page.insert("x".repeat(2000));
  page.insert("y".repeat(2000));
  assert.throws(() => page.insert("z".repeat(2000)), PageFullError);
});

test("超过页容量的单条记录抛出 RecordTooLargeError", () => {
  const page = new SlottedPage();
  assert.throws(() => page.insert("x".repeat(PAGE_SIZE)), RecordTooLargeError);
});

test("页字节可序列化并重新加载", () => {
  const page = new SlottedPage();
  page.insert("persist me");
  page.insert("and me");
  page.delete(0);
  const snapshot = Buffer.from(page.bytes);

  const loaded = new SlottedPage(PAGE_SIZE, snapshot);
  assert.equal(loaded.get(0), null);
  assert.equal(loaded.getString(1), "and me");
  assert.equal(loaded.tombstoneCount, 1);
  loaded.compact();
  assert.equal(loaded.getString(1), "and me");
});

test("大量随机长短记录的压力与一致性", () => {
  const page = new SlottedPage();
  const expected = new Map<number, string>();
  let nextLen = 7;
  // 伪随机长度序列
  const rand = () => (nextLen = (nextLen * 48271) % 997) + 1;

  for (let round = 0; round < 40; round++) {
    // 尽量插入
    for (let i = 0; i < 20; i++) {
      const rec = String(round) + ":" + "r".repeat(rand());
      try {
        expected.set(page.insert(rec), rec);
      } catch (e) {
        assert.ok(e instanceof PageFullError);
        break;
      }
    }
    // 随机删除约一半
    for (const id of [...expected.keys()]) {
      if (id % 2 === round % 2) {
        assert.equal(page.delete(id), true);
        expected.delete(id);
      }
    }
    page.compact();
    assert.equal(page.fragmentedFree, 0);
    // 校验所有存活记录
    for (const [id, rec] of expected) {
      assert.equal(page.getString(id), rec);
    }
    // 不变式：总空闲 = 页内未占用字节
    let liveBytes = 0;
    for (const rec of expected.values()) liveBytes += Buffer.byteLength(rec);
    assert.equal(page.totalFree, PAGE_SIZE - 10 - page.slotCount * 5 - liveBytes);
  }
});
