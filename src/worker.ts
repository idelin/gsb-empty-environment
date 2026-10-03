/**
 * 并发压测 Worker: 高频调用 tryAcquire(1), 直到主线程置位 stop 标志。
 * 本地计数, 退出前用一次 Atomics.add 汇总到共享计数器, 并通过消息上报。
 */
import { parentPort, workerData } from 'node:worker_threads';
import { CasTokenBucket } from './token-bucket.ts';

interface WorkerConfig {
  buffer: SharedArrayBuffer;        // 令牌桶共享状态
  controlBuffer: SharedArrayBuffer; // 控制块: [0..4) Int32 stop, [8..16) BigUint64 acquired
  capacity: number;
  refillRatePerSec: number;
}

const config = workerData as WorkerConfig;
const bucket = new CasTokenBucket({
  capacity: config.capacity,
  refillRatePerSec: config.refillRatePerSec,
  buffer: config.buffer,
});

const stopFlag = new Int32Array(config.controlBuffer, 0, 1);
const acquiredCounter = new BigUint64Array(config.controlBuffer, 8, 1);

let local = 0;
while (Atomics.load(stopFlag, 0) === 0) {
  if (bucket.tryAcquire(1)) local++;
}

Atomics.add(acquiredCounter, 0, BigInt(local));
parentPort?.postMessage({ acquired: local });
