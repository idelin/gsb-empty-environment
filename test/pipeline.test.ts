import test from 'node:test';
import assert from 'node:assert/strict';
import { Pipeline, runConsumer, runProducer } from '../src/pipeline.ts';
import { Tracker, TrackedItem } from '../src/tracked.ts';

function tick(): Promise<void> {
  return new Promise((resolve) => setImmediate(resolve));
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function waitFor(cond: () => boolean, what: string, timeoutMs = 5000): Promise<void> {
  const start = Date.now();
  for (;;) {
    if (cond()) return;
    if (Date.now() - start > timeoutMs) throw new Error(`waitFor timeout: ${what}`);
    await sleep(5);
  }
}

function makeSource(total: number): { pull: () => string | undefined; pulls: () => number } {
  let next = 0;
  return {
    pull: () => {
      if (next >= total) return undefined;
      next += 1;
      return `value-${next}`;
    },
    pulls: () => next,
  };
}

function releasesByItem(tracker: Tracker): Map<number, string> {
  return new Map(tracker.releases.map((r) => [r.itemId, r.by]));
}

test('happy path: every numbered item flows through 3 stages and is released once', async () => {
  const tracker = new Tracker();
  const pipeline = new Pipeline<string>(
    [
      { name: 's1', process: (item) => { item.value += '|s1'; } },
      { name: 's2', process: async (item) => { await tick(); item.value += '|s2'; } },
      { name: 's3', process: (item) => { item.value += '|s3'; } },
    ],
    [2, 1, 3, 1],
    tracker,
  );

  const total = 20;
  const source = makeSource(total);
  const consumed: Array<[number, string]> = [];
  const producerDone = runProducer(pipeline, tracker, source.pull);
  const consumerDone = runConsumer(pipeline, tracker, (item) => {
    consumed.push([item.id, item.value]);
  });

  await producerDone;
  await consumerDone;
  await pipeline.settled;

  tracker.assertNoLeaks();
  assert.equal(tracker.producedCount, total);
  assert.equal(tracker.releases.length, total);
  assert.ok(tracker.releases.every((r) => r.by === 'consumer'));
  assert.deepEqual(
    consumed.map(([id]) => id),
    Array.from({ length: total }, (_, i) => i + 1),
  );
  assert.ok(consumed.every(([, value]) => value.endsWith('|s1|s2|s3')));

  for (let id = 1; id <= total; id++) {
    const hops = tracker.handoffs
      .filter((h) => h.itemId === id)
      .map((h) => `${h.from}->${h.to}`);
    assert.deepEqual(hops, [
      'producer->ch0',
      'ch0->s1',
      's1->ch1',
      'ch1->s2',
      's2->ch2',
      'ch2->s3',
      's3->ch3',
      'ch3->consumer',
    ]);
  }
});

test('backpressure: a paused consumer progressively blocks upstream pulls', async () => {
  const tracker = new Tracker();
  const pipeline = new Pipeline<string>(
    [
      { name: 's1', process: async () => { await tick(); } },
      { name: 's2', process: async () => { await tick(); } },
      { name: 's3', process: async () => { await tick(); } },
    ],
    [1, 1, 1, 1],
    tracker,
  );

  const total = 20;
  const source = makeSource(total);
  const producerDone = runProducer(pipeline, tracker, source.pull);

  // Consumer has not started: every channel fills, every stage blocks on
  // send, and the producer blocks with one item in hand.
  await waitFor(
    () =>
      source.pulls() === 8 &&
      pipeline.channels.every((c) => c.bufferedCount === 1) &&
      pipeline.channels[0].pendingSends === 1,
    'pipeline fully congested',
  );
  // Steady state: no further pulls happen while downstream is paused.
  await sleep(50);
  assert.equal(source.pulls(), 8);

  // Resume the consumer; the whole chain unblocks and drains.
  let consumed = 0;
  const consumerDone = runConsumer(pipeline, tracker, () => { consumed += 1; });
  await producerDone;
  await consumerDone;
  await pipeline.settled;

  assert.equal(consumed, total);
  tracker.assertNoLeaks();
  assert.equal(tracker.releases.length, total);
});

test('mid-stage failure: transferred items are released by the receiving stage', async () => {
  const tracker = new Tracker();
  const boom = new Error('boom@s2');
  let openGate!: () => void;
  const gate = new Promise<void>((resolve) => { openGate = resolve; });

  const pipeline = new Pipeline<string>(
    [
      { name: 's1', process: () => {} },
      {
        name: 's2',
        process: async (item) => {
          if (item.id === 7) {
            await gate;
            throw boom;
          }
        },
      },
      { name: 's3', process: () => {} },
    ],
    [1, 2, 1, 1],
    tracker,
  );

  const total = 20;
  const source = makeSource(total);
  let consumed = 0;
  const producerDone = runProducer(pipeline, tracker, source.pull);
  const consumerDone = runConsumer(pipeline, tracker, () => { consumed += 1; });

  // Deterministic congestion behind the gated item #7:
  //   consumer released #1-#6, s2 holds #7, ch1 buffers #8,#9,
  //   s1 holds #10, ch0 buffers #11, producer holds #12 (blocked).
  await waitFor(
    () =>
      consumed === 6 &&
      source.pulls() === 12 &&
      pipeline.channels[0].pendingSends === 1,
    'congestion behind item #7',
  );
  openGate();

  await assert.rejects(producerDone);
  await assert.rejects(consumerDone);
  await assert.rejects(pipeline.settled);

  tracker.assertNoLeaks();
  assert.equal(tracker.producedCount, 12);
  assert.equal(tracker.releases.length, 12);

  const by = releasesByItem(tracker);
  for (let id = 1; id <= 6; id++) assert.equal(by.get(id), 'consumer');
  // #7 was being processed by s2; #8,#9 were already transferred into
  // ch1, so the receiving stage s2 releases them.
  for (let id = 7; id <= 9; id++) assert.equal(by.get(id), 's2');
  // #10 was held by s1 (its send rejected); #11 sat in ch0, s1's input.
  for (let id = 10; id <= 11; id++) assert.equal(by.get(id), 's1');
  // #12 was still owned by the producer when its send was interrupted.
  assert.equal(by.get(12), 'producer');
});

test('consumer early exit: abort cascades upstream and cleanup is complete', async () => {
  const tracker = new Tracker();
  const pipeline = new Pipeline<string>(
    [
      { name: 's1', process: async () => { await tick(); } },
      { name: 's2', process: async () => { await tick(); } },
      { name: 's3', process: async () => { await tick(); } },
    ],
    [1, 1, 1, 1],
    tracker,
  );

  const total = 20;
  const source = makeSource(total);
  const producerDone = runProducer(pipeline, tracker, source.pull);

  let consumed = 0;
  const earlyExit = new Error('consumer exited early');
  const consumerDone = runConsumer(pipeline, tracker, (item: TrackedItem<string>) => {
    consumed += 1;
    if (consumed === 5) pipeline.abort(earlyExit);
  });

  await assert.rejects(consumerDone);
  await assert.rejects(producerDone);
  await assert.rejects(pipeline.settled);

  tracker.assertNoLeaks();
  assert.equal(consumed, 5);
  // Producer stopped pulling: at most 5 consumed + 4 buffered +
  // 3 held by stages + 1 held by the producer.
  assert.ok(tracker.producedCount <= 13, `produced ${tracker.producedCount}`);
  assert.equal(tracker.releases.length, tracker.producedCount);

  const by = releasesByItem(tracker);
  for (let id = 1; id <= 5; id++) assert.equal(by.get(id), 'consumer');
});
