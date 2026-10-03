/**
 * End-to-end demo: schema definition -> encode -> hand-rolled wire decode ->
 * pooled release -> leak assertion -> steady-state latency benchmark.
 *
 * Run: node examples/demo.ts
 */
import { defineSchema, MessageDecoder } from '../src/decoder.ts';
import { encode } from '../src/encoder.ts';

const Address = defineSchema('Address', [
  { no: 1, name: 'city', type: 'string' },
  { no: 2, name: 'zip', type: 'fixed32' },
]);

const Person = defineSchema('Person', [
  { no: 1, name: 'name', type: 'string' },
  { no: 2, name: 'age', type: 'zigzag32' },
  { no: 3, name: 'address', type: 'message', schema: Address },
  { no: 4, name: 'scores', type: 'varint', repeated: true, packed: true },
  { no: 5, name: 'tags', type: 'string', repeated: true },
  { no: 6, name: 'balance', type: 'zigzag64' },
  { no: 7, name: 'id', type: 'fixed64' },
]);

const source = {
  name: 'Ada Lovelace',
  age: -36,
  address: { city: 'Shanghai', zip: 200000 },
  scores: [0, 1, 127, 128, 300, 16384],
  tags: ['engineer', 'pioneer'],
  balance: -(2n ** 60n),
  id: 0xdead_beef_cafe_f00dn,
};

const bytes = encode(Person, source);
console.log(`encoded ${bytes.length} bytes:`);
console.log(`  ${[...bytes].map((b) => b.toString(16).padStart(2, '0')).join(' ')}`);

const decoder = new MessageDecoder(Person);
const msg = decoder.decode(bytes) as Record<string, unknown>;
console.log('\ndecoded message:');
console.log(JSON.stringify(msg, (_, v) =>
  typeof v === 'bigint' ? (v < 0n ? `-0x${(-v).toString(16)}` : `0x${v.toString(16)}`) : v, 2));
decoder.release(msg);
decoder.assertNoLeaks();
console.log('\nleak check: OK (all pooled objects released)');

// Truncation safety demo
const truncated = bytes.subarray(0, bytes.length - 3);
try {
  decoder.decode(truncated);
  console.log('\ntruncation check: FAILED (no error raised)');
  process.exitCode = 1;
} catch (err) {
  console.log(`\ntruncation check: OK -> ${(err as Error).name}: ${(err as Error).message}`);
}
decoder.assertNoLeaks();

// Steady-state benchmark: pooled decode should allocate nothing per iteration.
const iterations = 1_000_000;
for (let i = 0; i < 10_000; i++) decoder.release(decoder.decode(bytes)); // warmup
const start = process.hrtime.bigint();
for (let i = 0; i < iterations; i++) {
  decoder.release(decoder.decode(bytes));
}
const elapsedNs = Number(process.hrtime.bigint() - start);
decoder.assertNoLeaks();
const opsPerSec = Math.round(iterations / (elapsedNs / 1e9));
console.log(`\nbenchmark: ${iterations.toLocaleString()} pooled decode+release cycles`);
console.log(`  ${(elapsedNs / 1e6).toFixed(1)} ms total, ~${opsPerSec.toLocaleString()} ops/s, ${(elapsedNs / iterations).toFixed(0)} ns/op`);
console.log('  pool stats:', JSON.stringify(decoder.stats));
