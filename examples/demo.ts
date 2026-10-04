import { ReflectionEngine, type MessageSchema } from '../src/schema.ts';
import { WireWriter, concatBytes } from '../src/wire/writer.ts';
import { writeVarint, zigzagEncode } from '../src/wire/varint.ts';
import { LocalityAwareRouter } from '../src/sharding/engine.ts';

// ---------- Part 1: hand-rolled protobuf wire-format round trip ----------
const pointSchema: MessageSchema = {
  name: 'Point',
  fields: [
    { name: 'x', number: 1, type: 'sint32', required: true },
    { name: 'y', number: 2, type: 'sint32', required: true },
  ],
};

const trackSchema: MessageSchema = {
  name: 'Track',
  fields: [
    { name: 'id', number: 1, type: 'uint64', required: true },
    { name: 'label', number: 2, type: 'string' },
    { name: 'points', number: 3, type: 'message', messageType: pointSchema, repeated: true },
    { name: 'samples', number: 4, type: 'sint64', repeated: true, packed: true },
  ],
};

const p1 = new WireWriter().varintField(1, zigzagEncode(17)).varintField(2, zigzagEncode(-8)).finish();
const p2 = new WireWriter().varintField(1, zigzagEncode(-3)).varintField(2, zigzagEncode(99)).finish();
const packed = concatBytes(writeVarint(zigzagEncode(-5)), writeVarint(zigzagEncode(0)), writeVarint(zigzagEncode(12345)));

const stream = new WireWriter()
  .varintField(1, 2n ** 40n + 7n)
  .stringField(2, 'demo-track')
  .messageField(3, p1)
  .messageField(3, p2)
  .packedField(4, packed)
  .finish();

console.log(`encoded ${stream.length} bytes: ${Buffer.from(stream).toString('hex')}`);

const engine = new ReflectionEngine();
engine.registerSchema(trackSchema);
const decoded = engine.decode(stream, trackSchema);
console.log('decoded:', JSON.stringify(decoded, (_, v) => (typeof v === 'bigint' ? v.toString() : v), 2));

// Truncation guard demo
const cut = stream.subarray(0, stream.length - 3);
try {
  engine.decode(cut, trackSchema);
} catch (err) {
  console.log(`truncation safely rejected -> ${(err as Error).message}`);
}

// ---------- Part 2: hierarchical sharding + locality-aware routing ----------
const router = new LocalityAwareRouter({ shardCount: 1024, replicaCount: 3 });
const nodes = [
  { id: 'east-z1-a', region: 'cn-east', zone: 'z1', weight: 2 },
  { id: 'east-z1-b', region: 'cn-east', zone: 'z1', weight: 1 },
  { id: 'east-z2-a', region: 'cn-east', zone: 'z2', weight: 1 },
  { id: 'north-z1-a', region: 'cn-north', zone: 'z1', weight: 1 },
  { id: 'north-z2-a', region: 'cn-north', zone: 'z2', weight: 1 },
];
for (const n of nodes) await router.addNode(n);

const client = { region: 'cn-east', zone: 'z1' };
for (const key of ['user:1001', 'user:1002', 'user:1003']) {
  const d = router.route(key, client);
  console.log(`route ${key} -> shard ${d.shardId}, replicas [${d.replicas.map((r) => r.id).join(', ')}], locality=${d.locality}`);
}

for (let i = 0; i < 5000; i++) router.route(`session:${i}`, client);
const m = router.getMetrics();
console.log(
  `metrics over ${m.totalRoutes} routes: local=${m.localRoutes} regional=${m.regionalRoutes} remote=${m.remoteRoutes} ` +
    `cross-node hops=${m.crossNodeHops} (local hit rate ${((m.localRoutes / m.totalRoutes) * 100).toFixed(1)}%)`,
);
