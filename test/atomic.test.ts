import test from 'node:test';
import assert from 'node:assert/strict';
import { Worker } from 'node:worker_threads';
import { AtomicCell, SharedAtomicCounter } from '../src/atomic.ts';

test('AtomicCell: load/store 基本语义', () => {
  const cell = new AtomicCell(1);
  assert.equal(cell.load(), 1);
  cell.store(42);
  assert.equal(cell.load(), 42);
});

test('AtomicCell: compareExchange 成功时交换并返回旧值', () => {
  const cell = new AtomicCell('a');
  const old = cell.compareExchange('a', 'b');
  assert.equal(old, 'a');
  assert.equal(cell.load(), 'b');
});

test('AtomicCell: compareExchange 失败时保持不变并返回当前值', () => {
  const cell = new AtomicCell('x');
  const old = cell.compareExchange('y', 'z');
  assert.equal(old, 'x');
  assert.equal(cell.load(), 'x');
});

test('AtomicCell: compareExchange 对引用类型使用 Object.is 同一性', () => {
  const sentinel = { tag: 1 };
  const cell = new AtomicCell<object | null>(null);
  assert.equal(cell.compareExchange(null, sentinel), null);
  assert.equal(cell.load(), sentinel);
  // 结构相同但非同一对象 => CAS 必须失败
  assert.equal(cell.compareExchange({ tag: 1 }, null), sentinel);
  assert.equal(cell.load(), sentinel);
});

test('AtomicCell: update 以 CAS 重试循环完成读-改-写', () => {
  const cell = new AtomicCell(0);
  for (let i = 0; i < 1000; i++) cell.update((n) => n + 1);
  assert.equal(cell.load(), 1000);
  const doubled = cell.update((n) => n * 2);
  assert.equal(doubled, 2000);
  assert.equal(cell.load(), 2000);
});

test('SharedAtomicCounter: 单线程 add / compareExchange', () => {
  const counter = new SharedAtomicCounter();
  assert.equal(counter.load(), 0);
  assert.equal(counter.add(5), 0);
  assert.equal(counter.load(), 5);
  assert.equal(counter.compareExchange(5, 9), 5);
  assert.equal(counter.load(), 9);
  assert.equal(counter.compareExchange(5, 1), 9); // 失败
  assert.equal(counter.load(), 9);
});

test('SharedAtomicCounter: 多 worker 并发自增结果精确无丢失', async () => {
  const counter = new SharedAtomicCounter();
  const WORKERS = 4;
  const ITERATIONS = 50_000;
  const jobs = Array.from(
    { length: WORKERS },
    () =>
      new Promise<void>((resolve, reject) => {
        const worker = new Worker(new URL('../workers/counterWorker.ts', import.meta.url), {
          workerData: { buffer: counter.buffer, iterations: ITERATIONS },
        });
        worker.once('error', reject);
        worker.once('exit', (code) =>
          code === 0 ? resolve() : reject(new Error(`worker exited with code ${code}`)),
        );
      }),
  );
  await Promise.all(jobs);
  assert.equal(counter.load(), WORKERS * ITERATIONS);
});
