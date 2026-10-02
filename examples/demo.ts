import { Worker } from 'node:worker_threads';
import {
  decodeMessage,
  encodeMessage,
  ProtobufTruncationError,
} from '../src/protobuf/index.ts';
import type { MessageDescriptor } from '../src/protobuf/index.ts';
import { LockFreeQueue } from '../src/queue/lockfree-queue.ts';
import { MPMCRing } from '../src/queue/mpmc-ring.ts';

// ------------------------------------------------------- 1. protobuf codec --

const Address: MessageDescriptor = {
  name: 'Address',
  fields: [
    { name: 'street', number: 1, type: 'string' },
    { name: 'zip', number: 2, type: 'uint32' },
  ],
};

const Person: MessageDescriptor = {
  name: 'Person',
  fields: [
    { name: 'id', number: 1, type: 'int32' },
    { name: 'name', number: 2, type: 'string' },
    { name: 'address', number: 3, type: Address },
    { name: 'luckyNumbers', number: 4, type: 'sint32', repeated: true, packed: true },
    { name: 'balance', number: 5, type: 'sint64' },
  ],
};

const ada = {
  id: 1,
  name: 'Ada Lovelace',
  address: { street: '12 Analytical Engine Rd', zip: 10001 },
  luckyNumbers: [3, -7, 42, 1000000],
  balance: -9_000_000_000n,
};

const encoded = encodeMessage(Person, ada);
console.log('== Protobuf wire codec ==');
console.log(`encoded ${encoded.length} bytes:`, Buffer.from(encoded).toString('hex'));

const decoded = decodeMessage(Person, encoded);
console.log('decoded:', JSON.stringify(decoded, (_, v) => (typeof v === 'bigint' ? `${v}n` : v)));

// Truncation safety: cut the stream in the middle of the nested message.
try {
  decodeMessage(Person, encoded.subarray(0, encoded.length - 3));
} catch (err) {
  console.log(`truncated stream safely rejected: [${(err as Error).name}] ${(err as Error).message}`);
  console.assert(err instanceof ProtobufTruncationError);
}

// ------------------------------------------------- 2. lock-free MPMC queue --

console.log('\n== Lock-free Michael-Scott queue ==');
const queue = new LockFreeQueue<string>();
queue.enqueue('first');
queue.enqueue('second');
console.log(`dequeue -> ${queue.dequeue()}`);
queue.enqueue('third');
console.log(`dequeue -> ${queue.dequeue()}, dequeue -> ${queue.dequeue()}`);
console.log(`empty=${queue.isEmpty}, collapsedToSentinel=${queue.isCollapsedToSentinel} (no leaked nodes)`);

// --------------------- 3. CAS ring buffer shared across worker threads -----

console.log('\n== Lock-free MPMC ring across worker threads ==');
const PRODUCERS = 4;
const PER_PRODUCER = 25_000;
const TOTAL = PRODUCERS * PER_PRODUCER;
const ring = MPMCRing.create(1024);
const seenBuffer = new SharedArrayBuffer(TOTAL * 4);
const counterBuffer = new SharedArrayBuffer(4);

const workerUrl = new URL('../test/helpers/ring-worker.ts', import.meta.url);
const tasks: Promise<void>[] = [];
const started = performance.now();

for (let p = 0; p < PRODUCERS; p++) {
  tasks.push(
    new Promise<void>((resolve, reject) => {
      const w = new Worker(workerUrl, {
        workerData: {
          mode: 'produce',
          ringBuffer: ring.buffer,
          capacity: ring.capacity,
          start: p * PER_PRODUCER,
          count: PER_PRODUCER,
        },
      });
      w.once('error', reject);
      w.once('exit', (c) => (c === 0 ? resolve() : reject(new Error(`exit ${c}`))));
    })
  );
}
for (let c = 0; c < 4; c++) {
  tasks.push(
    new Promise<void>((resolve, reject) => {
      const w = new Worker(workerUrl, {
        workerData: {
          mode: 'consume',
          ringBuffer: ring.buffer,
          capacity: ring.capacity,
          total: TOTAL,
          seenBuffer,
          counterBuffer,
        },
      });
      w.once('error', reject);
      w.once('exit', (c2) => (c2 === 0 ? resolve() : reject(new Error(`exit ${c2}`))));
    })
  );
}

await Promise.all(tasks);
const elapsed = performance.now() - started;
const seen = new Uint32Array(seenBuffer);
let ok = true;
for (let i = 0; i < TOTAL; i++) {
  if (seen[i] !== 1) {
    console.error(`value ${i} seen ${seen[i]} times!`);
    ok = false;
  }
}
console.log(
  `${TOTAL} items through 4 producers / 4 consumers in ${elapsed.toFixed(0)}ms ` +
    `(${(TOTAL / (elapsed / 1000) / 1e6).toFixed(2)}M ops/s), ` +
    `exactly-once delivery: ${ok ? 'OK' : 'FAILED'}, ring empty: ${ring.isEmpty}`
);
