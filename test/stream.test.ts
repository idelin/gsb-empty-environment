import test from "node:test";
import assert from "node:assert/strict";
import { promises as fs } from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import {
  CancelledError,
  collectStream,
  encodePath,
  evaluate,
  parseQuery,
  streamQuery,
  walkEvents,
  type JsonValue,
  type TreeEvent,
} from "../src/index.ts";
import { deepTree, sorted, store } from "./helpers.ts";

const QUERIES = [
  "$",
  "$.expensive",
  "$.store.book[*].author",
  "$..author",
  "$..*",
  "$..book[?(@.price < 10)].title",
  "$..book[?(@.isbn)]",
  "$.store.book[-1].title",
  "$.store.book[1:3].author",
  "$.store.book[::-1].title",
  "$.store.book[?(@.category == 'fiction' && @.price > 10)].title",
  "$..[?(@.color)]",
  "$.store.bicycle",
  "$.missing.deep[?(@.x)]",
];

test("streaming matches the reference interpreter", async () => {
  for (const src of QUERIES) {
    const query = parseQuery(src);
    const expected = evaluate(store, query);
    const actual = await collectStream(walkEvents(store), query);
    assert.deepEqual(sorted(actual), sorted(expected), `query ${src}`);
  }
});

test("results are emitted incrementally before the stream ends", async () => {
  const doc: JsonValue = { items: [{ id: 1 }, { id: 2 }, { id: 3 }], tail: { x: 1 } };
  let consumed = 0;
  function* counted(): Generator<TreeEvent> {
    for (const ev of walkEvents(doc)) {
      consumed++;
      yield ev;
    }
  }
  const total = [...walkEvents(doc)].length;
  const it = streamQuery(counted(), parseQuery("$.items[*].id"));
  const first = await it.next();
  assert.equal(first.value?.value, 1);
  assert.ok(consumed < total, `first result after ${consumed}/${total} events`);
  for await (const _ of it) void _;
});

test("negative index is not emitted before the array closes", async () => {
  const doc: JsonValue = { arr: [1, 2, 3] };
  const events = [...walkEvents(doc)];
  const closeArr = events.findIndex(
    (e) => e.type === "close" && e.path.length === 1 && e.path[0] === "arr",
  );
  let consumed = 0;
  function* counted(): Generator<TreeEvent> {
    for (const ev of events) {
      consumed++;
      yield ev;
    }
  }
  const seen: Array<{ value: JsonValue; at: number }> = [];
  for await (const r of streamQuery(counted(), parseQuery("$.arr[-1]"))) {
    seen.push({ value: r.value, at: consumed });
  }
  assert.deepEqual(seen.map((s) => s.value), [3]);
  assert.ok(seen[0].at > closeArr, "tail result only after the array closed");
});

test("filter results wait for their element but not the whole document", async () => {
  const doc: JsonValue = { items: [{ n: 1 }, { n: 5 }, { n: 9 }], rest: [1, 2, 3] };
  let consumed = 0;
  function* counted(): Generator<TreeEvent> {
    for (const ev of walkEvents(doc)) {
      consumed++;
      yield ev;
    }
  }
  const total = [...walkEvents(doc)].length;
  const seen: Array<{ value: JsonValue; at: number }> = [];
  for await (const r of streamQuery(counted(), parseQuery("$.items[?(@.n > 4)].n"))) {
    seen.push({ value: r.value, at: consumed });
  }
  assert.deepEqual(seen.map((s) => s.value), [5, 9]);
  assert.ok(seen[0].at < total, "first match emitted before the document ended");
});

test("bounded staging spills to disk and is cleaned up", async () => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "jsonq-test-"));
  try {
    const query = parseQuery("$..book[?(@.price < 20)]");
    const expected = evaluate(store, query);
    const streamed = await collectStream(walkEvents(store), query, {
      bufferLimit: 2,
      spillDir: dir,
    });
    assert.deepEqual(sorted(streamed), sorted(expected));
    const leftovers = (await fs.readdir(dir)).filter((f) => f.startsWith("jsonq-spill-"));
    assert.deepEqual(leftovers, [], "spill directory removed after completion");
  } finally {
    await fs.rm(dir, { recursive: true, force: true });
  }
});

test("spilled results stay correct for large staged containers", async () => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "jsonq-test-"));
  try {
    const doc: JsonValue = { arr: Array.from({ length: 500 }, (_, i) => ({ n: i })) };
    const query = parseQuery("$.arr[-1]");
    const streamed = await collectStream(walkEvents(doc), query, {
      bufferLimit: 4,
      spillDir: dir,
    });
    assert.deepEqual(streamed.map((r) => r.value), [{ n: 499 }]);
    assert.deepEqual(streamed[0].path, ["arr", 499]);
    const leftovers = (await fs.readdir(dir)).filter((f) => f.startsWith("jsonq-spill-"));
    assert.deepEqual(leftovers, []);
  } finally {
    await fs.rm(dir, { recursive: true, force: true });
  }
});

test("cancellation removes temporary spill data", async () => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "jsonq-test-"));
  try {
    const doc: JsonValue = { arr: Array.from({ length: 200 }, (_, i) => ({ n: i })) };
    const controller = new AbortController();
    const it = streamQuery(walkEvents(doc), parseQuery("$.arr[-1]"), {
      bufferLimit: 2,
      spillDir: dir,
      signal: controller.signal,
    });
    const pull = it.next();
    controller.abort();
    await assert.rejects(pull, (e: unknown) => e instanceof CancelledError);
    const leftovers = (await fs.readdir(dir)).filter((f) => f.startsWith("jsonq-spill-"));
    assert.deepEqual(leftovers, [], "spill files removed after cancellation");
  } finally {
    await fs.rm(dir, { recursive: true, force: true });
  }
});

test("streaming handles deep trees and empty results", async () => {
  // nested container results are inherently quadratic in output size
  const doc = deepTree(300);
  const results = await collectStream(walkEvents(doc), parseQuery("$..a"));
  assert.equal(results.length, 300);
  const none = await collectStream(walkEvents(store), parseQuery("$.nothing.here[?(@.x)]"));
  assert.deepEqual(none, []);
  // deep field chain at depth 10000
  const chain = deepTree(10000);
  const q = "$" + ".a".repeat(10000);
  const r = await collectStream(walkEvents(chain), parseQuery(q));
  assert.equal(r.length, 1);
  assert.equal(r[0].value, 1);
});

test("streaming dedupes recursive hits on the same path", async () => {
  const doc: JsonValue = { x: 1, a: { x: 2, b: { x: 3 } } };
  const results = await collectStream(walkEvents(doc), parseQuery("$..x"));
  const keys = results.map((r) => encodePath(r.path)).sort();
  assert.deepEqual(keys, ["/a/b/x", "/a/x", "/x"]);
});
