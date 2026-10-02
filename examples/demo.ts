/**
 * End-to-end demo:
 *  1. reflection-schema protobuf encode/decode with nesting + packed repeated
 *  2. safe truncation reporting
 *  3. adaptive load-aware threshold controller under a simulated avalanche
 */
import {
  decodeMessage,
  encodeMessage,
  type MessageSchema,
} from '../src/schema.ts';
import { TruncatedMessageError, ValidationError } from '../src/errors.ts';
import { AdaptiveLoadController, type LoadSample } from '../src/adaptive.ts';

const hex = (b: Uint8Array) => [...b].map((x) => x.toString(16).padStart(2, '0')).join(' ');
const stringify = (v: unknown) =>
  JSON.stringify(v, (_, x) => (typeof x === 'bigint' ? `${x}n` : x));

// ---------------------------------------------------------------- 1. codec
const Address: MessageSchema = {
  name: 'Address',
  fields: [
    { name: 'street', number: 1, type: 'string', required: true },
    { name: 'zip', number: 2, type: 'sint32', min: 0, max: 99999 },
  ],
};

const Person: MessageSchema = {
  name: 'Person',
  fields: [
    { name: 'id', number: 1, type: 'uint64', required: true },
    { name: 'name', number: 2, type: 'string', required: true, maxLength: 64 },
    { name: 'scores', number: 3, type: 'sint32', repeated: true }, // packed
    { name: 'address', number: 4, type: 'message', message: Address },
    { name: 'previous', number: 5, type: 'message', message: Address, repeated: true },
    { name: 'delta', number: 6, type: 'sint64' },
    { name: 'ratio', number: 7, type: 'double' },
  ],
};

const person = {
  id: 9007199254740993n,
  name: 'Ada',
  scores: [-1, 0, 1, 300, -2147483648],
  address: { street: '12 Engine Rd', zip: 10001 },
  previous: [{ street: '1 First St', zip: 7 }],
  delta: -42n,
  ratio: Math.PI,
};

const bytes = encodeMessage(Person, person);
console.log('=== 1. Protobuf wire-format codec (reflection schema) ===');
console.log(`encoded ${bytes.length} bytes:`);
console.log(hex(bytes));
const decoded = decodeMessage(Person, bytes);
console.log('decoded:', stringify(decoded));
console.log('round-trip identical:', stringify(decoded) === stringify(person));

// ------------------------------------------------------- 2. truncation guard
console.log('\n=== 2. Safe truncation guard ===');
const cut = bytes.subarray(0, bytes.length - 3);
try {
  decodeMessage(Person, cut);
} catch (err) {
  if (err instanceof TruncatedMessageError) {
    console.log(`TruncatedMessageError: ${err.message}`);
  } else if (err instanceof ValidationError) {
    console.log(`ValidationError (required field cut away): ${err.issues[0]}`);
  }
}

// ------------------------------------------------- 3. adaptive load control
console.log('\n=== 3. Adaptive threshold scaling under an avalanche ===');
let tick = 0;
const controller = new AdaptiveLoadController({
  minLimit: 2,
  maxLimit: 64,
  initialLimit: 64,
  cooldownMs: 3, // 3 ticks
  growStreak: 3,
  growthStep: 2,
  jitterPct: 0,
  maxAdjustmentsPerWindow: 8, // demo clock runs in ticks, not ms
  windowMs: 30,
  now: () => tick,
});
controller.on('adjust', (a) => {
  const adj = a as { direction: string; from: number; to: number; score: number };
  console.log(
    `  tick ${String(tick).padStart(2)}  ${adj.direction.toUpperCase()}  ${adj.from} -> ${adj.to}  (score ${adj.score.toFixed(2)})`,
  );
});

const sampleFor = (phase: string): LoadSample =>
  phase === 'avalanche'
    ? { inflight: controller.limit, queueDepth: controller.limit * 20, errorRate: 0.8, latencyMs: 800, eventLoopLagMs: 300, memoryUsage: 0.9 }
    : phase === 'recovering'
      ? { inflight: 2, queueDepth: 1, errorRate: 0.05, latencyMs: 40, eventLoopLagMs: 5, memoryUsage: 0.4 }
      : { inflight: 0, queueDepth: 0, errorRate: 0, latencyMs: 5, eventLoopLagMs: 1, memoryUsage: 0.2 };

for (tick = 0; tick < 60; tick++) {
  const phase = tick < 5 ? 'stable' : tick < 12 ? 'avalanche' : tick < 25 ? 'recovering' : 'stable';
  controller.record(sampleFor(phase));
  if (tick % 5 === 0 || tick === 59) {
    console.log(`tick ${String(tick).padStart(2)} [${phase.padEnd(10)}] limit = ${controller.limit}`);
  }
}
controller.dispose();
console.log(`final limit after recovery: ${controller.limit} (gradual, no thundering herd)`);
