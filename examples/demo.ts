/**
 * End-to-end demo:
 *  1. hand-encode a 3-level nested Protobuf message with packed repeated fields
 *  2. decode + validate it with the reflection engine
 *  3. demonstrate safe truncation handling
 *  4. route keys through the hierarchical locality-aware sharding engine
 *
 * Run: npm run demo
 */

import {
  decodeAndValidate,
  decodeMessage,
  encodeMessage,
  FieldType,
  type MessageSchema,
} from '../src/schema.ts';
import { TruncatedFieldError } from '../src/varint.ts';
import { LocalityAwareRouter, type NodeInfo } from '../src/shard.ts';

// ---------- 1. schema: 3-level nesting + packed repeated ----------
const Point: MessageSchema = {
  name: 'Point',
  fields: [
    { fieldNumber: 1, name: 'x', type: FieldType.Sint32, required: true },
    { fieldNumber: 2, name: 'y', type: FieldType.Sint32, required: true },
  ],
};

const Path: MessageSchema = {
  name: 'Path',
  fields: [
    { fieldNumber: 1, name: 'points', type: FieldType.Message, repeated: true, messageType: Point },
    { fieldNumber: 2, name: 'timestamps', type: FieldType.Fixed64, repeated: true, packed: true },
  ],
};

const Track: MessageSchema = {
  name: 'Track',
  fields: [
    { fieldNumber: 1, name: 'id', type: FieldType.Uint64, required: true },
    { fieldNumber: 2, name: 'label', type: FieldType.String, required: true },
    { fieldNumber: 3, name: 'path', type: FieldType.Message, messageType: Path },
    { fieldNumber: 4, name: 'speeds', type: FieldType.Sint32, repeated: true, packed: true },
    { fieldNumber: 5, name: 'active', type: FieldType.Bool },
  ],
};

const track = {
  id: 9007199254740993n, // 2^53 + 1: needs bigint precision
  label: 'truck-京A-1024',
  path: {
    points: [
      { x: 116, y: 39 },
      { x: -73, y: 40 },
    ],
    timestamps: [1696000000000n, 1696000005000n],
  },
  speeds: [-3, 0, 42, 88, -100],
  active: true,
};

const binary = encodeMessage(Track, track);
console.log('=== Protobuf wire codec ===');
console.log(`encoded ${binary.length} bytes:`);
console.log(Buffer.from(binary).toString('hex').replace(/(..)/g, '$1 ').trim());

const decoded = decodeAndValidate(Track, binary);
console.log('\ndecoded + validated:');
console.log(decoded);

// ---------- 2. safe truncation ----------
console.log('\n=== truncation guard ===');
const cut = binary.subarray(0, binary.length - 3);
try {
  decodeMessage(Track, cut);
  console.log('ERROR: truncation went unnoticed');
} catch (err) {
  console.log(`caught ${(err as Error).name}: ${(err as Error).message}`);
  console.log('is TruncatedFieldError:', err instanceof TruncatedFieldError);
}

// ---------- 3. hierarchical sharding + locality-aware routing ----------
console.log('\n=== locality-aware routing ===');
const router = new LocalityAwareRouter({ shardCount: 4096, replicaCount: 3 });
const nodes: NodeInfo[] = [];
for (const region of ['cn-east', 'cn-north', 'us-west']) {
  for (const zone of ['a', 'b']) {
    for (let i = 0; i < 4; i++) {
      nodes.push({
        id: `${region}/${zone}/node-${i}`,
        region,
        zone,
        weight: 1,
        healthy: true,
      });
    }
  }
}
await Promise.all(nodes.map((n) => router.upsertNode(n)));
console.log(`topology: ${router.nodeCount()} nodes, 4096 virtual shards, 3 replicas`);

// simulate clients in cn-east/a issuing 10k requests
for (let i = 0; i < 10_000; i++) {
  const d = router.route(`user:${i}`, { region: 'cn-east', zone: 'a' });
  const release = router.acquire(d.candidates[0].id);
  release();
}
const s = router.getStats();
const pct = (n: number) => `${((n / s.totalRoutes) * 100).toFixed(1)}%`;
console.log(`same-zone hits : ${s.sameZone} (${pct(s.sameZone)})`);
console.log(`same-region    : ${s.sameRegion} (${pct(s.sameRegion)})`);
console.log(`remote (cross) : ${s.remote} (${pct(s.remote)})`);

// failover: kill the zone, traffic shifts within the region
for (const n of nodes.filter((n) => n.region === 'cn-east' && n.zone === 'a')) {
  await router.setHealthy(n.id, false);
}
const before = router.getStats();
for (let i = 0; i < 1000; i++) router.route(`user:${i}`, { region: 'cn-east', zone: 'a' });
const after = router.getStats();
console.log(
  `\nafter zone failure -> same-region served: ${after.sameRegion - before.sameRegion}/1000, remote: ${after.remote - before.remote}/1000`,
);
router.dispose();
