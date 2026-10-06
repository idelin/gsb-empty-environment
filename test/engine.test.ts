import assert from "node:assert/strict";
import test from "node:test";
import { SuggestionEngine } from "../src/engine.ts";
import type { SuggestionSource } from "../src/engine.ts";
import type { Candidate } from "../src/merger.ts";

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function manualSource(id: string) {
  const calls: Array<ReturnType<typeof deferred<Candidate[]>>> = [];
  const source: SuggestionSource = {
    id,
    fetch: () => {
      const gate = deferred<Candidate[]>();
      calls.push(gate);
      return gate.promise;
    },
  };
  return { source, calls };
}

test("aggregates out-of-order async returns into one sorted list", async () => {
  const engine = new SuggestionEngine();
  const slow = manualSource("slow");
  const mid = manualSource("mid");
  const fast = manualSource("fast");
  engine.registerSource(slow.source);
  engine.registerSource(mid.source);
  engine.registerSource(fast.source);

  const handle = engine.query("re");

  fast.calls[0].resolve([{ key: "rename", score: 2 }]);
  await Promise.resolve();
  assert.deepEqual(engine.list().map((s) => s.id), ["rename"]);

  mid.calls[0].resolve([
    { key: "Rename", score: 6 },
    { key: "reload", score: 4 },
  ]);
  await Promise.resolve();
  assert.deepEqual(engine.list().map((s) => s.id), ["rename", "reload"]);

  slow.calls[0].resolve([
    { key: "RENAME ", score: 9 },
    { key: "restart", score: 7 },
  ]);
  await handle.settled;

  const list = engine.list();
  assert.deepEqual(list.map((s) => s.id), ["rename", "restart", "reload"]);
  const rename = list[0];
  assert.equal(rename.score, 9);
  assert.deepEqual(rename.sources, ["fast", "mid", "slow"]);
});

test("stale generation results are discarded after a new query", async () => {
  const engine = new SuggestionEngine();
  const first = manualSource("s");
  engine.registerSource(first.source);

  const stale = engine.query("old");
  assert.equal(stale.generation, 1);

  const fresh = engine.query("new");
  assert.equal(fresh.generation, 2);

  first.calls[0].resolve([{ key: "stale-hit", score: 100 }]);
  first.calls[1].resolve([]);
  await stale.settled;
  await fresh.settled;

  assert.deepEqual(engine.list(), []);
  assert.equal(engine.accept("s", stale.generation, [{ key: "x", score: 1 }]), false);
  assert.equal(engine.accept("s", fresh.generation, [{ key: "x", score: 1 }]), true);
  assert.deepEqual(engine.list().map((s) => s.id), ["x"]);
});

test("withdrawal mid-query re-selects winners and keeps highlight identity", async () => {
  const engine = new SuggestionEngine();
  engine.registerSource({
    id: "primary",
    fetch: async () => [
      { key: "target", score: 10 },
      { key: "other", score: 1 },
    ],
  });
  engine.registerSource({
    id: "backup",
    fetch: async () => [{ key: " target", score: 5 }],
  });

  const handle = engine.query("t");
  await handle.settled;

  engine.highlight("other");
  assert.equal(engine.list()[0].score, 10);

  engine.withdraw("primary");
  const list = engine.list();
  assert.equal(list.length, 1);
  assert.equal(list[0].id, "target");
  assert.equal(list[0].score, 5);
  assert.deepEqual(list[0].sources, ["backup"]);
  assert.equal(engine.highlighted()?.id, "target");
});

test("unregisterSource withdraws its contributions", async () => {
  const engine = new SuggestionEngine();
  engine.registerSource({ id: "a", fetch: async () => [{ key: "k", score: 8 }] });
  engine.registerSource({ id: "b", fetch: async () => [{ key: "k", score: 3 }] });
  const handle = engine.query("k");
  await handle.settled;
  assert.equal(engine.list()[0].score, 8);

  engine.unregisterSource("a");
  assert.equal(engine.list()[0].score, 3);
  assert.deepEqual(engine.list()[0].sources, ["b"]);
});

test("aborted fetches from superseded queries do not throw", async () => {
  const engine = new SuggestionEngine();
  engine.registerSource({
    id: "respectful",
    fetch: (_query, signal) =>
      new Promise<Candidate[]>((_resolve, reject) => {
        signal.addEventListener("abort", () =>
          reject(new DOMException("aborted", "AbortError")),
        );
      }),
  });
  const stale = engine.query("first");
  engine.query("second");
  await stale.settled;
});
