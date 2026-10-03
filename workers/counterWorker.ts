import { workerData } from 'node:worker_threads';

const { buffer, iterations } = workerData as { buffer: SharedArrayBuffer; iterations: number };
const view = new Int32Array(buffer);
for (let i = 0; i < iterations; i++) {
  Atomics.add(view, 0, 1);
}
