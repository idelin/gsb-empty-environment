import { test, eq, assertPending, deferred } from './helpers';
import { MessageBus } from '../src/bus';
import { Origin } from '../src/origin';
import { CacheNode } from '../src/node';

// "Late old load": a read whose underlying cache load started before the
// barrier arrived must not return the stale value when it completes.
// The read is re-validated at completion time and falls through to wait
// for barrier-satisfying data instead.

test('late old load does not return stale value after barrier lands', async () => {
  const bus = new MessageBus();
  const origin = new Origin(bus);
  const node = new CacheNode('n1');
  node.attachOrigin(origin);
  bus.attach(node);

  origin.set('cfg', 'v1');
  origin.pushData('cfg');
  bus.deliverAll();

  // Make the simulated cache load block on a gate we control.
  const gate = deferred();
  node.setLoadDelay(() => gate.promise);

  // The read snapshots v1 and its load is now in flight...
  const p = node.read('cfg');

  // ...meanwhile the update and its barrier arrive.
  origin.update('cfg', 'v2');
  origin.publishBarrier('cfg');
  bus.deliverAll();
  eq(node.keyPhase('cfg'), 'BLOCKED');

  // The old load completes late. The read must NOT resolve with v1.
  gate.resolve();
  await assertPending(p);

  // Once v2 data arrives, the same read resolves with the new version.
  origin.pushData('cfg');
  bus.deliverAll();
  const r = await p;
  eq(r.version, 2);
  eq(r.value, 'v2');
});

test('load completing before the barrier still serves the old value', async () => {
  const bus = new MessageBus();
  const origin = new Origin(bus);
  const node = new CacheNode('n1');
  node.attachOrigin(origin);
  bus.attach(node);

  origin.set('cfg', 'v1');
  origin.pushData('cfg');
  bus.deliverAll();

  // Invalidation only (no barrier yet): old reads may complete with v1.
  origin.update('cfg', 'v2');
  bus.deliverAll();
  eq(node.keyPhase('cfg'), 'STALE_OK');
  const r = await node.read('cfg');
  eq(r.version, 1);
  eq(r.value, 'v1');
});
