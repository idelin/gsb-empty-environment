import assert from "node:assert/strict";
import test from "node:test";
import { DVVStore, VersionVector, canonicalize, entrySize } from "../src/index.ts";
import type { DVVEntry } from "../src/index.ts";

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

function gossipRound<T>(replicas: DVVStore<T>[], key: string, rand: () => number): void {
  const messages: { from: number; entry: DVVEntry<T> }[] = [];
  replicas.forEach((replica, from) => {
    messages.push({ from, entry: replica.entry(key) });
  });
  for (let i = messages.length - 1; i > 0; i -= 1) {
    const j = Math.floor(rand() * (i + 1));
    [messages[i], messages[j]] = [messages[j], messages[i]];
  }
  const duplicated = [...messages, ...messages];
  for (let i = duplicated.length - 1; i > 0; i -= 1) {
    const j = Math.floor(rand() * (i + 1));
    [duplicated[i], duplicated[j]] = [duplicated[j], duplicated[i]];
  }
  for (const message of duplicated) {
    for (const replica of replicas) {
      replica.merge(key, message.entry);
    }
  }
}

function converged<T>(replicas: DVVStore<T>[], key: string): boolean {
  const first = canonicalize(replicas[0].snapshot(key));
  return replicas.every((replica) => canonicalize(replica.snapshot(key)) === first);
}

test("sequential writes keep a single sibling and a minimal context", () => {
  const store = new DVVStore<string>("A");
  let context = VersionVector.empty();
  for (let i = 0; i < 10; i += 1) {
    const result = store.put("k", `v${i}`, context);
    context = result.context;
  }
  const entry = store.entry("k");
  assert.equal(entry.siblings.length, 1);
  assert.equal(entry.siblings[0].value, "v9");
  assert.deepEqual(entry.siblings[0].dot, { node: "A", counter: 10 });
  assert.equal(entry.context.size, 1);
  assert.equal(entrySize(entry), 2);
});

test("empty updates (same-value rewrites) never grow the version graph", () => {
  const store = new DVVStore<string>("A");
  let context = store.put("k", "same").context;
  for (let i = 0; i < 50; i += 1) {
    context = store.put("k", "same", context).context;
  }
  const entry = store.entry("k");
  assert.equal(entry.siblings.length, 1);
  assert.deepEqual(entry.siblings[0].dot, { node: "A", counter: 51 });
  assert.deepEqual(entry.context.toJSON(), { A: 51 });
  assert.equal(entrySize(entry), 2);
});

test("same-value retry with a stale read context is deduplicated (no ghost branch)", () => {
  const store = new DVVStore<string>("A");
  const staleContext = store.put("k", "initial").context;
  store.put("k", "retried", staleContext);
  for (let i = 0; i < 5; i += 1) {
    store.put("k", "retried", staleContext);
  }
  const entry = store.entry("k");
  assert.equal(entry.siblings.length, 1);
  assert.equal(entry.siblings[0].value, "retried");
  assert.deepEqual(entry.siblings[0].dot, { node: "A", counter: 7 });
  assert.deepEqual(entry.context.toJSON(), { A: 7 });
});

test("different-value concurrent overwrites are preserved as siblings", () => {
  const store = new DVVStore<string>("A");
  const base = store.put("k", "base").context;
  store.put("k", "overwrite-1", base);
  store.put("k", "overwrite-2", base);
  const entry = store.entry("k");
  assert.equal(entry.siblings.length, 2);
  assert.deepEqual(
    entry.siblings.map((sibling) => sibling.value).sort(),
    ["overwrite-1", "overwrite-2"],
  );
});

test("same dot rebroadcast is idempotent under merge", () => {
  const source = new DVVStore<string>("A");
  source.put("k", "v");
  const replica = new DVVStore<string>("B");
  const entry = source.entry("k");
  for (let i = 0; i < 10; i += 1) {
    replica.merge("k", entry);
  }
  assert.equal(replica.entry("k").siblings.length, 1);
  assert.equal(
    canonicalize(replica.snapshot("k")),
    canonicalize(source.snapshot("k")),
  );
});

test("concurrent same-value writes from different nodes collapse into one value", () => {
  const alice = new DVVStore<string>("A");
  const bob = new DVVStore<string>("B");
  alice.put("k", "identical");
  bob.put("k", "identical");
  alice.merge("k", bob.entry("k"));
  bob.merge("k", alice.entry("k"));
  const entry = alice.entry("k");
  assert.equal(entry.siblings.length, 1);
  assert.equal(entry.siblings[0].value, "identical");
  assert.deepEqual(entry.siblings[0].dot, { node: "A", counter: 1 });
  assert.deepEqual(entry.context.toJSON(), { A: 1, B: 1 });
  assert.equal(
    canonicalize(alice.snapshot("k")),
    canonicalize(bob.snapshot("k")),
  );
});

test("causal overwrite discards dominated siblings after conflict", () => {
  const alice = new DVVStore<string>("A");
  const bob = new DVVStore<string>("B");
  const base = alice.put("k", "base").context;
  alice.put("k", "from-A", base);
  bob.merge("k", alice.entry("k"));
  bob.put("k", "from-B", base);
  alice.merge("k", bob.entry("k"));
  assert.equal(alice.entry("k").siblings.length, 2);
  const resolved = alice.put("k", "resolved", alice.get("k").context);
  assert.equal(alice.entry("k").siblings.length, 1);
  assert.deepEqual(resolved.context.toJSON(), { A: 3, B: 1 });
});

test("read context supersedes everything the client has seen", () => {
  const alice = new DVVStore<string>("A");
  const bob = new DVVStore<string>("B");
  alice.put("k", "x");
  bob.put("k", "y");
  alice.merge("k", bob.entry("k"));
  const read = alice.get("k");
  assert.deepEqual([...read.values].sort(), ["x", "y"]);
  alice.put("k", "z", read.context);
  assert.deepEqual(alice.get("k").values, ["z"]);
});

test("out-of-order duplicated broadcasts converge to a minimal graph", () => {
  const rand = mulberry32(42);
  const replicas = ["A", "B", "C", "D"].map((id) => new DVVStore<string>(id));
  const [a, b, c, d] = replicas;
  const ctxA = a.put("k", "v1").context;
  for (let i = 0; i < 20; i += 1) {
    a.put("k", "v1", ctxA);
  }
  b.put("k", "v1");
  c.put("k", "v2");
  d.put("k", "v2");
  for (let round = 0; round < 4; round += 1) {
    gossipRound(replicas, "k", rand);
  }
  assert.ok(converged(replicas, "k"));
  const entry = a.entry("k");
  assert.deepEqual(
    entry.siblings.map((sibling) => sibling.value).sort(),
    ["v1", "v2"],
  );
  assert.ok(entry.context.size <= 4);
  assert.ok(entrySize(entry) <= 6);
});

test("fuzz: random leaderless workloads converge under shuffled gossip", () => {
  const rand = mulberry32(20261002);
  const nodeIds = ["A", "B", "C", "D", "E"];
  const replicas = nodeIds.map((id) => new DVVStore<string>(id));
  const values = ["alpha", "beta", "gamma"];
  for (let op = 0; op < 60; op += 1) {
    const replica = replicas[Math.floor(rand() * replicas.length)];
    const value = values[Math.floor(rand() * values.length)];
    const useReadContext = rand() < 0.5;
    const context = useReadContext ? replica.get("k").context : VersionVector.empty();
    replica.put("k", value, context);
    if (rand() < 0.3) {
      gossipRound(replicas, "k", rand);
    }
  }
  for (let round = 0; round < 5; round += 1) {
    gossipRound(replicas, "k", rand);
  }
  assert.ok(converged(replicas, "k"));
  const entry = replicas[0].entry("k");
  const distinctValues = new Set(entry.siblings.map((sibling) => sibling.value));
  assert.equal(distinctValues.size, entry.siblings.length);
  assert.ok(entry.siblings.length <= values.length);
  assert.ok(entry.context.size <= nodeIds.length);
  const before = canonicalize(replicas[0].snapshot("k"));
  gossipRound(replicas, "k", rand);
  assert.equal(canonicalize(replicas[0].snapshot("k")), before);
});

test("structured values dedupe by deep equality", () => {
  type Doc = { title: string; tags: string[] };
  const alice = new DVVStore<Doc>("A");
  const bob = new DVVStore<Doc>("B");
  alice.put("doc", { title: "spec", tags: ["a", "b"] });
  bob.put("doc", { tags: ["a", "b"], title: "spec" });
  alice.merge("doc", bob.entry("doc"));
  assert.equal(alice.entry("doc").siblings.length, 1);
  alice.put("doc", { title: "spec", tags: ["a", "b", "c"] }, alice.get("doc").context);
  bob.put("doc", { title: "other", tags: [] });
  alice.merge("doc", bob.entry("doc"));
  assert.equal(alice.entry("doc").siblings.length, 2);
});
