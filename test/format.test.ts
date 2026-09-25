import assert from "node:assert/strict";
import { test } from "node:test";
import { buildTree } from "../src/builder.ts";
import { PathError } from "../src/errors.ts";
import { nodeToJson } from "../src/node.ts";
import { PathInterner, formatPath, pathToArray } from "../src/path.ts";
import {
  canonical,
  decodeRecord,
  encodeRecord,
  treeToRecords,
  type PathRecord,
} from "../src/record.ts";
import { RecordStore } from "../src/store.ts";

async function buildFrom(records: PathRecord[], interner: PathInterner) {
  const store = new RecordStore({ interner });
  await store.addAll(records);
  return buildTree(store.records());
}

test("{'0':'x'} stays an object and never becomes an array", async () => {
  const interner = new PathInterner();
  const records = [...treeToRecords({ "0": "x" }, interner)];
  const { root } = await buildFrom(records, interner);
  assert.equal(root.kind, "obj");
  const json = nodeToJson(root);
  assert.equal(Array.isArray(json), false);
  assert.deepEqual(json, { "0": "x" });
});

test("numeric-like keys, null, empty containers and holes round-trip", async () => {
  const interner = new PathInterner();
  const sparse: unknown[] = new Array(3);
  sparse[1] = 1;
  const tree = {
    "0": "x",
    "10": "y",
    a: null,
    empty: {},
    list: [],
    sparse,
  } as never;
  const { root } = await buildFrom([...treeToRecords(tree, interner)], interner);
  const json = nodeToJson(root) as Record<string, unknown>;
  assert.equal(Array.isArray(json), false);
  assert.equal(json["0"], "x");
  assert.equal(json["10"], "y");
  assert.ok("a" in json && json.a === null); // null is a value, not absence
  assert.deepEqual(json.empty, {});
  assert.deepEqual(json.list, []);
  const holes = json.sparse as unknown[];
  assert.equal(holes.length, 3);
  assert.equal(0 in holes, false); // hole stays a hole
  assert.equal(holes[1], 1);
  assert.equal(2 in holes, false);
});

test("wire format encodes every record kind explicitly and losslessly", () => {
  const interner = new PathInterner();
  const cases: PathRecord[] = [
    { path: interner.root, op: "obj" },
    { path: interner.internPath([{ t: "k", k: "0" }]), op: "val", v: "x" },
    { path: interner.internPath([{ t: "k", k: "n" }]), op: "val", v: null },
    { path: interner.internPath([{ t: "k", k: "e" }]), op: "arr", n: 0 },
    { path: interner.internPath([{ t: "i", i: 2 }]), op: "val", v: 42 },
    { path: interner.internPath([{ t: "k", k: "gone" }]), op: "del" },
  ];
  const round = new PathInterner();
  for (const rec of cases) {
    const line = encodeRecord(rec);
    const back = decodeRecord(line, round, "test");
    assert.equal(formatPath(pathToArray(back.path)), formatPath(pathToArray(rec.path)));
    assert.equal(canonical(back), canonical(rec));
    assert.equal(encodeRecord(back), line);
  }
  // del is explicit on the wire; null is a val, not a del
  assert.ok(encodeRecord(cases[5]).includes('"del"'));
  assert.ok(encodeRecord(cases[2]).includes('"v":null'));
});

test("malformed records are rejected with their origin", () => {
  const interner = new PathInterner();
  assert.throws(
    () => decodeRecord("{not json", interner, "run-0000.jsonl:7"),
    (err: unknown) => {
      assert.ok(err instanceof PathError);
      assert.equal(err.kind, "MalformedRecord");
      assert.equal(err.source, "run-0000.jsonl:7");
      return true;
    },
  );
  assert.throws(
    () => decodeRecord('{"p":[["i",-1]],"op":"val","v":1}', interner, "input#3"),
    (err: unknown) => err instanceof PathError && err.kind === "MalformedRecord",
  );
});
