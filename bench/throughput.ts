import { Worker } from 'node:worker_threads';
import { AtomicMPMCQueue, MutexQueue } from '../src/queue.ts';

const WORKER_URL = new URL('./helpers/bench_worker.ts', import.meta.url);

const PRODUCERS = 4;
const CONSUMERS = 4;
const PER_PRODUCER = 250_000;
const CAPACITY = 1024;
const TOTAL = PRODUCERS * PER_PRODUCER;

function runWorker(workerData: Record<string, unknown>): Promise<void> {
  return new Promise((resolve, reject) => {
    const worker = new Worker(WORKER_URL, { workerData });
    worker.once('message', () => resolve());
    worker.once('error', reject);
  });
}

async function bench(impl: 'lockfree' | 'mutex'): Promise<number> {
  const sab =
    impl === 'lockfree'
      ? new AtomicMPMCQueue(CAPACITY).buffer
      : new MutexQueue(CAPACITY).buffer;
  const counterSab = new SharedArrayBuffer(4);
  const jobs: Promise<void>[] = [];
  const start = performance.now();
  for (let p = 0; p < PRODUCERS; p++) {
    jobs.push(
      runWorker({
        impl,
        role: 'producer',
        sab,
        capacity: CAPACITY,
        counterSab,
        start: p * PER_PRODUCER,
        count: PER_PRODUCER,
        total: TOTAL,
      }),
    );
  }
  for (let c = 0; c < CONSUMERS; c++) {
    jobs.push(
      runWorker({
        impl,
        role: 'consumer',
        sab,
        capacity: CAPACITY,
        counterSab,
        start: 0,
        count: 0,
        total: TOTAL,
      }),
    );
  }
  await Promise.all(jobs);
  const elapsed = performance.now() - start;
  const consumed = Atomics.load(new Int32Array(counterSab), 0);
  if (consumed !== TOTAL) {
    throw new Error(`${impl}: consumed ${consumed}, expected ${TOTAL}`);
  }
  return elapsed;
}

const lockfree = await bench('lockfree');
const mutex = await bench('mutex');
const fmt = (ms: number) => `${(TOTAL / (ms / 1000) / 1e6).toFixed(2)} M msgs/s`;
console.log(
  `${PRODUCERS} producers x ${CONSUMERS} consumers, ${TOTAL.toLocaleString()} messages, capacity ${CAPACITY}`,
);
console.log(`lock-free (CAS):     ${lockfree.toFixed(0)} ms   ${fmt(lockfree)}`);
console.log(`mutex (global lock): ${mutex.toFixed(0)} ms   ${fmt(mutex)}`);
console.log(`throughput speedup: ${(mutex / lockfree).toFixed(2)}x`);
