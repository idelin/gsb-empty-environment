import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DiffServScheduler } from '../src/scheduler.ts';
import { DSCP } from '../src/packet.ts';
import type { Packet, QueueId } from '../src/packet.ts';

let seq = 0;
function pkt(dscp: number, sizeBytes = 100): Packet {
  return { id: seq++, dscp, sizeBytes, arrivalTime: 0 };
}

function drain(s: DiffServScheduler): QueueId[] {
  const order: QueueId[] = [];
  for (let d = s.dequeue(); d; d = s.dequeue()) order.push(d.queue);
  return order;
}

test('EF is served with strict priority over AF and BE', () => {
  const s = new DiffServScheduler();
  s.enqueue(pkt(DSCP.CS0));
  s.enqueue(pkt(DSCP.AF41));
  s.enqueue(pkt(DSCP.EF));
  s.enqueue(pkt(DSCP.AF11));
  s.enqueue(pkt(DSCP.EF));
  assert.deepEqual(drain(s), ['EF', 'EF', 'AF4', 'AF1', 'BE']);
});

test('BE is served only when EF and all AF queues are empty', () => {
  const s = new DiffServScheduler();
  s.enqueue(pkt(DSCP.CS0));
  s.enqueue(pkt(DSCP.CS0));
  assert.equal(s.dequeue()!.queue, 'BE');
  s.enqueue(pkt(DSCP.AF21));
  s.enqueue(pkt(DSCP.CS0));
  assert.deepEqual(drain(s), ['AF2', 'BE', 'BE']);
});

test('AF classes follow smooth WRR with exact long-term ratios', () => {
  const s = new DiffServScheduler({ afWeights: { AF4: 4, AF3: 3, AF2: 2, AF1: 1 } });
  const N = 1000;
  for (let i = 0; i < N; i++) {
    s.enqueue(pkt(DSCP.AF41));
    s.enqueue(pkt(DSCP.AF31));
    s.enqueue(pkt(DSCP.AF21));
    s.enqueue(pkt(DSCP.AF11));
  }
  const order = drain(s);
  assert.equal(order.length, 4 * N);
  // The first N dequeues must follow the 4:3:2:1 weight ratio exactly.
  const window = order.slice(0, N);
  const count = (q: QueueId) => window.filter((x) => x === q).length;
  assert.equal(count('AF4'), 400);
  assert.equal(count('AF3'), 300);
  assert.equal(count('AF2'), 200);
  assert.equal(count('AF1'), 100);
  const head = order.slice(0, 10);
  assert.equal(head.filter((q) => q === 'AF4').length, 4);
  assert.equal(head.filter((q) => q === 'AF3').length, 3);
  assert.equal(head.filter((q) => q === 'AF2').length, 2);
  assert.equal(head.filter((q) => q === 'AF1').length, 1);
});

test('empty AF queues are skipped and do not consume WRR turns', () => {
  const s = new DiffServScheduler({ afWeights: { AF4: 4, AF3: 3, AF2: 2, AF1: 1 } });
  for (let i = 0; i < 400; i++) {
    s.enqueue(pkt(DSCP.AF41));
  }
  for (let i = 0; i < 100; i++) {
    s.enqueue(pkt(DSCP.AF11));
  }
  const order = drain(s);
  const af4 = order.filter((q) => q === 'AF4').length;
  const af1 = order.filter((q) => q === 'AF1').length;
  assert.equal(af4 / af1, 4);
});

test('dequeue on an empty scheduler returns undefined', () => {
  const s = new DiffServScheduler();
  assert.equal(s.dequeue(), undefined);
  assert.equal(s.size, 0);
});

test('rejects non-positive AF weights', () => {
  assert.throws(() => new DiffServScheduler({ afWeights: { AF4: 0 } }), RangeError);
  assert.throws(() => new DiffServScheduler({ afWeights: { AF1: -2 } }), RangeError);
});
