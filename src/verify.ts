import { DeliveryEvent, SlidingWindowDelivery, StepResult } from './sliding-window';

let failures = 0;

function assert(cond: boolean, message: string): void {
  if (!cond) {
    failures += 1;
    console.error(`FAIL: ${message}`);
  } else {
    console.log(`ok: ${message}`);
  }
}

function assertInvariants<T>(w: SlidingWindowDelivery<T>, windowSize: number, nextSeqUpper: number, step: string): void {
  const wm = w.getWatermark();
  assert(wm >= 0 && wm < nextSeqUpper, `[${step}] watermark ${wm} within [0, ${nextSeqUpper})`);
  assert(nextSeqUpper - 1 - wm <= windowSize, `[${step}] window not exceeded: nextSeq-1-watermark <= ${windowSize}`);
}

function scenarioOutOfOrder(): void {
  console.log('--- scenario: out-of-order completion ---');
  const w = new SlidingWindowDelivery<string>(4);
  const seqs: number[] = [];
  for (let i = 0; i < 4; i += 1) {
    const r = w.apply({ type: 'deliver', payload: `msg-${i}` });
    assert(r.accepted, `deliver ${i} accepted`);
    seqs.push(r.seq!);
  }
  assertInvariants(w, 4, 5, 'after 4 delivers');

  let r = w.apply({ type: 'complete', seq: seqs[2], generation: 1 });
  assert(r.accepted && r.watermark === 0, 'ack #3 does not move watermark past gap');
  r = w.apply({ type: 'complete', seq: seqs[1], generation: 1 });
  assert(r.accepted && r.watermark === 0, 'ack #2 still blocked by missing #1');
  r = w.apply({ type: 'complete', seq: seqs[0], generation: 1 });
  assert(r.accepted && r.watermark === 3, 'ack #1 advances watermark over contiguous 1..3');
  assertInvariants(w, 4, 5, 'after contiguous advance');

  r = w.apply({ type: 'deliver', payload: 'msg-4' });
  assert(r.accepted && r.seq === 5, 'freed window allows seq 5');
  r = w.apply({ type: 'complete', seq: 5, generation: 1 });
  assert(r.accepted && r.watermark === 3, 'ack #5 blocked by missing #4');
  r = w.apply({ type: 'complete', seq: 4, generation: 1 });
  assert(r.accepted && r.watermark === 5, 'ack #4 closes gap, watermark reaches 5');
}

function scenarioDuplicateComplete(): void {
  console.log('--- scenario: duplicate completion is idempotent ---');
  const w = new SlidingWindowDelivery<string>(2);
  w.apply({ type: 'deliver', payload: 'a' });
  w.apply({ type: 'deliver', payload: 'b' });
  const first = w.apply({ type: 'complete', seq: 1, generation: 1 });
  assert(first.accepted && first.watermark === 1, 'first ack accepted, watermark 1');
  const dup = w.apply({ type: 'complete', seq: 1, generation: 1 });
  assert(dup.accepted && dup.watermark === 1, 'duplicate ack of freed slot is a no-op');
  const dup2 = w.apply({ type: 'complete', seq: 2, generation: 1 });
  assert(dup2.accepted && dup2.watermark === 2, 'ack #2 advances watermark');
  const dup3 = w.apply({ type: 'complete', seq: 2, generation: 1 });
  assert(dup3.accepted && dup3.watermark === 2, 're-acking #2 has no side effect');
  assert(w.getRedeliveries().length === 0, 'no redeliveries produced by duplicates');
}

function scenarioTimeoutAndGenerations(): void {
  console.log('--- scenario: timeout, redelivery, stale generation ---');
  const w = new SlidingWindowDelivery<string>(3);
  w.apply({ type: 'deliver', payload: 'x' });
  w.apply({ type: 'deliver', payload: 'y' });

  let r = w.apply({ type: 'timeout', seq: 1 });
  assert(r.accepted && r.redeliveries.length === 1 && r.redeliveries[0].seq === 1, 'timeout puts seq 1 on redelivery list');

  r = w.apply({ type: 'timeout', seq: 1 });
  assert(!r.accepted && r.reason === 'not-pending', 'second timeout on failed slot rejected');

  r = w.apply({ type: 'complete', seq: 1, generation: 1 });
  assert(r.accepted && r.watermark === 0, 'ack of failed slot does not confirm (awaiting redelivery)');

  const drained = w.drainRedeliveries();
  assert(drained.length === 1 && drained[0].generation === 1, 'drained redelivery carries generation 1');
  assert(w.getRedeliveries().length === 0, 'redelivery list empty after drain');

  r = w.redeliver(1);
  assert(r.accepted, 'redeliver re-arms seq 1');

  r = w.apply({ type: 'complete', seq: 1, generation: 1 });
  assert(!r.accepted && r.reason === 'stale-generation' && r.watermark === 0, 'old-generation ack cannot confirm new delivery');

  r = w.apply({ type: 'complete', seq: 1, generation: 2 });
  assert(r.accepted && r.watermark === 1, 'current-generation ack confirms, watermark 1');

  r = w.apply({ type: 'complete', seq: 2, generation: 1 });
  assert(r.accepted && r.watermark === 2, 'seq 2 confirms normally');
}

function scenarioWindowBound(): void {
  console.log('--- scenario: window bound ---');
  const size = 3;
  const w = new SlidingWindowDelivery<string>(size);
  for (let i = 0; i < size; i += 1) {
    const r = w.apply({ type: 'deliver', payload: `m${i}` });
    assert(r.accepted, `deliver ${i + 1} within window`);
  }
  const overflow = w.apply({ type: 'deliver', payload: 'overflow' });
  assert(!overflow.accepted && overflow.reason === 'window-full', 'deliver beyond window rejected');
  assertInvariants(w, size, size + 1, 'after overflow attempt');

  w.apply({ type: 'complete', seq: 1, generation: 1 });
  const r = w.apply({ type: 'deliver', payload: 'm3' });
  assert(r.accepted && r.seq === size + 1, 'slot freed by watermark advance allows next deliver');
  assertInvariants(w, size, size + 2, 'after refill');
}

function scenarioEventStream(): void {
  console.log('--- scenario: mixed event stream with per-step invariants ---');
  const size = 4;
  const w = new SlidingWindowDelivery<number>(size);
  const events: Array<DeliveryEvent<number>> = [
    { type: 'deliver', payload: 1 },
    { type: 'deliver', payload: 2 },
    { type: 'deliver', payload: 3 },
    { type: 'complete', seq: 2, generation: 1 },
    { type: 'deliver', payload: 4 },
    { type: 'timeout', seq: 1 },
    { type: 'deliver', payload: 5 },
    { type: 'complete', seq: 3, generation: 1 },
    { type: 'complete', seq: 3, generation: 1 },
    { type: 'complete', seq: 1, generation: 1 },
  ];
  let nextSeqUpper = 1;
  events.forEach((e, i) => {
    const r: StepResult<number> = w.apply(e);
    if (e.type === 'deliver' && r.accepted) {
      nextSeqUpper += 1;
    }
    assertInvariants(w, size, nextSeqUpper, `event ${i} (${e.type})`);
  });
  assert(w.getWatermark() === 0, 'watermark stuck at 0 while seq 1 failed');
  assert(w.getRedeliveries().map((s) => s.seq).join(',') === '1', 'redelivery list holds seq 1');

  w.redeliver(1);
  const r = w.apply({ type: 'complete', seq: 1, generation: 2 });
  assert(r.accepted && r.watermark === 3, 'redelivered seq 1 confirmed at gen 2, watermark jumps to 3');
  assertInvariants(w, size, nextSeqUpper, 'final');
}

scenarioOutOfOrder();
scenarioDuplicateComplete();
scenarioTimeoutAndGenerations();
scenarioWindowBound();
scenarioEventStream();

if (failures > 0) {
  console.error(`\n${failures} assertion(s) failed`);
  process.exit(1);
}
console.log('\nAll scenarios passed.');
