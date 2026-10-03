import { parentPort, workerData } from 'node:worker_threads';
import { CASBasedTokenBucket } from './token-bucket.ts';

interface WorkerInput {
  buffer: SharedArrayBuffer;
  capacity: number;
  refillRate: number;
  durationMs: number;
}

const { buffer, capacity, refillRate, durationMs } = workerData as WorkerInput;

// 附加到主线程创建的共享状态（同一 64 位原子字，纯 CAS 竞争，无锁）
const bucket = new CASBasedTokenBucket({ capacity, refillRate, buffer });

const deadline = Date.now() + durationMs;
let acquired = 0;
while (Date.now() < deadline) {
  if (bucket.tryAcquire(1)) acquired += 1;
}

parentPort?.postMessage({ acquired });
