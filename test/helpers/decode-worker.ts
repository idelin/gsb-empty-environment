import { parentPort, workerData } from 'node:worker_threads';
import { decodeMessage, encodeMessage } from '../../src/message.ts';
import { addressBookSchema } from './fixture.ts';

const { data, iterations } = workerData as { data: Uint8Array; iterations: number };
let last: unknown;
let checksum = 0;
for (let i = 0; i < iterations; i += 1) {
  last = decodeMessage(addressBookSchema, data);
  checksum = (checksum + encodeMessage(addressBookSchema, last as Record<string, unknown>).length) | 0;
}
parentPort!.postMessage({ result: last, checksum });
