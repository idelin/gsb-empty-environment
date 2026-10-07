import assert from "node:assert/strict";
import { History } from "./.build/history.mjs";

// ---------- tiny test harness (node:test unavailable on Node 16) ----------
const tests = [];
const test = (name, fn) => tests.push([name, fn]);

// ---------- helpers ----------
function deferred() {
  let resolve, reject;
  const promise = new Promise((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

function mulberry32(seed) {
  let a = seed >>> 0;
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// Array reference model: the history is just an array of
// { id, rev, state } plus a cursor; every operation mirrors the library.
class Model {
  constructor(capacity) {
    this.cap = capacity;
    this.stack = [];
    this.cursor = -1;
    this.nextId = 0;
  }
  push(state) {
    this.stack.length = this.cursor + 1; // drop forward branch
    this.stack.push({ id: "id" + this.nextId++, rev: 1, state });
    this.cursor = this.stack.length - 1;
    while (this.stack.length > this.cap) { this.stack.shift(); this.cursor--; }
    if (this.cursor < 0 && this.stack.length > 0) this.cursor = 0;
  }
  replace(state) {
    const e = this.stack[this.cursor];
    e.rev += 1;
    e.state = state;
  }
  back() { if (this.cursor > 0) this.cursor--; }
  forward() { if (this.cursor < this.stack.length - 1) this.cursor++; }
  applyRestore(id, rev, state) {
    const e = this.stack.find((x) => x.id === id);
    if (e === undefined || e.rev !== rev) return false;
    e.rev += 1;
    e.state = state;
    return true;
  }
}

function newPair(capacity) {
  let counter = 0;
  const model = new Model(capacity);
  const history = new History({ capacity, idFactory: () => "id" + counter++ });
  return { history, model };
}

function compare(history, model) {
  assert.deepStrictEqual(
    history.entries().map((e) => [e.id, e.revision, e.state]),
    model.stack.map((e) => [e.id, e.rev, e.state]),
    "entries diverged from model"
  );
  assert.equal(history.index, model.cursor, "cursor diverged");
  assert.equal(history.length, model.stack.length, "length diverged");
  const cur = model.cursor >= 0 ? model.stack[model.cursor] : undefined;
  if (cur === undefined) {
    assert.equal(history.current, undefined);
  } else {
    assert.deepStrictEqual(
      [history.current.id, history.current.revision, history.current.state],
      [cur.id, cur.rev, cur.state],
      "current diverged"
    );
  }
  assert.equal(history.canBack, model.cursor > 0);
  assert.equal(history.canForward, model.cursor >= 0 && model.cursor < model.stack.length - 1);
}

// ---------- deterministic scenarios ----------

test("push after back discards the forward branch", () => {
  const { history, model } = newPair(10);
  for (const s of ["a", "b", "c"]) { history.push(s); model.push(s); }
  history.back(); model.back();
  history.back(); model.back();
  compare(history, model);
  history.push("d"); model.push("d");
  compare(history, model);
  assert.equal(history.canForward, false);
  assert.deepStrictEqual(history.entries().map((e) => e.state), ["a", "d"]);
  // ids of truncated entries are gone for good
  assert.deepStrictEqual(history.entries().map((e) => e.id), ["id0", "id3"]);
});

test("replace keeps the entry id and increments its revision", () => {
  const history = new History();
  const first = history.push("a");
  const second = history.replace("b");
  assert.equal(second.id, first.id);
  assert.equal(second.revision, first.revision + 1);
  assert.equal(second.state, "b");
  const third = history.replace("c");
  assert.equal(third.id, first.id);
  assert.equal(third.revision, first.revision + 2);
  assert.equal(history.length, 1);
});

test("replace on an empty history throws", () => {
  const history = new History();
  assert.throws(() => history.replace("x"), /empty/);
});

test("consecutive back/forward clamps at both ends", () => {
  const { history, model } = newPair(10);
  for (let i = 0; i < 5; i++) { history.push("s" + i); model.push("s" + i); }
  for (let i = 0; i < 8; i++) { history.back(); model.back(); } // past the bottom
  compare(history, model);
  assert.equal(history.current.state, "s0");
  assert.equal(history.index, 0);
  for (let i = 0; i < 9; i++) { history.forward(); model.forward(); } // past the top
  compare(history, model);
  assert.equal(history.current.state, "s4");
  // interleaved walk
  const walk = [1, 1, -1, -1, -1, 1, -1, 1, 1, 1, -1];
  for (const step of walk) {
    if (step < 0) { history.back(); model.back(); }
    else { history.forward(); model.forward(); }
    compare(history, model);
  }
});

test("back/forward on an empty history yield undefined", () => {
  const history = new History();
  assert.equal(history.back(), undefined);
  assert.equal(history.forward(), undefined);
  assert.equal(history.current, undefined);
  assert.equal(history.index, -1);
});

test("stale restore after a replace is discarded", async () => {
  const history = new History();
  const entry = history.push("a");
  const d = deferred();
  const result = history.restore(entry, () => d.promise);
  history.replace("b"); // bumps revision -> pending restore becomes stale
  d.resolve("restored");
  assert.equal(await result, false);
  assert.equal(history.current.state, "b");
  assert.equal(history.current.revision, entry.revision + 1);
});

test("restore applies when the revision is unchanged", async () => {
  const history = new History();
  const entry = history.push("a");
  const d = deferred();
  const result = history.restore(entry, () => d.promise);
  d.resolve("restored");
  assert.equal(await result, true);
  assert.equal(history.current.state, "restored");
  assert.equal(history.current.id, entry.id);
  assert.equal(history.current.revision, entry.revision + 1);
});

test("restore supports a synchronous loader", async () => {
  const history = new History();
  const entry = history.push("a");
  assert.equal(await history.restore(entry, () => "sync"), true);
  assert.equal(history.current.state, "sync");
});

test("restore targets a non-current entry regardless of cursor moves", async () => {
  const history = new History();
  const first = history.push("a");
  history.push("b");
  const d = deferred();
  const result = history.restore(first, () => d.promise);
  history.back();    // cursor moves onto the restore target
  history.forward(); // and away again
  d.resolve("A");
  assert.equal(await result, true);
  const entries = history.entries();
  assert.equal(entries[0].state, "A");
  assert.equal(entries[0].revision, first.revision + 1);
  assert.equal(history.current.state, "b"); // current untouched
});

test("stale restore after forward-branch truncation is discarded", async () => {
  const history = new History();
  history.push("a");
  history.push("b");
  const tail = history.push("c");
  history.back();
  history.back();
  const d = deferred();
  const result = history.restore(tail, () => d.promise);
  history.push("d"); // truncates b and c
  d.resolve("restored");
  assert.equal(await result, false);
  assert.deepStrictEqual(history.entries().map((e) => e.state), ["a", "d"]);
});

test("stale restore after capacity trim is discarded", async () => {
  const history = new History({ capacity: 2 });
  const oldest = history.push("a");
  history.push("b");
  const d = deferred();
  const result = history.restore(oldest, () => d.promise);
  history.push("c"); // trims "a"
  d.resolve("restored");
  assert.equal(await result, false);
  assert.deepStrictEqual(history.entries().map((e) => e.state), ["b", "c"]);
});

test("concurrent restores on one entry: first resolution wins", async () => {
  const history = new History();
  const entry = history.push("a");
  const d1 = deferred();
  const d2 = deferred();
  const r1 = history.restore(entry, () => d1.promise);
  const r2 = history.restore(entry, () => d2.promise);
  d1.resolve("first");
  assert.equal(await r1, true); // applied, revision bumped
  d2.resolve("second");
  assert.equal(await r2, false); // now stale
  assert.equal(history.current.state, "first");
  assert.equal(history.current.revision, entry.revision + 1);
});

test("capacity trimming drops the oldest entries", () => {
  const { history, model } = newPair(3);
  for (let i = 0; i < 6; i++) { history.push("s" + i); model.push("s" + i); }
  compare(history, model);
  assert.deepStrictEqual(history.entries().map((e) => e.state), ["s3", "s4", "s5"]);
  assert.equal(history.index, 2);
  // consecutive backs stop at the oldest retained entry
  for (let i = 0; i < 5; i++) { history.back(); model.back(); }
  compare(history, model);
  assert.equal(history.current.state, "s3");
  assert.equal(history.index, 0);
  history.forward(); model.forward();
  compare(history, model);
  assert.equal(history.current.state, "s4");
});

test("capacity of one keeps only the newest entry", () => {
  const { history, model } = newPair(1);
  history.push("a"); model.push("a");
  history.push("b"); model.push("b");
  compare(history, model);
  assert.equal(history.length, 1);
  assert.equal(history.canBack, false);
  assert.equal(history.current.state, "b");
});

test("invalid capacity is rejected", () => {
  assert.throws(() => new History({ capacity: 0 }), /capacity/);
  assert.throws(() => new History({ capacity: -2 }), /capacity/);
});

test("returned entries are snapshots, not live references", () => {
  const history = new History();
  history.push("a");
  const cur = history.current;
  cur.state = "hacked";
  cur.revision = 99;
  const listed = history.entries();
  listed[0].state = "hacked";
  assert.equal(history.current.state, "a");
  assert.equal(history.current.revision, 1);
});

test("restore loader rejection propagates and leaves state intact", async () => {
  const history = new History();
  history.push("a");
  const entry = history.current;
  await assert.rejects(
    history.restore(entry, () => Promise.reject(new Error("boom"))),
    /boom/
  );
  assert.equal(history.current.state, "a");
  assert.equal(history.current.revision, entry.revision);
});

// ---------- randomized model-based fuzz ----------

test("fuzz: randomized operations match the array reference model", async () => {
  const configs = [[11, 1], [22, 2], [33, 3], [44, 7], [55, Infinity]];
  let appliedCount = 0;
  let staleCount = 0;

  for (const [seed, capacity] of configs) {
    const rand = mulberry32(seed);
    const { history, model } = newPair(capacity);
    const pending = [];
    const refPool = [];
    let valueCounter = 0;
    const nextValue = () => "s" + seed + "v" + valueCounter++;

    const flush = async (all) => {
      const ready = [];
      for (const r of pending.splice(0)) {
        if (all || rand() < 0.5) ready.push(r);
        else pending.push(r);
      }
      for (const r of ready) r.deferred.resolve(r.value);
      for (const r of ready) {
        const applied = await r.result;
        const expected = model.applyRestore(r.id, r.revision, r.value);
        assert.equal(applied, expected, "restore outcome diverged from model");
        if (applied) appliedCount++;
        else staleCount++;
      }
    };

    for (let step = 0; step < 300; step++) {
      const r = rand();
      if (r < 0.35) {
        const v = nextValue();
        history.push(v); model.push(v);
      } else if (r < 0.5) {
        if (model.cursor >= 0) {
          const v = nextValue();
          history.replace(v); model.replace(v);
        }
      } else if (r < 0.7) {
        history.back(); model.back();
      } else if (r < 0.9) {
        history.forward(); model.forward();
      } else if (refPool.length > 0) {
        const ref = refPool[Math.floor(rand() * refPool.length)];
        const d = deferred();
        const value = nextValue();
        const result = history.restore(ref, () => d.promise);
        pending.push({ deferred: d, result, id: ref.id, revision: ref.revision, value });
      }
      // refresh the ref pool with the currently visible entries
      refPool.length = 0;
      for (const e of history.entries()) refPool.push({ id: e.id, revision: e.revision });
      if (rand() < 0.3) await flush(false);
      compare(history, model);
    }
    await flush(true);
    compare(history, model);
  }

  // the fuzz must actually exercise both restore outcomes
  assert.ok(appliedCount > 0, "expected some restores to apply");
  assert.ok(staleCount > 0, "expected some restores to be stale");
  console.log(`      (restores applied: ${appliedCount}, stale: ${staleCount})`);
});

// ---------- run ----------
let failed = 0;
for (const [name, fn] of tests) {
  try {
    await fn();
    console.log("ok   - " + name);
  } catch (err) {
    failed++;
    console.error("FAIL - " + name);
    console.error(err);
  }
}
console.log(`\n${tests.length - failed}/${tests.length} tests passed`);
process.exit(failed === 0 ? 0 : 1);
