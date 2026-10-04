import { test, eq, assert, assertPending } from './helpers';
import { MessageBus } from '../src/bus';
import { Origin } from '../src/origin';
import { CacheNode } from '../src/node';

// Two consecutive updates (v1 -> v2 -> v3) with their barriers.
// The node must never serve v2 once it knows barrier 3, even if v2's
// data arrives after v3's invalidation and barrier.

test('two consecutive updates cannot bypass the barrier', async () => {
  const bus = new MessageBus();
  const origin = new Origin(bus);
  const node = new CacheNode('n1');
  node.attachOrigin(origin);
  bus.attach(node);

  origin.set('cfg', 'v1');
  origin.pushData('cfg');
  bus.deliverAll();
  eq(node.installedVersion('cfg'), 1);

  // Origin performs both updates and publishes both barriers up front.
  origin.update('cfg', 'v2');       // invalidate(2)
  origin.pushData('cfg');           // data(2)
  origin.publishBarrier('cfg');     // barrier(2)
  origin.update('cfg', 'v3');       // invalidate(3)
  origin.publishBarrier('cfg');     // barrier(3)
  origin.pushData('cfg');           // data(3)

  // Adversarial delivery: both invalidations, then barrier(3), then the
  // late data(2) — before barrier(2) and data(3) show up.
  assert(bus.deliverWhere((m) => m.kind === 'invalidate' && m.version === 2));
  assert(bus.deliverWhere((m) => m.kind === 'invalidate' && m.version === 3));
  assert(bus.deliverWhere((m) => m.kind === 'barrier' && m.version === 3));
  eq(node.keyPhase('cfg'), 'BLOCKED');

  const p = node.read('cfg');

  // Late data(2) installs (it is newer than v1) but must NOT be served:
  // version 2 < known barrier 3. Serving it would bypass the barrier.
  assert(bus.deliverWhere((m) => m.kind === 'data' && m.version === 2));
  eq(node.installedVersion('cfg'), 2);
  eq(node.keyPhase('cfg'), 'BLOCKED');
  await assertPending(p);

  // The stale barrier(2) replay must not lower the barrier either.
  assert(bus.deliverWhere((m) => m.kind === 'barrier' && m.version === 2));
  eq(node.knownBarrier('cfg'), 3);
  await assertPending(p);

  // data(3) finally satisfies the barrier.
  bus.deliverAll();
  const r = await p;
  eq(r.version, 3);
  eq(r.value, 'v3');
  eq(node.keyPhase('cfg'), 'SERVING');
});

test('second update while first is still pending keeps reads waiting for the newest barrier', async () => {
  const bus = new MessageBus();
  const origin = new Origin(bus);
  const node = new CacheNode('n1');
  node.attachOrigin(origin);
  bus.attach(node);

  origin.set('cfg', 'v1');
  origin.pushData('cfg');
  bus.deliverAll();

  // Update 1 fully propagates: node serves v2.
  origin.update('cfg', 'v2');
  origin.publishBarrier('cfg');
  origin.pushData('cfg');
  bus.deliverAll();
  const r1 = await node.read('cfg');
  eq(r1.version, 2);

  // Update 2: invalidation arrives, then barrier(3), data still missing.
  origin.update('cfg', 'v3');
  origin.publishBarrier('cfg');
  bus.deliverAll();
  eq(node.keyPhase('cfg'), 'BLOCKED');

  const p = node.read('cfg');
  await assertPending(p);

  origin.pushData('cfg');
  bus.deliverAll();
  const r2 = await p;
  eq(r2.version, 3);
  eq(r2.value, 'v3');
});
