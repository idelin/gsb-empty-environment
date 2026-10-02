import { parentPort, workerData } from 'node:worker_threads';
import { decodeMessage } from '../src/index.ts';
import { Person } from './schemas.ts';

const { bytes, iterations } = workerData;
let checksum = 0;
let last;
for (let i = 0; i < iterations; i++) {
  last = decodeMessage(Person, bytes);
  checksum = (checksum + last.scores.length + last.name.length) >>> 0;
}
parentPort.postMessage({ checksum, sample: last.name });
