import assert from "node:assert/strict";
import { History } from "../dist/history.js";

const tests = [];
const test = (name, fn) => tests.push([name, fn]);

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function createModel(capacity) {
  const items = [];
  let pos = -1;
  let nextId = 1;
  return {
    push(value) {
      items.length = pos + 1;
      items.push({ id: nextId++, revision: 1, value });
      pos = items.length - 1;
      const overflow = items.length - capacity;
      if (overflow > 0) {
        items.splice(0, overflow);
        pos = Math.max(0, pos - overflow);
      }
    },
    replace(value) {
      if (pos < 0) {
        this.push(value);
        return;
      }
      const entry = items[pos];
      entry.value = value;
      entry.revision += 1;
    },
    back() {
      if (pos > 0) pos -= 1;
    },
    forward() {
      if (pos >= 0 && pos < items.length - 1) pos += 1;
    },
    restore(id, revision, value) {
      const index = items.findIndex((entry) => entry.id === id);
      if (index < 0) return false;
      const entry = items[index];
      if (entry.revision !== revision) return false;
      entry.value = value;
      entry.revision += 1;
      return true;
    },
    current() {
      return pos >= 0 ? { ...items[pos] } : undefined;
    },
    get size() {
      return items.length;
    },
    get position() {
      return pos;
    },
  };
}

function assertSameState(history, model, context) {
  assert.deepEqual(history.current(), model.current(), `${context}: current`);
  assert.equal(history.size, model.size, `${context}: size`);
  assert.equal(history.position, model.position, `${context}: position`);
  assert.equal(history.canBack(), model.position > 0, `${context}: canBack`);
  assert.equal(
    history.canForward(),
    model.position >= 0 && model.position < model.size - 1,
    `${context}: canForward`,
  );
}

test("consecutive backs stop at the oldest entry", () => {
  const history = new History();
  const model = createModel(Infinity);
  for (const value of ["a", "b", "c", "d"]) {
    history.push(value);
    model.push(value);
  }
  assert.equal(history.back().value, "c");
  assert.equal(history.back().value, "b");
  assert.equal(history.back().value, "a");
  model.back(); model.back(); model.back();
  assert.equal(history.back(), undefined);
  model.back();
  assertSameState(history, model, "bottomed out");
  assert.equal(history.forward().value, "b");
  assert.equal(history.forward().value, "c");
  model.forward(); model.forward();
  assertSameState(history, model, "walked forward");
});

test("push after back discards the forward branch", () => {
  const history = new History();
  history.push("a");
  history.push("b");
  history.push("c");
  history.back();
  const kept = history.push("d");
  assert.equal(history.size, 3);
  assert.equal(history.canForward(), false);
  assert.equal(history.forward(), undefined);
  assert.equal(history.current().value, "d");
  assert.equal(history.back().value, "b");
  assert.equal(kept.id, 4);
});

test("replace keeps the entry id and bumps the revision", () => {
  const history = new History();
  const pushed = history.push("a");
  const replaced = history.replace("a2");
  assert.equal(replaced.id, pushed.id);
  assert.equal(replaced.revision, pushed.revision + 1);
  assert.equal(history.current().value, "a2");
  const again = history.replace("a3");
  assert.equal(again.id, pushed.id);
  assert.equal(again.revision, pushed.revision + 2);
});

test("replace on an empty history behaves like push", () => {
  const history = new History();
  const entry = history.replace("a");
  assert.equal(entry.revision, 1);
  assert.equal(history.size, 1);
});

test("stale restore responses are rejected", async () => {
  const history = new History();
  history.push("a");

  const first = deferred();
  const firstResult = history.restore(() => first.promise);
  history.replace("edited");
  first.resolve("restored");
  assert.equal(await firstResult, false);
  assert.equal(history.current().value, "edited");

  const second = deferred();
  const secondResult = history.restore(() => second.promise);
  history.push("next");
  second.resolve("restored");
  assert.equal(await secondResult, true);
  history.back();
  assert.equal(history.current().value, "restored");
});

test("restore is rejected for trimmed entries and binds by id+revision", async () => {
  const history = new History(2);
  history.push("a");
  const pending = deferred();
  const result = history.restore(() => pending.promise);
  history.push("b");
  history.push("c");
  pending.resolve("restored");
  assert.equal(await result, false);
  assert.equal(history.size, 2);
  assert.equal(history.current().value, "c");

  const history2 = new History();
  history2.push("x");
  history2.push("y");
  history2.back();
  const pending2 = deferred();
  const result2 = history2.restore(() => pending2.promise);
  history2.forward();
  history2.back();
  history2.push("z");
  pending2.resolve("restored");
  assert.equal(await result2, true);
  history2.back();
  assert.equal(history2.current().value, "restored");
});

test("capacity trimming drops the oldest entries", () => {
  const history = new History(3);
  const model = createModel(3);
  for (const value of [1, 2, 3, 4, 5]) {
    history.push(value);
    model.push(value);
  }
  assertSameState(history, model, "after trim");
  assert.equal(history.current().value, 5);
  assert.equal(history.back().value, 4);
  assert.equal(history.back().value, 3);
  assert.equal(history.back(), undefined);
  model.back(); model.back(); model.back();
  assertSameState(history, model, "bottomed out after trim");
});

test("concurrent restores: first resolution wins, later one is stale", async () => {
  const history = new History();
  history.push("a");
  const first = deferred();
  const second = deferred();
  const firstResult = history.restore(() => first.promise);
  const secondResult = history.restore(() => second.promise);
  first.resolve("one");
  second.resolve("two");
  assert.equal(await firstResult, true);
  assert.equal(await secondResult, false);
  assert.equal(history.current().value, "one");
});

function mulberry32(seed) {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test("randomized differential test against the array reference model", async () => {
  for (let seed = 1; seed <= 40; seed += 1) {
    const random = mulberry32(seed);
    const capacity = 1 + Math.floor(random() * 6);
    const history = new History(capacity);
    const model = createModel(capacity);
    const pending = [];
    let valueCounter = 0;

    for (let step = 0; step < 400; step += 1) {
      const context = `seed=${seed} capacity=${capacity} step=${step}`;
      const roll = random();
      if (roll < 0.3) {
        const value = valueCounter++;
        history.push(value);
        model.push(value);
      } else if (roll < 0.45) {
        const value = valueCounter++;
        history.replace(value);
        model.replace(value);
      } else if (roll < 0.65) {
        history.back();
        model.back();
      } else if (roll < 0.8) {
        history.forward();
        model.forward();
      } else if (roll < 0.9) {
        const slot = deferred();
        let ticket = null;
        const result = history.restore((entry) => {
          ticket = { id: entry.id, revision: entry.revision };
          return slot.promise;
        });
        await new Promise((resolve) => setTimeout(resolve, 0));
        if (ticket === null) {
          assert.equal(await result, false, `${context}: restore on empty`);
        } else {
          pending.push({ slot, result, ticket, value: valueCounter++ });
        }
      } else if (pending.length > 0) {
        const index = Math.floor(random() * pending.length);
        const [{ slot, result, ticket, value }] = pending.splice(index, 1);
        slot.resolve(value);
        const applied = await result;
        const expected = model.restore(ticket.id, ticket.revision, value);
        assert.equal(applied, expected, `${context}: restore outcome`);
      }
      assertSameState(history, model, context);
    }

    for (const { slot, result, ticket, value } of pending) {
      slot.resolve(value);
      const applied = await result;
      const expected = model.restore(ticket.id, ticket.revision, value);
      assert.equal(applied, expected, "drain: restore outcome");
    }
    assertSameState(history, model, `seed=${seed} final`);
  }
});

let failures = 0;
for (const [name, fn] of tests) {
  try {
    await fn();
    console.log(`ok - ${name}`);
  } catch (error) {
    failures += 1;
    console.error(`FAIL - ${name}`);
    console.error(error);
  }
}
if (failures > 0) {
  console.error(`${failures} test(s) failed`);
  process.exit(1);
}
console.log(`${tests.length} tests passed`);
