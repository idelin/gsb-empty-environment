// Worker entry: runs an isolated decoder with its own pools in a real thread.
import { parentPort, workerData } from 'node:worker_threads';
import { defineSchema, MessageDecoder } from '../src/decoder.ts';
import { encode } from '../src/encoder.ts';

const Schema = defineSchema('WorkerMsg', [
  { no: 1, name: 'id', type: 'varint' },
  { no: 2, name: 'name', type: 'string' },
  { no: 3, name: 'values', type: 'varint', repeated: true, packed: true },
]);

const { iterations, seed } = workerData as { iterations: number; seed: number };
const decoder = new MessageDecoder(Schema);
const bytes = encode(Schema, { id: seed, name: `worker-${seed}`, values: [seed, seed + 1, seed + 2] });

let checksum = 0;
for (let i = 0; i < iterations; i++) {
  const msg = decoder.decode(bytes) as Record<string, unknown>;
  checksum += (msg.id as number) + (msg.values as number[]).length;
  decoder.release(msg);
}
decoder.assertNoLeaks();
parentPort!.postMessage({ ok: true, checksum });
