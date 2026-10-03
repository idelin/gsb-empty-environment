import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DiffServScheduler } from '../src/scheduler.ts';
import { DSCP, type Packet } from '../src/packet.ts';

let nextId = 1;
function pkt(dscp: number, flowId = 'f'): Packet {
  return { id: nextId++, dscp, sizeBytes: 100, arrivalMs: 0, flowId };
}

test('EF queue is drained with strict priority over AF and BE', () => {
  const s = new DiffServScheduler();
  s.enqueue(pkt(DSCP.BE));
  s.enqueue(pkt(DSCP.AF41));
  s.enqueue(pkt(DSCP.EF));
  s.enqueue(pkt(DSCP.EF));

  assert.equal(s.dequeue()?.dscp, DSCP.EF);
  assert.equal(s.dequeue()?.dscp, DSCP.EF);
  // Late-arriving EF still jumps ahead of queued AF/BE.
  s.enqueue(pkt(DSCP.EF));
  assert.equal(s.dequeue()?.dscp, DSCP.EF);
  assert.equal(s.dequeue()?.dscp, DSCP.AF41);
  assert.equal(s.dequeue()?.dscp, DSCP.BE);
  assert.equal(s.dequeue(), undefined);
});

test('BE is served only when EF and AF queues are empty', () => {
  const s = new DiffServScheduler();
  s.enqueue(pkt(DSCP.BE));
  s.enqueue(pkt(DSCP.AF11));
  assert.equal(s.dequeue()?.dscp, DSCP.AF11);
  assert.equal(s.dequeue()?.dscp, DSCP.BE);
});

test('AF classes share capacity by WRR weights', () => {
  const s = new DiffServScheduler({ afWeights: { AF1: 1, AF2: 2, AF4: 4 } });
  // Keep every queue persistently backlogged, then sample 700 dequeues.
  for (let i = 0; i < 1000; i++) {
    s.enqueue(pkt(DSCP.AF11, 'af1'));
    s.enqueue(pkt(DSCP.AF21, 'af2'));
    s.enqueue(pkt(DSCP.AF41, 'af4'));
  }
  const counts: Record<string, number> = { AF1: 0, AF2: 0, AF4: 0 };
  for (let i = 0; i < 700; i++) {
    const p = s.dequeue();
    assert.ok(p);
    const queue = p.dscp === DSCP.AF11 ? 'AF1' : p.dscp === DSCP.AF21 ? 'AF2' : 'AF4';
    counts[queue] += 1;
  }
  // Weights 1:2:4 over 700 rounds -> 100 / 200 / 400.
  assert.equal(counts.AF1, 100);
  assert.equal(counts.AF2, 200);
  assert.equal(counts.AF4, 400);
});

test('smooth WRR interleaves instead of bursting', () => {
  const s = new DiffServScheduler({ afWeights: { AF1: 1, AF4: 4 } });
  for (let i = 0; i < 10; i++) {
    s.enqueue(pkt(DSCP.AF11, 'af1'));
    s.enqueue(pkt(DSCP.AF41, 'af4'));
  }
  const order: string[] = [];
  for (let i = 0; i < 5; i++) order.push(s.dequeue()?.flowId ?? '');
  // Smooth WRR pattern: af4, af4, af1, af4, af4 (no long af1 starvation).
  assert.deepEqual(order, ['af4', 'af4', 'af1', 'af4', 'af4']);
});

test('empty AF queues are skipped without disturbing the ratio', () => {
  const s = new DiffServScheduler({ afWeights: { AF1: 1, AF2: 2, AF3: 3 } });
  for (let i = 0; i < 1000; i++) {
    s.enqueue(pkt(DSCP.AF11));
    s.enqueue(pkt(DSCP.AF31));
  }
  const counts: Record<number, number> = { [DSCP.AF11]: 0, [DSCP.AF31]: 0 };
  for (let i = 0; i < 400; i++) {
    const p = s.dequeue();
    assert.ok(p);
    counts[p.dscp] += 1;
  }
  assert.equal(counts[DSCP.AF11], 100);
  assert.equal(counts[DSCP.AF31], 300);
});

test('enqueue reports the queue each packet lands in', () => {
  const s = new DiffServScheduler();
  assert.equal(s.enqueue(pkt(DSCP.EF)), 'EF');
  assert.equal(s.enqueue(pkt(DSCP.AF22)), 'AF2');
  assert.equal(s.enqueue(pkt(DSCP.BE)), 'BE');
  assert.equal(s.backlog, 3);
  assert.equal(s.queueLength('EF'), 1);
});
