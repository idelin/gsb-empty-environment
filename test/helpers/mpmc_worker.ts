import { parentPort, workerData } from 'node:worker_threads';
import { AtomicMPMCQueue } from '../../src/queue.ts';

interface ProducerData {
  role: 'producer';
  sab: SharedArrayBuffer;
  capacity: number;
  start: number;
  count: number;
}

interface ConsumerData {
  role: 'consumer';
  sab: SharedArrayBuffer;
  capacity: number;
  counterSab: SharedArrayBuffer;
  total: number;
}

const data = workerData as ProducerData | ConsumerData;
const queue = AtomicMPMCQueue.fromSharedArrayBuffer(data.sab, data.capacity);

if (data.role === 'producer') {
  for (let i = 0; i < data.count; i++) {
    const value = data.start + i;
    while (!queue.enqueue(value)) {
      // queue full: retry (lock-free backpressure)
    }
  }
  parentPort!.postMessage({ done: true });
} else {
  const counter = new Int32Array(data.counterSab);
  const collected: number[] = [];
  for (;;) {
    const v = queue.dequeue();
    if (v !== undefined) {
      collected.push(v);
      Atomics.add(counter, 0, 1);
    } else if (Atomics.load(counter, 0) >= data.total) {
      break;
    }
  }
  parentPort!.postMessage({ done: true, collected });
}
