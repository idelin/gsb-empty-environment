// End-to-end demo: build an index, commit versions, query, verify.
import { SpatioTemporalIndex, formatQueryResult } from '../dist/index.js';
import { verifyQueryResult } from '../dist/verifier.js';

const idx = new SpatioTemporalIndex();

// ship-1: moves along +x, then turns along +y.
idx.addSegments([
  { objectId: 'ship-1', segmentId: 's1', t0: 0, t1: 10, p0: { x: 0, y: 0, z: 0 }, p1: { x: 30, y: 0, z: 0 } },
  { objectId: 'ship-1', segmentId: 's2', t0: 10, t1: 20, p0: { x: 30, y: 0, z: 0 }, p1: { x: 30, y: 20, z: 0 } },
]);
// ship-2: parked far away.
idx.addSegment({ objectId: 'ship-2', segmentId: 's3', t0: 5, t1: 15, p0: { x: 100, y: 100, z: 100 }, p1: { x: 100, y: 100, z: 100 } });
const v1 = idx.commit();

// Replay query: who crossed box x in [10,20], y,z in [-5,5] during [0, 20)?
const box = { lo: { x: 10, y: -5, z: -5 }, hi: { x: 20, y: 5, z: 5 } };
const window = { t0: 0, t1: 20 };

const r1 = idx.query(box, window, { version: v1 });
console.log('=== version ' + v1 + ' result ===');
console.log(JSON.stringify(formatQueryResult(r1), null, 2));
console.log('verify:', JSON.stringify(verifyQueryResult(idx, r1)));

// Update ship-1's trajectory (new route), creating version 2.
idx.updateObject('ship-1', [
  { objectId: 'ship-1', segmentId: 's4', t0: 0, t1: 20, p0: { x: 15, y: 0, z: 0 }, p1: { x: 15, y: 0, z: 0 } },
]);
const v2 = idx.commit();

const r2 = idx.query(box, window);
console.log('=== version ' + v2 + ' (latest) result ===');
console.log(JSON.stringify(formatQueryResult(r2), null, 2));
console.log('verify:', JSON.stringify(verifyQueryResult(idx, r2)));

// The old snapshot is still queryable.
const r1again = idx.query(box, window, { version: v1 });
console.log('=== version ' + v1 + ' re-queried after update ===');
console.log('objects:', JSON.stringify(r1again.objects.map((o) => o.objectId)));
console.log('verify:', JSON.stringify(verifyQueryResult(idx, r1again)));
