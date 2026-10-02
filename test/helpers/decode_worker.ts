/** Worker entry: decodes the same buffer and returns a canonical digest. */
import { parentPort, workerData } from 'node:worker_threads';
import { decodeStruct, createStats } from '../../src/schema.ts';
import { Inspector } from '../../src/inspector.ts';
import { OrderBookSchema, OrderSchema, UserSchema } from './orderbook.ts';

const bytes = new Uint8Array(workerData.bytes as ArrayBuffer);
const stats = createStats();
const decoded = decodeStruct(OrderBookSchema, bytes, stats);
const report = new Inspector()
  .registerSchema(OrderBookSchema)
  .registerSchema(OrderSchema)
  .registerSchema(UserSchema)
  .inspect('OrderBook', decoded, { leaks: stats.leaks });

parentPort?.postMessage(JSON.stringify({ decoded, report }, (_, v) => (typeof v === 'bigint' ? `${v}n` : v)));
