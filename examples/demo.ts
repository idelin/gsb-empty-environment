/**
 * End-to-end demo:
 *  1. hand-rolled protobuf wire-format encode/decode with nested + packed fields
 *  2. reflection-mode validation
 *  3. incremental snapshots, checkpoint archive, compaction and restore
 */
import { defineMessage, validateMessage } from '../src/schema.ts';
import { decodeMessage, encodeMessage } from '../src/message.ts';
import { DecodeError } from '../src/errors.ts';
import { IncrementalSnapshotEngine } from '../src/snapshot.ts';

const hex = (b: Uint8Array) => [...b].map((x) => x.toString(16).padStart(2, '0')).join(' ');

/* ------------------------------ 1. wire codec ----------------------------- */

const Point = defineMessage('Point', [
  { name: 'x', number: 1, type: 'sint32' },
  { name: 'y', number: 2, type: 'sint32' },
]);
const Track = defineMessage('Track', [
  { name: 'name', number: 1, type: 'string' },
  { name: 'points', number: 2, type: 'message', message: Point, repeated: true },
  { name: 'samples', number: 3, type: 'int32', repeated: true, packed: true },
]);

const track = {
  name: 'demo-轨迹',
  points: [{ x: -1, y: 2 }, { x: 3, y: -4 }],
  samples: [10, -20, 300, 4000],
};

validateMessage(Track, track);
const encoded = encodeMessage(Track, track);
console.log('=== Protobuf wire format ===');
console.log('encoded (%d bytes): %s', encoded.length, hex(encoded));
const { value: decoded } = decodeMessage(Track, encoded);
console.log('decoded:', JSON.stringify(decoded));
console.log('roundtrip equal:', JSON.stringify(decoded) === JSON.stringify(track));

// Safe truncation handling
const truncated = encoded.subarray(0, encoded.length - 3);
try {
  decodeMessage(Track, truncated);
} catch (e) {
  console.log('truncated stream safely rejected:', (e as DecodeError).message);
}

/* -------------------- 2. incremental snapshot & checkpoint ----------------- */

console.log('\n=== Incremental snapshot & checkpoint archive ===');
const engine = new IncrementalSnapshotEngine({ chunkSize: 64 });
const enc = (s: string) => new TextEncoder().encode(s);

// Simulate a large state with concurrent writers.
const writers: Promise<unknown>[] = [];
for (let i = 0; i < 1000; i++) writers.push(engine.set(`user:${i}`, enc(`{"score":${i}}`)));
await Promise.all(writers);
console.log('state size after 1000 concurrent writes:', engine.stats().stateSize);

const cp1 = await engine.createCheckpoint();
console.log('checkpoint @v%d with %d entries', cp1.version, cp1.entries.length);

// Incremental changes only.
await engine.set('user:7', enc('{"score":9999}'));
await engine.delete('user:3');
await engine.set('user:1000', enc('{"score":1000}'));
const snap = await engine.createSnapshot();
console.log('incremental snapshot v%d->v%d carries only %d ops',
  snap.baseVersion, snap.version, snap.ops.length);

// A newer checkpoint makes the earlier delta obsolete.
await engine.createCheckpoint();
const dropped = await engine.compact();
console.log('compaction dropped %d superseded delta(s)', dropped);

const { state, version } = await engine.restore();
console.log('restored to v%d, %d entries; user:7=%s, user:3=%s',
  version, state.size,
  new TextDecoder().decode(state.get('user:7')),
  state.has('user:3') ? 'present' : 'deleted');

await engine.dispose();
const stats = engine.stats();
console.log('after dispose: liveResources=%d (leak-free: %s)',
  stats.liveResources, stats.liveResources === 0);
