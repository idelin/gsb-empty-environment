import test from "node:test";
import assert from "node:assert/strict";
import {
  IncrementalQuery,
  VersionedDocument,
  collectStream,
  evaluate,
  parseQuery,
  walkEvents,
  type Edit,
  type JsonValue,
} from "../src/index.ts";
import { mulberry32, randomTree, sorted } from "./helpers.ts";

const QUERIES = [
  "$",
  "$.a",
  "$..a",
  "$..*",
  "$.arr[*]",
  "$.arr[-1]",
  "$.arr[1:3]",
  "$.arr[::-1]",
  "$.arr[-2:]",
  "$..arr[?(@.n > 2)]",
  "$..[?(@.x == 1)]",
  "$.arr[?(@ >= 2)]",
  "$['a/b']",
  "$..['a/b']",
  "$..nested..leaf",
  "$..n",
  "$.a.b.c",
  "$..missing[?(@.q)]",
];

test("interpreter, streaming and incremental engines agree on random trees", async () => {
  const rand = mulberry32(42);
  for (let round = 0; round < 30; round++) {
    const doc = randomTree(rand);
    for (const src of QUERIES) {
      const query = parseQuery(src);
      const expected = sorted(evaluate(doc, query));

      const streamed = sorted(await collectStream(walkEvents(doc), query));
      assert.deepEqual(streamed, expected, `stream mismatch: ${src} round ${round}`);

      const vdoc = new VersionedDocument(doc);
      const inc = new IncrementalQuery(vdoc, query);
      inc.sync();
      assert.deepEqual(sorted(inc.results()), expected, `incremental mismatch: ${src} round ${round}`);
    }
  }
});

test("incremental results after random edits match a fresh evaluation", () => {
  const rand = mulberry32(7);
  for (let round = 0; round < 20; round++) {
    const doc = randomTree(rand);
    const vdoc = new VersionedDocument(doc);
    const src = QUERIES[round % QUERIES.length];
    const query = parseQuery(src);
    const inc = new IncrementalQuery(vdoc, query);
    inc.sync();

    const edits: Edit[] = [];
    const editCount = 1 + Math.floor(rand() * 3);
    for (let i = 0; i < editCount; i++) {
      edits.push(randomEdit(rand, doc));
    }
    try {
      vdoc.applyEdits(edits);
    } catch {
      continue; // invalid edit path on this tree; skip
    }
    inc.sync();
    const fresh = evaluate(vdoc.get(), query);
    assert.deepEqual(sorted(inc.results()), sorted(fresh), `round ${round} query ${src}`);
  }
});

function randomEdit(rand: () => number, doc: JsonValue): Edit {
  const path: (string | number)[] = [];
  let node = doc;
  for (;;) {
    if (Array.isArray(node) && node.length > 0 && rand() < 0.8) {
      const idx = Math.floor(rand() * node.length);
      path.push(idx);
      node = node[idx];
    } else if (
      node !== null && typeof node === "object" && !Array.isArray(node) &&
      Object.keys(node).length > 0 && rand() < 0.8
    ) {
      const keys = Object.keys(node);
      const key = keys[Math.floor(rand() * keys.length)];
      path.push(key);
      node = (node as { [key: string]: JsonValue })[key];
    } else {
      break;
    }
    if (rand() < 0.4) break;
  }
  if (path.length === 0) path.push("a");
  if (rand() < 0.5) {
    return { type: "set", path, value: rand() < 0.5 ? Math.floor(rand() * 10) : { n: 1 } };
  }
  return { type: "delete", path };
}
