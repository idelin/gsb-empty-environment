/**
 * 读线程：只读事务循环执行全表扫描校验和统计。
 * 每次扫描 = 一次 O(1) 快照 + 无锁遍历 1000 个键的不可变版本链，
 * 校验快照内总和守恒；任何撕裂（看到半个写事务）都会计入 torn。
 */
import { parentPort, workerData } from 'node:worker_threads';
import { MvccStore } from '../store.ts';

const { sab, keyCount, poolCapacity, expectedSum } = workerData as {
  sab: SharedArrayBuffer;
  keyCount: number;
  poolCapacity: number;
  expectedSum: number;
};

const store = MvccStore.attach(sab, keyCount, poolCapacity);

let scans = 0;
let torn = 0;

while (!store.isWriterDone()) {
  const snap = store.snapshot(); // 开启只读事务：无锁、无等待
  const sum = store.sum(snap);   // 无锁遍历不可变版本节点
  const isTorn = sum !== expectedSum;
  store.recordScan(isTorn);
  scans++;
  if (isTorn) torn++;
}

parentPort!.postMessage({ scans, torn });
