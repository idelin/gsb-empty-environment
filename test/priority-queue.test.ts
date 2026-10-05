import test from "node:test";
import assert from "node:assert/strict";
import {
  TaskPriorityQueue,
  StaleHandleError,
  type Handle,
  type BatchUpdate,
} from "../src/priority-queue.ts";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

interface OracleItem {
  id: number;
  priority: number;
  seq: bigint;
  handle: Handle;
}

/** Total order: priority first, then insertion sequence (FIFO on ties). */
function compareOracleItems(a: OracleItem, b: OracleItem): number {
  if (a.priority !== b.priority) return a.priority - b.priority;
  return a.seq < b.seq ? -1 : a.seq > b.seq ? 1 : 0;
}

function drainIds(queue: TaskPriorityQueue<number>): Array<number> {
  const out: Array<number> = [];
  for (;;) {
    const item = queue.extractMin();
    if (item === undefined) return out;
    out.push(item.value);
  }
}

// ---------------------------------------------------------------------------
// Deterministic unit tests
// ---------------------------------------------------------------------------

test("extractMin returns items in ascending priority order", () => {
  const queue = new TaskPriorityQueue<string>();
  const priorities = [5, 1, 9, -3, 0, 7, 1, -3];
  priorities.forEach((p, i) => queue.enqueue(`v${i}`, p));
  const got: Array<number> = [];
  for (;;) {
    const item = queue.extractMin();
    if (item === undefined) break;
    got.push(item.priority);
  }
  assert.deepEqual(got, [...priorities].sort((a, b) => a - b));
  assert.equal(queue.size, 0);
  assert.equal(queue.extractMin(), undefined);
});

test("equal priorities are dequeued in insertion (FIFO) order", () => {
  const queue = new TaskPriorityQueue<number>();
  for (let i = 0; i < 16; i += 1) queue.enqueue(i, 42);
  queue.enqueue(-1, 41); // jumps ahead of all ties
  queue.enqueue(16, 42);
  assert.deepEqual(drainIds(queue), [-1, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16]);
});

test("handle carries slot and generation", () => {
  const queue = new TaskPriorityQueue<number>();
  const handle = queue.enqueue(1, 1);
  assert.equal(typeof handle.slot, "number");
  assert.equal(typeof handle.generation, "number");
  assert.equal(handle.slot, 0);
  assert.equal(handle.generation, 0);
});

test("slot reuse invalidates old handles via generation bump", () => {
  const queue = new TaskPriorityQueue<string>();
  const first = queue.enqueue("a", 1);
  assert.equal(queue.cancel(first), true);

  const second = queue.enqueue("b", 1);
  assert.equal(second.slot, first.slot, "slot should be recycled");
  assert.equal(second.generation, first.generation + 1, "generation must bump on reuse");

  assert.equal(queue.cancel(first), false, "stale handle must not cancel the new occupant");
  assert.throws(() => queue.changePriority(first, 0), StaleHandleError);
  assert.throws(
    () => queue.updateMany([{ handle: first, priority: 0 }]),
    StaleHandleError,
  );
  assert.equal(queue.size, 1);
  assert.equal(queue.extractMin()!.value, "b");
});

test("duplicate cancel: second cancel of the same handle returns false", () => {
  const queue = new TaskPriorityQueue<number>();
  const handle = queue.enqueue(1, 10);
  assert.equal(queue.cancel(handle), true);
  assert.equal(queue.cancel(handle), false, "duplicate cancel must report false");
  assert.equal(queue.cancel(handle), false, "and keep reporting false");
  assert.equal(queue.size, 0);

  // After the slot is recycled, the old handle is still rejected.
  const reused = queue.enqueue(2, 5);
  assert.equal(reused.slot, handle.slot);
  assert.equal(queue.cancel(handle), false);
  assert.equal(queue.cancel(reused), true);
});

test("cancel removes arbitrary elements and keeps the heap consistent", () => {
  const queue = new TaskPriorityQueue<number>();
  const handles: Array<Handle> = [];
  for (let i = 0; i < 50; i += 1) handles.push(queue.enqueue(i, (i * 37) % 23));
  // Cancel every third element.
  for (let i = 0; i < 50; i += 3) assert.equal(queue.cancel(handles[i]), true);
  const expected: Array<number> = [];
  for (let i = 0; i < 50; i += 1) if (i % 3 !== 0) expected.push(i);
  const byPriority = (id: number) => (id * 37) % 23;
  expected.sort((a, b) => byPriority(a) - byPriority(b) || a - b);
  assert.deepEqual(drainIds(queue), expected);
});

test("changePriority reorders both upward and downward", () => {
  const queue = new TaskPriorityQueue<string>();
  const a = queue.enqueue("a", 10);
  queue.enqueue("b", 20);
  queue.enqueue("c", 30);
  queue.changePriority(a, 35); // demote below b and c
  assert.equal(queue.extractMin()!.value, "b");
  queue.changePriority(a, 5); // promote to the front
  assert.equal(queue.extractMin()!.value, "a");
  assert.equal(queue.extractMin()!.value, "c");
  assert.throws(() => queue.changePriority(a, 1), StaleHandleError);
});

test("changePriority rejects invalid priorities", () => {
  const queue = new TaskPriorityQueue<number>();
  const handle = queue.enqueue(1, 1);
  for (const bad of [NaN, Infinity, -Infinity]) {
    assert.throws(() => queue.changePriority(handle, bad), TypeError);
  }
  assert.equal(queue.extractMin()!.priority, 1, "priority must be untouched");
});

test("updateMany applies a valid batch atomically", () => {
  const queue = new TaskPriorityQueue<string>();
  const a = queue.enqueue("a", 100);
  const b = queue.enqueue("b", 100);
  const c = queue.enqueue("c", 100);
  queue.updateMany([
    { handle: c, priority: 1 },
    { handle: a, priority: 2 },
    { handle: b, priority: 3 },
  ]);
  assert.deepEqual(drainIds(queue), ["c", "a", "b"]);
});

test("updateMany rolls back fully when a priority is invalid mid-batch", () => {
  const queue = new TaskPriorityQueue<string>();
  const a = queue.enqueue("a", 30);
  const b = queue.enqueue("b", 20);
  const c = queue.enqueue("c", 10);
  assert.throws(
    () =>
      queue.updateMany([
        { handle: c, priority: 99 }, // would move c to the back
        { handle: a, priority: NaN }, // boom
        { handle: b, priority: 1 },
      ]),
    TypeError,
  );
  assert.equal(queue.size, 3);
  // State must be exactly as before the batch: c(10), b(20), a(30).
  assert.deepEqual(drainIds(queue), ["c", "b", "a"]);
});

test("updateMany rolls back fully on a stale handle mid-batch", () => {
  const queue = new TaskPriorityQueue<string>();
  const a = queue.enqueue("a", 30);
  const b = queue.enqueue("b", 20);
  const c = queue.enqueue("c", 10);
  assert.equal(queue.cancel(b), true);
  assert.throws(
    () =>
      queue.updateMany([
        { handle: c, priority: 99 }, // applied, then rolled back
        { handle: b, priority: 1 }, // stale -> boom
        { handle: a, priority: 1 },
      ]),
    StaleHandleError,
  );
  // Handles of the survivors must still resolve correctly after rollback:
  // cancel by handle exercises the slot -> heap-index mapping.
  assert.equal(queue.cancel(a), true);
  assert.equal(queue.extractMin()!.value, "c");
  assert.equal(queue.extractMin(), undefined);
});

test("sequence numbers beyond Number.MAX_SAFE_INTEGER keep FIFO tie order", () => {
  // 2^53 + 7 .. 2^53 + 38 are not all representable as float64; a
  // number-based sequence counter would collapse distinct sequence values
  // and scramble FIFO order. The bigint counter stays exact.
  const initialSeq = BigInt(Number.MAX_SAFE_INTEGER) + 7n;
  const queue = new TaskPriorityQueue<number>({ initialSeq });
  const count = 32;
  for (let i = 0; i < count; i += 1) queue.enqueue(i, 7); // all ties
  queue.enqueue(-1, 6); // smaller priority jumps the queue
  queue.enqueue(count, 7); // another tie at the back
  const expected = [-1, ...Array.from({ length: count }, (_, i) => i), count];
  assert.deepEqual(drainIds(queue), expected);
});

test("empty-queue edge cases", () => {
  const queue = new TaskPriorityQueue<number>();
  assert.equal(queue.isEmpty, true);
  assert.equal(queue.peekMin(), undefined);
  assert.equal(queue.extractMin(), undefined);
  assert.equal(queue.cancel({ slot: 0, generation: 0 }), false);
  assert.equal(queue.cancel({ slot: -1, generation: 0 }), false);
  assert.throws(() => queue.changePriority({ slot: 0, generation: 0 }, 1), StaleHandleError);
  assert.throws(() => queue.enqueue(1, NaN), TypeError);
  queue.updateMany([]); // empty batch is a no-op
  assert.equal(queue.size, 0);
});

// ---------------------------------------------------------------------------
// Randomized differential test against an independent sorted-list oracle
// ---------------------------------------------------------------------------

interface RoundConfig {
  seed: number;
  ops: number;
  initialSeq: bigint;
}

function runRandomRound(config: RoundConfig): void {
  const queue = new TaskPriorityQueue<number>({ initialSeq: config.initialSeq });
  const rng = mulberry32(config.seed);

  // Independent oracle: a list kept sorted by (priority, seq).
  const oracle: Array<OracleItem> = [];
  const liveIds: Array<number> = [];
  const itemsById = new Map<number, OracleItem>();
  const deadHandles: Array<Handle> = [];

  let nextSeq = config.initialSeq;
  let nextId = 1;

  const randomPriority = (): number =>
    rng() < 0.8 ? Math.floor(rng() * 11) - 5 : Math.floor(rng() * 2000) / 100 - 10;

  const pickLiveId = (): number | undefined => {
    if (liveIds.length === 0) return undefined;
    return liveIds[Math.floor(rng() * liveIds.length)];
  };

  const removeFromOracle = (id: number): void => {
    const index = liveIds.indexOf(id);
    assert.notEqual(index, -1, "oracle bookkeeping drifted");
    liveIds.splice(index, 1);
    const item = itemsById.get(id)!;
    itemsById.delete(id);
    const oracleIndex = oracle.indexOf(item);
    assert.notEqual(oracleIndex, -1, "oracle bookkeeping drifted");
    oracle.splice(oracleIndex, 1);
    deadHandles.push(item.handle);
    if (deadHandles.length > 64) deadHandles.shift();
  };

  const checkInvariants = (): void => {
    assert.equal(queue.size, liveIds.length, "size mismatch vs oracle");
    const expectedTop = oracle[0];
    const peeked = queue.peekMin();
    if (expectedTop === undefined) {
      assert.equal(peeked, undefined);
    } else {
      assert.equal(peeked!.value, expectedTop.id, "peekMin mismatch vs oracle");
      assert.equal(peeked!.priority, expectedTop.priority);
    }
  };

  for (let op = 0; op < config.ops; op += 1) {
    const roll = rng();

    if (roll < 0.32 || liveIds.length === 0) {
      // enqueue
      const id = nextId;
      nextId += 1;
      const priority = randomPriority();
      const handle = queue.enqueue(id, priority);
      const item: OracleItem = { id, priority, seq: nextSeq, handle };
      nextSeq += 1n;
      itemsById.set(id, item);
      liveIds.push(id);
      oracle.push(item);
      oracle.sort(compareOracleItems);
    } else if (roll < 0.47) {
      // changePriority on a live handle
      const id = pickLiveId()!;
      const item = itemsById.get(id)!;
      const priority = randomPriority();
      queue.changePriority(item.handle, priority);
      item.priority = priority;
      oracle.sort(compareOracleItems);
    } else if (roll < 0.62) {
      // cancel a live handle
      const id = pickLiveId()!;
      const item = itemsById.get(id)!;
      assert.equal(queue.cancel(item.handle), true, "cancel of live handle must succeed");
      removeFromOracle(id);
    } else if (roll < 0.68) {
      // duplicate / stale cancel
      if (deadHandles.length > 0 && rng() < 0.7) {
        const stale = deadHandles[Math.floor(rng() * deadHandles.length)];
        assert.equal(queue.cancel(stale), false, "stale cancel must report false");
        assert.equal(queue.cancel(stale), false, "duplicate stale cancel must report false");
      } else {
        const id = pickLiveId()!;
        const item = itemsById.get(id)!;
        assert.equal(queue.cancel(item.handle), true);
        assert.equal(queue.cancel(item.handle), false, "duplicate cancel must report false");
        removeFromOracle(id);
      }
    } else if (roll < 0.8) {
      // extractMin
      const got = queue.extractMin();
      const expected = oracle[0];
      assert.notEqual(expected, undefined);
      assert.equal(got!.value, expected.id, "extractMin order mismatch vs oracle");
      assert.equal(got!.priority, expected.priority);
      removeFromOracle(expected.id);
    } else if (roll < 0.9) {
      // valid updateMany batch
      const batchSize = 1 + Math.floor(rng() * Math.min(5, liveIds.length));
      const shuffled = [...liveIds].sort(() => rng() - 0.5);
      const updates: Array<BatchUpdate> = [];
      for (let i = 0; i < batchSize; i += 1) {
        updates.push({ handle: itemsById.get(shuffled[i])!.handle, priority: randomPriority() });
      }
      queue.updateMany(updates);
      for (let i = 0; i < batchSize; i += 1) {
        itemsById.get(shuffled[i])!.priority = updates[i].priority;
      }
      oracle.sort(compareOracleItems);
    } else {
      // failing updateMany batch: must throw and leave the queue untouched
      const batchSize = 1 + Math.floor(rng() * Math.min(4, liveIds.length));
      const shuffled = [...liveIds].sort(() => rng() - 0.5);
      const updates: Array<BatchUpdate> = [];
      for (let i = 0; i < batchSize; i += 1) {
        updates.push({ handle: itemsById.get(shuffled[i])!.handle, priority: randomPriority() });
      }
      const failAt = Math.floor(rng() * updates.length);
      if (rng() < 0.5 || deadHandles.length === 0) {
        updates[failAt] = { handle: updates[failAt].handle, priority: NaN };
        assert.throws(() => queue.updateMany(updates), TypeError, "batch with NaN must fail");
      } else {
        const stale = deadHandles[Math.floor(rng() * deadHandles.length)];
        updates[failAt] = { handle: stale, priority: randomPriority() };
        assert.throws(() => queue.updateMany(updates), StaleHandleError, "batch with stale handle must fail");
      }
      // Oracle intentionally not updated: the whole batch must have rolled back.
    }

    checkInvariants();
  }

  // Final drain: the queue must yield exactly the oracle's sorted order.
  while (oracle.length > 0) {
    const expected = oracle.shift()!;
    const got = queue.extractMin();
    assert.equal(got!.value, expected.id, "final drain order mismatch vs oracle");
    assert.equal(got!.priority, expected.priority);
    itemsById.delete(expected.id);
  }
  assert.equal(queue.extractMin(), undefined);
  assert.equal(queue.size, 0);
}

test("randomized operations match an independent sorted-list oracle", () => {
  const rounds: Array<RoundConfig> = [
    { seed: 1, ops: 3000, initialSeq: 0n },
    { seed: 2, ops: 3000, initialSeq: 0n },
    { seed: 3, ops: 3000, initialSeq: 1000n },
    {
      seed: 4,
      ops: 3000,
      // Sequence numbers live entirely beyond the float64 safe range.
      initialSeq: BigInt(Number.MAX_SAFE_INTEGER) * 2n + 12345n,
    },
  ];
  for (const round of rounds) {
    runRandomRound(round);
  }
});
