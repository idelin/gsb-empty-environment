/**
 * End-to-end demo:
 *  1. hand-encode a multi-level nested protobuf message,
 *  2. decode + validate it via the reflection engine,
 *  3. run a multi-node contention round on the arbiter.
 */
import { decodeMessage, encodeMessage } from '../src/protobuf.ts';
import { validateMessage } from '../src/schema.ts';
import { TruncatedMessageError } from '../src/errors.ts';
import { Arbiter } from '../src/arbiter.ts';
import { ENVELOPE_SCHEMA, SAMPLE_VALUE } from '../tests/fixtures.ts';

const hex = (bytes: Uint8Array) =>
  [...bytes].map((b) => b.toString(16).padStart(2, '0')).join(' ');

// --- 1. encode -------------------------------------------------------------
const bytes = encodeMessage(ENVELOPE_SCHEMA, SAMPLE_VALUE);
console.log(`encoded ${bytes.length} bytes:`);
console.log(hex(bytes));

// --- 2. decode + validate --------------------------------------------------
const decoded = decodeMessage(ENVELOPE_SCHEMA, bytes);
console.log('\ndecoded struct:');
console.dir(decoded, { depth: null });
const issues = validateMessage(ENVELOPE_SCHEMA, decoded);
console.log(`validation issues: ${issues.length}`);

// --- 3. truncation guard ---------------------------------------------------
const cut = bytes.subarray(0, bytes.length - 3);
try {
  decodeMessage(ENVELOPE_SCHEMA, cut);
  console.log('\ntruncated input decoded (cut landed on a field boundary)');
} catch (err) {
  if (err instanceof TruncatedMessageError) {
    console.log(`\ntruncation safely rejected: ${err.message}`);
  } else {
    throw err;
  }
}

// --- 4. multi-node contention ----------------------------------------------
const arbiter = new Arbiter();
const NODES = 5;
const INCREMENTS = 10;
await Promise.all(
  Array.from({ length: NODES }, (_, i) =>
    (async () => {
      for (let j = 0; j < INCREMENTS; j += 1) {
        await arbiter.submitWithRetry<number>(`node-${i}`, 'shared-counter', (c) => (c ?? 0) + 1);
      }
    })(),
  ),
);
const final = await arbiter.read('shared-counter');
const stats = arbiter.stats();
console.log(`\narbiter: ${NODES} nodes x ${INCREMENTS} increments`);
console.log(`final value=${final.value} version=${final.version}`);
console.log(
  `commits=${stats.committed} conflicts=${stats.conflicts} ` +
    `pending=${stats.pending} liveOps=${stats.liveOps}`,
);
await arbiter.close();
