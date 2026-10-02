import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SlottedPage,
  PageFullError,
  PAGE_SIZE,
  HEADER_SIZE,
  SLOT_SIZE,
} from '../src/slotted-page.ts';

const rec = (label: string, len: number) =>
  Buffer.alloc(len).fill(label.slice(0, 1)).toString();

test('页大小为 4KB，初始空间 = 页大小 - 页头', () => {
  const page = new SlottedPage();
  assert.equal(page.pageSize, PAGE_SIZE);
  assert.equal(PAGE_SIZE, 4096);
  assert.equal(page.freeSpace(), PAGE_SIZE - HEADER_SIZE);
  assert.equal(page.fragmentedSpace(), 0);
});

test('写入长短不一的记录并可原样读回', () => {
  const page = new SlottedPage();
  const inputs = ['a', 'hello world', 'x'.repeat(500), '中文字符串', 'y'.repeat(37)];
  const ids = inputs.map((s) => page.insert(s));
  assert.deepEqual(ids, [0, 1, 2, 3, 4]);
  inputs.forEach((s, i) => {
    assert.equal(page.get(i)!.toString(), s);
  });
  const used = inputs.reduce((n, s) => n + Buffer.byteLength(s), 0) + ids.length * SLOT_SIZE;
  assert.equal(page.freeSpace(), PAGE_SIZE - HEADER_SIZE - used);
});

test('记录从页尾向前紧密排列', () => {
  const page = new SlottedPage();
  page.insert('AB'); // 2B -> [4094, 4096)
  page.insert('CDE'); // 3B -> [4091, 4094)
  assert.equal(page.get(0)!.toString(), 'AB');
  assert.equal(page.get(1)!.toString(), 'CDE');
  assert.equal(page.freeSpace(), PAGE_SIZE - HEADER_SIZE - 2 * SLOT_SIZE - 5);
});

test('删除中间记录产生碎片，槽位 id 保持稳定', () => {
  const page = new SlottedPage();
  const ids = ['r0', 'r1'.repeat(50), 'r2', 'r3'.repeat(80), 'r4'].map((s) => page.insert(s));
  const freeBefore = page.freeSpace();

  assert.equal(page.delete(ids[1]), true);
  assert.equal(page.delete(ids[3]), true);
  assert.equal(page.get(ids[1]), null);
  assert.equal(page.get(ids[3]), null);
  // 存活记录不受影响
  assert.equal(page.get(ids[0])!.toString(), 'r0');
  assert.equal(page.get(ids[2])!.toString(), 'r2');
  assert.equal(page.get(ids[4])!.toString(), 'r4');
  // 连续空闲不变，碎片增加（r1=100B, r3=160B）
  assert.equal(page.freeSpace(), freeBefore);
  assert.equal(page.fragmentedSpace(), 100 + 160);
  // 重复删除返回 false
  assert.equal(page.delete(ids[1]), false);
});

test('碎片整理：存活数据向页尾对齐，空闲空间合并为连续一段', () => {
  const page = new SlottedPage();
  const payload = ['alpha', 'b'.repeat(200), 'c', 'd'.repeat(300), 'epsilon', 'f'.repeat(64)];
  const ids = payload.map((s) => page.insert(s));
  page.delete(ids[1]);
  page.delete(ids[3]);
  page.delete(ids[4]);
  const fragmented = page.fragmentedSpace();
  assert.ok(fragmented > 0);
  const freeBefore = page.freeSpace();

  page.compact();

  // 碎片全部并入连续空闲空间
  assert.equal(page.fragmentedSpace(), 0);
  assert.equal(page.freeSpace(), freeBefore + fragmented);
  // 存活数据完整可读
  assert.equal(page.get(ids[0])!.toString(), 'alpha');
  assert.equal(page.get(ids[2])!.toString(), 'c');
  assert.equal(page.get(ids[5])!.toString(), 'f'.repeat(64));
  // 存活记录向页尾对齐：最后一条存活记录紧贴页尾
  const stats = page.stats();
  const liveBytes = 5 + 1 + 64; // alpha + c + f*64
  assert.equal(stats.recordBytes, liveBytes);
  assert.equal(
    page.freeSpace(),
    PAGE_SIZE - HEADER_SIZE - stats.slotCount * SLOT_SIZE - liveBytes,
  );
});

test('整理后空间利用率最大化：连续空闲 = 页内全部未用字节', () => {
  const page = new SlottedPage();
  const ids: number[] = [];
  for (let i = 0; i < 20; i++) ids.push(page.insert(`record-${i}-${'z'.repeat(i * 7)}`));
  for (let i = 0; i < 20; i += 2) page.delete(ids[i]); // 删掉一半
  page.compact();
  const s = page.stats();
  assert.equal(
    s.freeBytes,
    s.pageSize - s.headerBytes - s.slotArrayBytes - s.recordBytes,
  );
  assert.equal(s.fragmentedBytes, 0);
});

test('空间不足时抛 PageFullError，整理后可继续写入', () => {
  const page = new SlottedPage();
  page.insert('x'.repeat(2000));
  const id1 = page.insert('y'.repeat(2000));
  page.delete(id1); // 2000B 变成碎片，连续空闲不足
  assert.throws(
    () => page.insert('z'.repeat(1000)),
    (err: unknown) => err instanceof PageFullError,
  );
  page.compact();
  const id2 = page.insert('z'.repeat(1000));
  assert.equal(page.get(id2)!.toString(), 'z'.repeat(1000));
  assert.equal(id2, id1); // 复用了墓碑槽位
});

test('墓碑槽位复用不额外消耗槽位数组空间', () => {
  const page = new SlottedPage();
  const id0 = page.insert('aaaa');
  page.delete(id0);
  const freeBefore = page.freeSpace();
  const id1 = page.insert('bb');
  assert.equal(id1, id0);
  assert.equal(page.freeSpace(), freeBefore - 2); // 只扣记录字节，不扣槽位
});

test('非法输入与越界访问', () => {
  const page = new SlottedPage();
  assert.throws(() => page.insert(''), RangeError);
  assert.equal(page.get(99), null);
  assert.equal(page.get(-1), null);
  assert.equal(page.delete(99), false);
});

test('填满整页（利用率上限验证）', () => {
  const page = new SlottedPage();
  const n = Math.floor((PAGE_SIZE - HEADER_SIZE) / (100 + SLOT_SIZE));
  for (let i = 0; i < n; i++) page.insert('q'.repeat(100));
  assert.throws(() => page.insert('q'.repeat(100)), PageFullError);
  const s = page.stats();
  assert.equal(s.liveRecords, n);
  assert.ok(s.freeBytes < 100 + SLOT_SIZE);
});
