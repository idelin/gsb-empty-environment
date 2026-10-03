import assert from 'node:assert/strict';
import { test } from 'node:test';
import { VirtualClock } from '../src/clock.ts';
import { SimulatedNetwork } from '../src/network.ts';
import { SwimNode, type SwimOptions } from '../src/swim.ts';

const NODE_IDS = ['A', 'B', 'C', 'D'] as const;

interface Cluster {
  clock: VirtualClock;
  net: SimulatedNetwork;
  nodes: Map<string, SwimNode>;
}

/**
 * Build a 4-node fully-joined cluster on a virtual clock.
 * `rng: () => 0` makes every "random" pick deterministic: A probes B first,
 * then picks C and D (in order) as indirect proxies.
 */
function makeCluster(options: SwimOptions = {}): Cluster {
  const clock = new VirtualClock();
  const net = new SimulatedNetwork(clock, 1);
  const nodes = new Map<string, SwimNode>();
  for (const id of NODE_IDS) {
    const node = new SwimNode(id, net, clock, {
      periodMs: 1000,
      ackTimeoutMs: 300,
      indirectK: 2,
      rng: () => 0,
      ...options,
    });
    node.join(NODE_IDS);
    net.register(node);
    nodes.set(id, node);
  }
  return { clock, net, nodes };
}

test('acceptance: direct A->B loss triggers K indirect probes via C/D, B stays alive', () => {
  const { clock, net, nodes } = makeCluster();
  const a = nodes.get('A')!;

  // Weak network: the direct A<->B path drops everything,
  // but the proxy paths A->C->B and A->D->B are healthy.
  net.dropLink('A', 'B');

  a.start();

  // t=1000: period tick, A pings B directly; the ping is lost.
  clock.advance(1000);
  const directPings = net
    .sentOfType('ping')
    .filter((m) => m.from === 'A' && m.to === 'B');
  assert.equal(directPings.length, 1);
  assert.equal(net.sentOfType('ack').length, 0, 'no Ack on the broken direct path');

  // t=1300: direct Ack timeout fires, A asks K=2 proxies (C and D) for help.
  clock.advance(300);
  const pingReqs = net.sentOfType('ping-req');
  assert.deepEqual(
    pingReqs.map((m) => m.to).sort(),
    ['C', 'D'],
    'exactly K=2 distinct proxies receive PingReq',
  );
  assert.ok(pingReqs.every((m) => m.from === 'A' && m.target === 'B'));

  // Proxies relay pings to B and relay B's Acks back to A, well within the period.
  clock.advance(10);
  const relayedAcks = net
    .sentOfType('ack')
    .filter((m) => m.to === 'A' && (m.target ?? m.from) === 'B');
  assert.ok(relayedAcks.length >= 1, 'A receives B\'s Ack via a proxy');
  const firstAck = net.log.find(
    (e) => e.message.type === 'ack' && e.message.to === 'A',
  )!;
  assert.ok(
    firstAck.at < 2000,
    `Ack arrives before the end of the period (at t=${firstAck.at}ms)`,
  );

  // t=2000: end of the period. B must NOT be marked suspect/dead.
  clock.advance(700);
  assert.equal(a.stateOf('B'), 'alive');

  // Long after the suspicion timeout, B is still not marked Down:
  // every period the indirect path keeps refuting the false positive.
  clock.advance(10_000);
  assert.equal(a.stateOf('B'), 'alive');
});

test('healthy direct path: Ack without any PingReq', () => {
  const { clock, net, nodes } = makeCluster();
  const a = nodes.get('A')!;

  a.start();
  clock.advance(2000);

  assert.equal(net.sentOfType('ping-req').length, 0, 'no indirect probes needed');
  assert.equal(a.stateOf('B'), 'alive');
  assert.equal(a.stateOf('C'), 'alive');
  assert.equal(a.stateOf('D'), 'alive');
});

test('total isolation: target becomes suspect, then dead after suspicion timeout', () => {
  const { clock, net, nodes } = makeCluster();
  const a = nodes.get('A')!;

  // B is unreachable through every path (direct and via both proxies).
  net.dropLink('A', 'B');
  net.dropLink('C', 'B');
  net.dropLink('D', 'B');

  a.start();

  // First period completes with no Ack at all => B is suspected.
  clock.advance(2000);
  assert.equal(net.sentOfType('ping-req').length, 2, 'indirect probes were attempted');
  assert.equal(a.stateOf('B'), 'suspect');

  // Suspicion timeout (2 periods) elapses without a refuting Ack => dead.
  clock.advance(2000);
  assert.equal(a.stateOf('B'), 'dead');
});

test('K is capped by the number of available proxy members', () => {
  const { clock, net, nodes } = makeCluster({ indirectK: 10 });
  const a = nodes.get('A')!;

  net.dropLink('A', 'B');
  a.start();
  clock.advance(1300);

  const proxies = net.sentOfType('ping-req').map((m) => m.to);
  assert.deepEqual(
    proxies.sort(),
    ['C', 'D'],
    'only the 2 non-target members are used as proxies',
  );
  assert.equal(new Set(proxies).size, proxies.length, 'proxies are distinct');
});
