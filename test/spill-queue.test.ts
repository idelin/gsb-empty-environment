import { test } from 'node:test';
import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';
import { SpillQueue } from '../src/spill-queue.ts';

function makeQueue(maxMemoryBytes: number): SpillQueue<string> {
  return new SpillQueue<string>({
    maxMemoryBytes,
    serialize: (s) => Buffer.from(s, 'utf8'),
    deserialize: (b) => b.toString('utf8'),
  });
}

test('溢写队列：内存阈值内不溢写', async () => {
  const queue = makeQueue(1_024);
  await queue.push('a', 100);
  await queue.push('b', 100);
  assert.equal(queue.memoryBytes, 200);
  assert.equal(queue.spilledBytes, 0);
  assert.equal(queue.spillFilePath, null);
  assert.equal(await queue.shift(), 'a');
  assert.equal(await queue.shift(), 'b');
  assert.equal(await queue.shift(), undefined);
  await queue.close();
});

test('溢写队列：超额后溢写磁盘且保持全局 FIFO', async () => {
  const queue = makeQueue(200);
  for (let i = 0; i < 10; i++) {
    await queue.push('item-' + i, 100);
  }
  assert.equal(queue.memoryBytes, 200, '内存中仅保留阈值内的 2 个元素');
  assert.ok(queue.spilledBytes > 0, '其余元素应溢写磁盘');
  const path = queue.spillFilePath;
  assert.ok(path !== null && existsSync(path));

  for (let i = 0; i < 10; i++) {
    assert.equal(await queue.shift(), 'item-' + i, '内存与磁盘边界的 FIFO 顺序必须保持');
  }
  assert.equal(await queue.shift(), undefined);
  await queue.close();
});

test('溢写队列：磁盘积压期间新元素继续落盘，排空后恢复内存驻留', async () => {
  const queue = makeQueue(100);
  await queue.push('m0', 100);
  await queue.push('d0', 100);
  await queue.push('d1', 100);
  assert.equal(await queue.shift(), 'm0');
  assert.equal(await queue.shift(), 'd0');
  assert.equal(await queue.shift(), 'd1');

  await queue.push('m1', 100);
  assert.equal(queue.memoryBytes, 100);
  assert.equal(await queue.shift(), 'm1');
  await queue.close();
});

test('溢写队列：close 后删除临时文件', async () => {
  const queue = makeQueue(1);
  await queue.push('x', 100);
  const path = queue.spillFilePath;
  assert.ok(path !== null && existsSync(path));
  await queue.close();
  assert.equal(existsSync(path), false, '临时溢写文件应被清理');
});
