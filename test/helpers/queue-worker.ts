import { parentPort, workerData } from 'node:worker_threads';
import { LockFreeQueue } from '../../src/lockfree-queue.ts';

interface WorkerSpec {
  queueBuffer: SharedArrayBuffer;
  capacity: number;
  bitmapBuffer: SharedArrayBuffer;
  counterBuffer: SharedArrayBuffer;
  workerId: number;
  workers: number;
  perWorker: number;
}

const spec = workerData as WorkerSpec;
const queue = LockFreeQueue.attach(spec.queueBuffer, spec.capacity);
const bitmap = new Int32Array(spec.bitmapBuffer);
const counter = new Int32Array(spec.counterBuffer); // [0] = consumed total
const total = spec.workers * spec.perWorker;
const myBase = spec.workerId * spec.perWorker;

let produced = 0;
let consumed = 0;

// Mixed producer/consumer loop: keep going until I produced my share and
// every item in the system has been consumed exactly once.
while (produced < spec.perWorker || Atomics.load(counter, 0) < total) {
  if (produced < spec.perWorker && queue.enqueue(myBase + produced)) {
    produced++;
  }
  const value = queue.dequeue();
  if (value !== null) {
    Atomics.add(bitmap, value, 1);
    Atomics.add(counter, 0, 1);
    consumed++;
  }
}

parentPort!.postMessage({ produced, consumed });
