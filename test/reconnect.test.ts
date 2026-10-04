import { test, eq, assert, assertPending, deferred } from './helpers';
import { MessageBus } from '../src/bus';
import { Origin } from '../src/origin';
import { CacheNode } from '../src/node';

function setup() {
  const bus = new MessageBus();
  const origin = new Origin(bus);
  const node = new CacheNode('n1');
  node.attachOrigin(origin);
  bus.attach(node);
  origin.set('cfg', 'v1');
  origin.pushData('cfg');
  bus.deliverAll();
  return { bus, origin, node };
}

test('reconnect resyncs and never serves the pre-barrier value afterwards', async () => {
  const { bus, origin, node } = setup();
  eq(node.installedVersion('cfg'), 1);

  // Partition the node, then update the world moves on: update + barrier + data.
  node.disconnect();
  origin.update('cfg', 'v2');
  origin.publishBarrier('cfg');
  origin.pushData('cfg');
  bus.deliverAll();
  assert(bus.droppedCount() >= 3, 'messages to the offline node must be dropped');
  eq(node.installedVersion('cfg'), 1);
  eq(node.knownBarrier('cfg'), 0);

  // Reconnect with a controllable resync so we can read mid-sync.
  const gate = deferred();
  node.setResyncDelay(() => gate.promise);
  const rec = node.reconnect();
  eq(node.nodePhase(), 'RESYNCING');

  // A read issued while resyncing must wait: v1 might be past a barrier
  // the node has not heard about yet.
  const p = node.read('cfg');
  await assertPending(p);

  gate.resolve();
  await rec;
  eq(node.nodePhase(), 'ONLINE');

  // The waiting read now resolves with the barrier-satisfying version.
  const r = await p;
  eq(r.version, 2);
  eq(r.value, 'v2');
  eq(node.knownBarrier('cfg'), 2);

  // And every subsequent read serves v2.
  const r2 = await node.read('cfg');
  eq(r2.version, 2);
});

test('offline node serves last known value (it cannot learn new barriers)', async () => {
  const { node } = setup();
  node.disconnect();
  const r = await node.read('cfg');
  eq(r.version, 1);
  eq(r.value, 'v1');
});

test('reconnect picks up keys created while offline', async () => {
  const { bus, origin, node } = setup();
  node.disconnect();
  origin.set('new-key', 'fresh');
  origin.pushData('new-key');
  bus.deliverAll(); // dropped

  await node.reconnect();
  const r = await node.read('new-key');
  eq(r.value, 'fresh');
  eq(r.version, 1);
});
