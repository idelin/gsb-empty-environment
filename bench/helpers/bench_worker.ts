import { parentPort, workerData } from 'node:worker_threads';
import { AtomicMPMCQueue, MutexQueue } from '../../src/queue.ts';

interface BenchData {
  impl: 'lockfree' | 'mutex';
  role: 'producer' | 'consumer';
  sab: SharedArrayBuffer;
  capacity: number;
  counterSab: SharedArrayBuffer;
  start: number;
  count: number;
  total: number;
}

const data = workerData as BenchData;
const queue =
  data.impl === 'lockfree'
    ? AtomicMPMCQueue.fromSharedArrayBuffer(data.sab, data.capacity)
    : MutexQueue.fromSharedArrayBuffer(data.sab, data.capacity);

if (data.role === 'producer') {
  for (let i = 0; i < data.count; i++) {
    const value = data.start + i;
    while (!queue.enqueue(value)) {
      // full: retry
    }
  }
} else {
  const counter = new Int32Array(data.counterSab);
  for (;;) {
    const v = queue.dequeue();
    if (v !== undefined) {
      Atomics.add(counter, 0, 1);
    } else if (Atomics.load(counter, 0) >= data.total) {
      break;
    }
  }
}
parentPort!.postMessage({ done: true });
