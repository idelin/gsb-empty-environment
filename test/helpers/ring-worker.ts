import { parentPort, workerData } from 'node:worker_threads';
import { MPMCRing } from '../../src/queue/mpmc-ring.ts';

interface WorkerTask {
  mode: 'produce' | 'consume' | 'count';
  ringBuffer?: SharedArrayBuffer;
  capacity?: number;
  start?: number;
  count?: number;
  total?: number;
  seenBuffer?: SharedArrayBuffer;
  counterBuffer?: SharedArrayBuffer;
}

const task = workerData as WorkerTask;

if (task.mode === 'produce') {
  const ring = MPMCRing.from(task.ringBuffer!, task.capacity!);
  for (let i = 0; i < task.count!; i++) {
    const value = task.start! + i;
    while (!ring.enqueue(value)) {
      // Ring momentarily full: retry (lock-free backpressure).
    }
  }
} else if (task.mode === 'consume') {
  const ring = MPMCRing.from(task.ringBuffer!, task.capacity!);
  const seen = new Uint32Array(task.seenBuffer!);
  const consumed = new Int32Array(task.counterBuffer!);
  for (;;) {
    if (Atomics.load(consumed, 0) >= task.total!) break;
    const value = ring.dequeue();
    if (value === undefined) continue; // momentarily empty
    Atomics.add(seen, value, 1);
    Atomics.add(consumed, 0, 1);
  }
} else if (task.mode === 'count') {
  const counter = new Int32Array(task.counterBuffer!);
  for (let i = 0; i < task.count!; i++) {
    Atomics.add(counter, 0, 1);
  }
}

parentPort?.postMessage('done');
