import {
  decodeMessage, encodeMessage, runWorkload, diffTraces, TraceRecorder, Simulator,
  type MessageDescriptor,
} from '../src/index.ts';

const line = (s: string) => console.log(`\n=== ${s} ===`);

const Address: MessageDescriptor = {
  name: 'Address',
  fields: [
    { no: 1, name: 'city', type: 'string' },
    { no: 2, name: 'zip', type: 'sint32' },
  ],
};
const Person: MessageDescriptor = {
  name: 'Person',
  fields: [
    { no: 1, name: 'name', type: 'string' },
    { no: 2, name: 'id', type: 'uint64' },
    { no: 3, name: 'scores', type: 'sint32', repeated: true, packed: true },
    { no: 4, name: 'address', type: 'message', message: Address },
    { no: 5, name: 'aliases', type: 'string', repeated: true },
    { no: 6, name: 'balance', type: 'sint64' },
  ],
};

line('1. Protobuf round-trip (nested + packed repeated)');
const original = {
  name: 'Ada',
  id: 9007199254740993n,
  scores: [-7, 0, 42, 300],
  address: { city: '上海', zip: -200000 },
  aliases: ['ada', 'a.lovelace'],
  balance: -123456789012345n,
};
const bytes = encodeMessage(Person, original);
console.log(`encoded ${bytes.length} bytes:`, Buffer.from(bytes).toString('hex'));
const decoded = decodeMessage(Person, bytes);
console.log('decoded:', JSON.stringify(decoded, (_, v) => (typeof v === 'bigint' ? `${v}n` : v)));

line('2. Truncation safety');
try {
  decodeMessage(Person, bytes.subarray(0, bytes.length - 3));
} catch (err) {
  console.log(`caught ${(err as Error).name}: ${(err as Error).message}`);
}

line('3. Deterministic simulation (1,000,000 steps)');
const t0 = performance.now();
const run1 = runWorkload({ events: 1_000_000, seed: 42 });
const t1 = performance.now();
console.log(`steps=${run1.steps} served=${run1.served} maxQueue=${run1.maxQueue} in ${(t1 - t0).toFixed(0)}ms`);
console.log(`trace sha256: ${run1.checksum}`);
const run2 = runWorkload({ events: 1_000_000, seed: 42 });
console.log(`re-run identical: ${run1.checksum === run2.checksum}`);

line('4. Fault reproduction');
const buggy = runWorkload({ events: 50_000, seed: 42, buggy: true });
const good = runWorkload({ events: 50_000, seed: 42 });
const divergence = diffTraces(good.trace, buggy.trace);
if (divergence) {
  console.log(`first divergence at event #${divergence.index}`);
  console.log('  expected:', JSON.stringify(divergence.expected));
  console.log('  actual  :', JSON.stringify(divergence.actual));
}

line('5. Event ordering (same timestamp -> priority -> seq)');
const sim = new Simulator(7);
const order: string[] = [];
const rec = new TraceRecorder();
sim.schedule(5, 'late', () => order.push('late'));
sim.schedule(5, 'urgent', () => order.push('urgent'), -1);
sim.schedule(0, 'first', () => order.push('first'));
sim.run({ observer: rec.observer() });
console.log('execution order:', order.join(' -> '));
sim.dispose();
