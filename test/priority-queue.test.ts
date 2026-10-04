import test from "node:test";
import assert from "node:assert/strict";
import {
  PriorityQueue,
  StaleHandleError,
  type Handle,
} from "../src/priority-queue.ts";

function mulberry32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test("equal priorities are extracted in insertion order (FIFO)", () => {
  const queue = new PriorityQueue();
  for (let i = 0; i < 8; i++) queue.insert(7);
  queue.insert(3);
  assert.equal(queue.extractMin()!.seq, 8n); // priority 3 wins
  for (let i = 0; i < 8; i++) {
    assert.equal(queue.extractMin()!.seq, BigInt(i));
  }
  assert.equal(queue.extractMin(), undefined);
});

test("slot reuse invalidates old handles via generation bump", () => {
  const queue = new PriorityQueue();
  const oldHandle = queue.insert(5);
  assert.equal(queue.cancel(oldHandle), true);

  const newHandle = queue.insert(9);
  assert.equal(newHandle.slot, oldHandle.slot, "slot should be recycled");
  assert.notEqual(newHandle.generation, oldHandle.generation);

  assert.equal(queue.has(oldHandle), false);
  assert.equal(queue.has(newHandle), true);
  assert.equal(queue.cancel(oldHandle), false, "stale handle must not cancel the new entry");
  assert.throws(() => queue.setPriority(oldHandle, 1), StaleHandleError);
  assert.throws(
    () => queue.batchSetPriority([{ handle: oldHandle, priority: 1 }]),
    StaleHandleError,
  );

  assert.equal(queue.size, 1);
  assert.equal(queue.extractMin()!.priority, 9);
  queue.debugCheckInvariants();
});

test("duplicate cancel is a safe no-op", () => {
  const queue = new PriorityQueue();
  const a = queue.insert(1);
  const b = queue.insert(2);
  assert.equal(queue.cancel(a), true);
  assert.equal(queue.cancel(a), false);
  assert.equal(queue.cancel(a), false);
  assert.equal(queue.size, 1);
  assert.equal(queue.extractMin()!.priority, 2);
  assert.equal(queue.cancel(b), false, "handle of extracted entry is stale");
  queue.debugCheckInvariants();
});

test("sequence numbers stay exact beyond Number.MAX_SAFE_INTEGER", () => {
  const initialSeq = 2n ** 53n + 1n; // not representable as a double
  const queue = new PriorityQueue({ initialSeq });
  for (let i = 0; i < 5; i++) queue.insert(42);
  queue.insert(41);
  assert.equal(queue.extractMin()!.seq, initialSeq + 5n);
  for (let i = 0; i < 5; i++) {
    const entry = queue.extractMin()!;
    assert.equal(entry.seq, initialSeq + BigInt(i), "exact bigint seq, FIFO order");
    assert.ok(entry.seq > BigInt(Number.MAX_SAFE_INTEGER));
  }
});

test("NaN priority is rejected", () => {
  const queue = new PriorityQueue();
  assert.throws(() => queue.insert(NaN), TypeError);
  const handle = queue.insert(1);
  assert.throws(() => queue.setPriority(handle, NaN), TypeError);
  assert.throws(() => queue.batchSetPriority([{ handle, priority: NaN }]), TypeError);
  assert.equal(queue.size, 1);
  queue.debugCheckInvariants();
});

test("failed batch rolls back priorities and the handle index map", () => {
  const queue = new PriorityQueue();
  const handles: Handle[] = [];
  const priorities: number[] = [];
  for (let i = 0; i < 12; i++) {
    const priority = (i * 37) % 11; // heavy ties on purpose
    priorities.push(priority);
    handles.push(queue.insert(priority));
  }
  const victim = handles[3];
  assert.equal(queue.cancel(victim), true);

  const updates = handles
    .filter((_, i) => i !== 3)
    .map((handle, i) => ({ handle, priority: 1000 + i }));
  updates.splice(4, 0, { handle: victim, priority: -1 }); // stale handle mid-batch

  assert.throws(() => queue.batchSetPriority(updates), StaleHandleError);
  queue.debugCheckInvariants();

  // Every surviving entry must still resolve through its handle.
  for (let i = 0; i < handles.length; i++) {
    if (i === 3) continue;
    assert.equal(queue.has(handles[i]), true);
    queue.setPriority(handles[i], priorities[i]); // must not throw or corrupt
  }
  queue.debugCheckInvariants();

  // Drain order must match the original priorities (batch fully rolled back).
  const expected = priorities
    .map((priority, i) => ({ priority, seq: BigInt(i) }))
    .filter((_, i) => i !== 3)
    .sort((a, b) => a.priority - b.priority || Number(a.seq - b.seq));
  for (const want of expected) {
    const got = queue.extractMin()!;
    assert.equal(got.priority, want.priority);
    assert.equal(got.seq, want.seq);
  }
  assert.equal(queue.extractMin(), undefined);
});

test("successful batch applies all updates", () => {
  const queue = new PriorityQueue();
  const handles = [queue.insert(10), queue.insert(20), queue.insert(30)];
  queue.batchSetPriority([
    { handle: handles[2], priority: -5 },
    { handle: handles[0], priority: 0 },
  ]);
  assert.equal(queue.extractMin()!.priority, -5);
  assert.equal(queue.extractMin()!.priority, 0);
  assert.equal(queue.extractMin()!.priority, 20);
  queue.debugCheckInvariants();
});

interface ModelEntry {
  handle: Handle;
  priority: number;
  seq: bigint;
}

function modelMin(entries: ModelEntry[]): ModelEntry | undefined {
  let best: ModelEntry | undefined;
  for (const entry of entries) {
    if (
      best === undefined ||
      entry.priority < best.priority ||
      (entry.priority === best.priority && entry.seq < best.seq)
    ) {
      best = entry;
    }
  }
  return best;
}

function runRandomWorkload(seed: number, opCount: number, initialSeq: bigint): void {
  const rng = mulberry32(seed);
  const queue = new PriorityQueue({ initialSeq });
  const live: ModelEntry[] = [];
  const staleHandles: Handle[] = [];
  let modelSeq = initialSeq;
  const randInt = (n: number) => Math.floor(rng() * n);
  const randomPriority = () => randInt(21) - 5; // small range -> many ties

  const checkPeek = () => {
    assert.equal(queue.size, live.length, `size mismatch at seed ${seed}`);
    const want = modelMin(live);
    const got = queue.peek();
    if (want === undefined) {
      assert.equal(got, undefined);
    } else {
      assert.equal(got!.priority, want.priority);
      assert.equal(got!.seq, want.seq);
    }
  };

  for (let op = 0; op < opCount; op++) {
    const roll = rng();
    if (roll < 0.35 || live.length === 0) {
      const priority = randomPriority();
      const handle = queue.insert(priority);
      live.push({ handle, priority, seq: modelSeq });
      modelSeq += 1n;
    } else if (roll < 0.5) {
      const want = modelMin(live)!;
      const got = queue.extractMin()!;
      assert.equal(got.priority, want.priority);
      assert.equal(got.seq, want.seq);
      live.splice(live.indexOf(want), 1);
      staleHandles.push(want.handle);
    } else if (roll < 0.65) {
      if (staleHandles.length > 0 && rng() < 0.4) {
        const stale = staleHandles[randInt(staleHandles.length)];
        assert.equal(queue.cancel(stale), false, "stale/duplicate cancel must return false");
      } else {
        const victim = live[randInt(live.length)];
        assert.equal(queue.cancel(victim.handle), true);
        live.splice(live.indexOf(victim), 1);
        staleHandles.push(victim.handle);
        if (rng() < 0.3) {
          assert.equal(queue.cancel(victim.handle), false, "duplicate cancel");
        }
      }
    } else if (roll < 0.8) {
      const entry = live[randInt(live.length)];
      const priority = randomPriority();
      queue.setPriority(entry.handle, priority);
      entry.priority = priority;
    } else {
      const updates: { handle: Handle; priority: number }[] = [];
      const count = 1 + randInt(Math.min(6, live.length));
      const touched = new Set<number>();
      for (let k = 0; k < count; k++) {
        const idx = randInt(live.length);
        if (touched.has(idx)) continue;
        touched.add(idx);
        updates.push({ handle: live[idx].handle, priority: randomPriority() });
      }
      const injectFailure = updates.length > 0 && rng() < 0.35;
      if (injectFailure) {
        const kind = rng();
        const bad =
          kind < 0.4 && staleHandles.length > 0
            ? { handle: staleHandles[randInt(staleHandles.length)], priority: randomPriority() }
            : kind < 0.8
              ? { handle: { slot: 1_000_000 + randInt(1000), generation: 0 }, priority: 1 }
              : { handle: updates[0].handle, priority: NaN };
        updates.splice(randInt(updates.length + 1), 0, bad);
        assert.throws(() => queue.batchSetPriority(updates));
        queue.debugCheckInvariants();
        checkPeek(); // rollback must leave the queue matching the untouched model
      } else {
        queue.batchSetPriority(updates);
        for (const update of updates) {
          const entry = live.find((e) => e.handle === update.handle)!;
          entry.priority = update.priority;
        }
      }
    }
    if (op % 25 === 0) {
      queue.debugCheckInvariants();
      checkPeek();
    }
  }

  // Final drain: queue order must equal the independently sorted model order.
  const expected = [...live].sort(
    (a, b) => a.priority - b.priority || (a.seq < b.seq ? -1 : a.seq > b.seq ? 1 : 0),
  );
  for (const want of expected) {
    const got = queue.extractMin();
    assert.ok(got !== undefined, "queue drained earlier than model");
    assert.equal(got.priority, want.priority);
    assert.equal(got.seq, want.seq);
  }
  assert.equal(queue.extractMin(), undefined);
  queue.debugCheckInvariants();
}

test("random operations match an independent sorted-list model", () => {
  for (let seed = 1; seed <= 8; seed++) {
    runRandomWorkload(seed, 2000, 0n);
  }
});

test("random workload with sequence numbers beyond the safe-integer range", () => {
  runRandomWorkload(99, 2000, 2n ** 53n + 1n);
  runRandomWorkload(100, 2000, 2n ** 60n + 12345n);
});
