import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Network } from '../src/network.ts';
import { buildTopology } from '../src/topology.ts';

function convergedNetwork(): Network {
  const net = new Network();
  buildTopology(net);
  const t = net.runUntilConverged();
  assert.ok(t > 0 && t <= 5, `initial convergence took ${t} ticks`);
  assert.deepEqual(net.checkAllPairs(), []);
  return net;
}

test('initial convergence: all 8x7 pairs reachable, loop-free', () => {
  const net = convergedNetwork();
  assert.equal(net.checkAllPairs().length, 0);
  const n1 = net.nodes.get('N1');
  assert.ok(n1);
  assert.equal(n1.fib.size, 7);
  assert.equal(n1.fib.get('N5')?.cost, 3, 'N1->N5 via primary backbone N3-N5');
});

test('core backbone cut: converges within a few ticks, no transient loops or blackholes', () => {
  const net = convergedNetwork();
  const cutTick = net.tick;
  net.cutLink('N3', 'N5');

  const violations: string[] = [];
  let convergedTick = -1;
  for (let i = 0; i < 10; i++) {
    net.step();
    for (const v of net.checkAllPairs()) violations.push(`tick ${net.tick}: ${v}`);
    if (net.isConverged()) {
      convergedTick = net.tick;
      break;
    }
  }

  assert.deepEqual(violations, [], 'no transient routing loops or blackholes at any tick boundary');
  assert.ok(convergedTick > 0, 'network reconverged');
  assert.ok(convergedTick - cutTick <= 2, `converged in ${convergedTick - cutTick} ticks`);

  const n1 = net.nodes.get('N1');
  assert.ok(n1);
  assert.equal(n1.fib.get('N5')?.cost, 4, 'N1->N5 rerouted via backup backbone N4-N8');

  const trace = net.traceRoute('N1', 'N5');
  assert.ok(trace.ok);
  for (let i = 0; i + 1 < trace.path.length; i++) {
    assert.notDeepEqual(
      [trace.path[i], trace.path[i + 1]].sort(),
      ['N3', 'N5'],
      'path must not traverse the cut link',
    );
  }

  assert.ok(
    net.log.entries.some((e) => e.message.includes('incremental')),
    'incremental SPF was used for reconvergence',
  );
});

test('metric change triggers reconvergence onto the alternate path', () => {
  const net = convergedNetwork();
  net.cutLink('N3', 'N5');
  net.runUntilConverged();
  assert.equal(net.nodes.get('N1')?.fib.get('N5')?.cost, 4);

  net.setLinkCost('N4', 'N8', 10);
  net.runUntilConverged();
  assert.deepEqual(net.checkAllPairs(), []);
  const n1 = net.nodes.get('N1');
  assert.ok(n1);
  assert.equal(n1.fib.get('N5')?.cost, 7, 'N1->N5 now via long-haul N2-N6');
  assert.equal(n1.fib.get('N5')?.nextHop, 'N2');
});

test('link flapping triggers route oscillation avoidance (SPF hold-down backoff)', () => {
  const net = convergedNetwork();
  const n2 = net.nodes.get('N2');
  assert.ok(n2);
  const runsBefore = n2.spfRuns;

  const flapEvents = 12;
  for (let i = 0; i < flapEvents; i++) {
    if (i % 2 === 0) net.cutLink('N2', 'N6');
    else net.restoreLink('N2', 'N6');
    net.step();
  }

  assert.ok(
    net.log.count(/route oscillation avoidance: LSDB flapping/) > 0,
    'oscillation avoidance was logged',
  );
  assert.ok(n2.holdDown > 0, 'hold-down backoff engaged');
  assert.ok(
    n2.spfRuns - runsBefore < flapEvents,
    `SPF throttled: ${n2.spfRuns - runsBefore} runs for ${flapEvents} flap events`,
  );

  const t = net.runUntilConverged(60);
  assert.ok(t > 0, 'network stabilizes after flapping stops');
  assert.deepEqual(net.checkAllPairs(), []);
});

test('link restoration reconverges to the original optimum', () => {
  const net = convergedNetwork();
  net.cutLink('N3', 'N5');
  net.runUntilConverged();
  assert.equal(net.nodes.get('N1')?.fib.get('N5')?.cost, 4);
  net.restoreLink('N3', 'N5');
  net.runUntilConverged();
  assert.deepEqual(net.checkAllPairs(), []);
  assert.equal(net.nodes.get('N1')?.fib.get('N5')?.cost, 3);
});
