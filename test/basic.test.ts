import { test, eq, assert, assertPending, rejects } from './helpers';
import { MessageBus } from '../src/bus';
import { Origin } from '../src/origin';
import { CacheNode, ReadTimeoutError } from '../src/node';

function setup() {
  const bus = new MessageBus();
  const origin = new Origin(bus);
  const node = new CacheNode('n1');
  node.attachOrigin(origin);
  bus.attach(node);
  return { bus, origin, node };
}

function installV1(bus: MessageBus, origin: Origin): void {
  origin.set('cfg', 'v1');
  origin.pushData('cfg');
  bus.deliverAll();
}

test('invalidation before data: old value served until barrier, then reads wait', async () => {
  const { bus, origin, node } = setup();
  installV1(bus, origin);
  eq(node.installedVersion('cfg'), 1);

  // Invalidation arrives, data has not been pushed yet.
  origin.update('cfg', 'v2');
  bus.deliverAll();
  eq(node.keyPhase('cfg'), 'STALE_OK');

  // Pre-barrier: old value is still served.
  const r1 = await node.read('cfg');
  eq(r1.value, 'v1');
  eq(r1.version, 1);

  // Barrier published: reads must now wait for v2.
  origin.publishBarrier('cfg');
  bus.deliverAll();
  eq(node.keyPhase('cfg'), 'BLOCKED');
  const p = node.read('cfg');
  await assertPending(p);

  // Data arrives after the invalidation: read unblocks with the new value.
  origin.pushData('cfg');
  bus.deliverAll();
  const r2 = await p;
  eq(r2.value, 'v2');
  eq(r2.version, 2);
  eq(node.keyPhase('cfg'), 'SERVING');
});

test('data arriving before its invalidation still installs', async () => {
  const { bus, origin, node } = setup();
  installV1(bus, origin);

  origin.update('cfg', 'v2');
  origin.pushData('cfg');
  // Deliver data first, out of order.
  assert(bus.deliverWhere((m) => m.kind === 'data' && m.version === 2));
  eq(node.installedVersion('cfg'), 2);
  eq(node.keyPhase('cfg'), 'SERVING');

  // The now-stale invalidation must not disturb the installed version.
  bus.deliverAll();
  eq(node.keyPhase('cfg'), 'SERVING');
  const r = await node.read('cfg');
  eq(r.value, 'v2');
});

test('barrier arriving before its invalidation blocks reads immediately', async () => {
  const { bus, origin, node } = setup();
  installV1(bus, origin);

  origin.update('cfg', 'v2');
  origin.publishBarrier('cfg');
  origin.pushData('cfg');

  // Barrier first: node goes straight from SERVING to BLOCKED.
  assert(bus.deliverWhere((m) => m.kind === 'barrier'));
  eq(node.keyPhase('cfg'), 'BLOCKED');
  const p = node.read('cfg');
  await assertPending(p);

  bus.deliverAll();
  const r = await p;
  eq(r.value, 'v2');
  eq(r.version, 2);
});

test('stale and duplicate messages are ignored', async () => {
  const { bus, origin, node } = setup();
  installV1(bus, origin);
  origin.update('cfg', 'v2');
  origin.pushData('cfg');
  bus.deliverAll();
  eq(node.installedVersion('cfg'), 2);

  // Replayed old data and invalidation must not regress anything. An old
  // barrier is recorded (barriers are monotonic) but is already satisfied
  // by the installed version, so it must not affect serving.
  bus.send('n1', { kind: 'data', key: 'cfg', version: 1, value: 'v1' });
  bus.send('n1', { kind: 'barrier', key: 'cfg', version: 1 });
  bus.send('n1', { kind: 'invalidate', key: 'cfg', version: 1 });
  bus.deliverAll();
  eq(node.installedVersion('cfg'), 2);
  eq(node.knownBarrier('cfg'), 1);
  eq(node.keyPhase('cfg'), 'SERVING');
  const r = await node.read('cfg');
  eq(r.value, 'v2');
});

test('read times out when the barrier is never satisfied', async () => {
  const { bus, origin, node } = setup();
  installV1(bus, origin);
  origin.update('cfg', 'v2');
  origin.publishBarrier('cfg');
  bus.deliverAll(); // invalidation + barrier, but no data
  eq(node.keyPhase('cfg'), 'BLOCKED');

  await rejects(node.read('cfg', 40), (e) => {
    assert(e instanceof ReadTimeoutError, 'expected ReadTimeoutError');
  });
});

test('read of an unknown key waits, then resolves once data arrives', async () => {
  const { bus, origin, node } = setup();
  const p = node.read('late');
  await assertPending(p);
  origin.set('late', 'first');
  origin.pushData('late');
  bus.deliverAll();
  const r = await p;
  eq(r.value, 'first');
  eq(r.version, 1);
});
