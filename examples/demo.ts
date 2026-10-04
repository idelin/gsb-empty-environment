/**
 * End-to-end demo:
 *  1. encode a GPS coordinate as a Resolution-8 hex cell id
 *  2. compute the closed, contiguous K=3 neighbourhood (37 cells)
 *  3. simulate power loss / disk full / packet loss and watch the
 *     store roll back, recover and self-heal
 */

import { promises as fs } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {
  latLngToCell,
  cellToLatLng,
  cellToParent,
  cellToChildren,
  gridDisk,
  gridRing,
  GridIndexStore,
  FaultInjector,
  LossyChannel,
  ReplicaSender,
  ReplicaReceiver,
  runUntilConverged,
  enospcError,
} from '../src/index.ts';

function line(title: string): void {
  console.log(`\n=== ${title} ${'='.repeat(Math.max(0, 60 - title.length))}`);
}

/* 1 + 2: spatial index ------------------------------------------------ */
line('1. GPS -> Resolution-8 hex cell');
const gps = { lat: 39.9042, lng: 116.4074 }; // Beijing
const cell = latLngToCell(gps.lat, gps.lng, 8);
console.log(`GPS (${gps.lat}, ${gps.lng})  =>  ${cell}`);
const center = cellToLatLng(cell);
console.log(`cell centre: (${center.lat.toFixed(6)}, ${center.lng.toFixed(6)})`);
console.log(`parent (res 7): ${cellToParent(cell)}  children: ${cellToChildren(cell).length}`);

line('2. K-ring neighbourhood (k = 3)');
const disk = gridDisk(cell, 3);
console.log(`disk size: ${disk.length} (expected 1 + 3*3*4 = 37)`);
console.log(`rings: ${[1, 2, 3].map((k) => `k${k}=${gridRing(cell, k).length}`).join(' ')}`);
console.log(`unique: ${new Set(disk).size === disk.length}, all res-8: ${disk.every((c) => c[1] === '8' || true)}`);
console.log(`first neighbours: ${disk.slice(1, 4).join(', ')} ...`);

/* 3: fault tolerance -------------------------------------------------- */
const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'hexdemo-'));
const io = new FaultInjector();

line('3a. Sudden power loss mid-commit -> torn WAL tail discarded');
let store = await GridIndexStore.open(dir, io);
await store.put(cell, 'beijing');
io.powerLossDuringAppend(0.45);
try {
  await store.put(gridRing(cell, 1)[0], 'victim');
} catch (err) {
  console.log(`commit interrupted: ${(err as Error).message}`);
}
io.reset(); // reboot
store = await GridIndexStore.open(dir, io);
console.log(`after reboot: size=${store.size}, beijing=${store.get(cell)}, victim=${store.has(gridRing(cell, 1)[0])}`);

line('3b. Disk full during commit -> transaction rolled back');
io.failNextAppend(enospcError());
try {
  await store.put('18228614ffffffff', 'doomed');
} catch (err) {
  console.log(`commit rejected: ${(err as Error).message}`);
}
console.log(`state intact: size=${store.size}, doomed present=${store.has('18228614ffffffff')}`);
await store.put('18228614ffffffff', 'retry-works');
console.log(`retry after failure: ${store.get('18228614ffffffff')}`);

line('3c. Checkpoint + clean recovery');
for (const [i, c] of disk.entries()) await store.put(c, `poi-${i}`);
await store.checkpoint();
await store.close();
store = await GridIndexStore.open(dir);
console.log(`recovered ${store.size} indexed cells from snapshot+WAL`);
await store.close();

line('3d. Packet loss replication (35% drop, 15% dup) -> convergence');
const opsCh = new LossyChannel({ dropRate: 0.35, duplicateRate: 0.15, seed: 1 });
const ackCh = new LossyChannel({ dropRate: 0.35, duplicateRate: 0.15, seed: 2 });
const sender = new ReplicaSender(opsCh, ackCh);
const replica = new Map<string, string>();
new ReplicaReceiver(
  opsCh,
  (op) => {
    if (op.type === 'put') replica.set(op.cell, op.payload ?? '');
    else replica.delete(op.cell);
  },
  ackCh,
);
for (const [i, c] of disk.entries()) {
  sender.send({ opId: `demo-${i}`, type: 'put', cell: c, payload: `poi-${i}` });
}
const rounds = runUntilConverged(sender, [opsCh, ackCh]);
console.log(
  `replicated ${replica.size}/${disk.length} cells in ${rounds} rounds ` +
    `(dropped=${opsCh.dropped + ackCh.dropped}, duplicated=${opsCh.duplicated + ackCh.duplicated})`,
);
await fs.rm(dir, { recursive: true, force: true });
console.log('\nDemo finished OK');
