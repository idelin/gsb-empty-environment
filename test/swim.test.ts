import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { SwimCluster, SwimDetector, simulate } from '../src/swim.ts';
import type { FaultEvent } from '../src/types.ts';

function crashEvent(nodeIndex: number, cycle: number, phase: number): FaultEvent {
  return {
    eventId: `crash-${nodeIndex}`,
    type: 'node-crash',
    nodeId: `node-${String(nodeIndex).padStart(2, '0')}`,
    cycle,
    phase,
  };
}

describe('SwimDetector', () => {
  test('suspects a crashed node on the first missed probe and marks it dead after confirmCycles', () => {
    const cluster = new SwimCluster(4);
    cluster.applyFault(crashEvent(1, 5, 0));
    const detector = new SwimDetector(cluster, { suspicionThreshold: 1, confirmCycles: 2 });
    const log = simulate(cluster, detector, 12);

    const nodeEvents = log.filter((e) => e.nodeId === 'node-01');
    assert.deepEqual(
      nodeEvents.map((e) => [e.cycle, e.kind]),
      [
        [5, 'suspected'],
        [7, 'marked-dead'],
      ],
    );
  });

  test('a crash after the node probe slot is detected one cycle later', () => {
    const cluster = new SwimCluster(4);
    // node-02 probes at phase 0.5; crashing at phase 0.9 means it still
    // answers in cycle 5 and the first missed probe is cycle 6.
    cluster.applyFault(crashEvent(2, 5, 0.9));
    const detector = new SwimDetector(cluster, { suspicionThreshold: 1, confirmCycles: 2 });
    const log = simulate(cluster, detector, 12);

    const nodeEvents = log.filter((e) => e.nodeId === 'node-02');
    assert.deepEqual(
      nodeEvents.map((e) => [e.cycle, e.kind]),
      [
        [6, 'suspected'],
        [8, 'marked-dead'],
      ],
    );
  });

  test('a transient packet loss is suspected then refuted, never marked dead', () => {
    const cluster = new SwimCluster(4);
    cluster.applyFault({
      eventId: 'loss-1',
      type: 'packet-loss',
      nodeId: 'node-03',
      cycle: 4,
      phase: 0,
    });
    const detector = new SwimDetector(cluster);
    const log = simulate(cluster, detector, 10);

    const nodeEvents = log.filter((e) => e.nodeId === 'node-03');
    assert.deepEqual(
      nodeEvents.map((e) => [e.cycle, e.kind]),
      [
        [4, 'suspected'],
        [5, 'refuted'],
      ],
    );
    assert.equal(detector.stateOf('node-03'), 'alive');
  });

  test('healthy members never produce detector events', () => {
    const cluster = new SwimCluster(8);
    const detector = new SwimDetector(cluster);
    const log = simulate(cluster, detector, 20);
    assert.equal(log.length, 0);
  });

  test('dead members are no longer probed', () => {
    const cluster = new SwimCluster(4);
    cluster.applyFault(crashEvent(0, 2, 0));
    const detector = new SwimDetector(cluster);
    const log = simulate(cluster, detector, 20);
    const nodeEvents = log.filter((e) => e.nodeId === 'node-00');
    assert.equal(nodeEvents.filter((e) => e.kind === 'marked-dead').length, 1);
    assert.equal(nodeEvents.length, 2);
  });
});
