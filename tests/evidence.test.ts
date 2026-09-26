import { test } from "node:test";
import assert from "node:assert/strict";
import { SampleStore, buildEvidence } from "../src/evidence.ts";

function storeWith(samples: Record<string, unknown>): SampleStore {
  const s = new SampleStore();
  for (const [id, v] of Object.entries(samples)) s.add(id, v);
  return s;
}

test("missing and null are counted separately, with provenance", () => {
  const s = storeWith({
    a: { email: "x@y.z" },
    b: { email: null },
    c: {},
  });
  const root = buildEvidence(s);
  const email = root?.children.get("email");
  assert.equal(email?.present, 2);
  assert.equal(email?.nullCount, 1);
  assert.equal(email?.missing, 1);
  assert.deepEqual(email?.sampleIds.null, ["b"]);
  assert.deepEqual(email?.sampleIds.missing, ["c"]);
  assert.deepEqual(email?.sampleIds.present.sort(), ["a", "b"]);
});

test("retracting an outlier shrinks the observed range", () => {
  const s = storeWith({
    a: { age: 30 },
    b: { age: 40 },
    outlier: { age: 121 },
  });
  let root = buildEvidence(s);
  let age = root?.children.get("age");
  assert.deepEqual(age?.numericMax, { value: 121, sampleIds: ["outlier"] });

  assert.equal(s.retract("outlier"), true);
  root = buildEvidence(s);
  age = root?.children.get("age");
  assert.deepEqual(age?.numericMax, { value: 40, sampleIds: ["b"] });
  assert.equal(age?.present, 2);
  assert.deepEqual(s.retractedIds(), ["outlier"]);
  assert.deepEqual(s.retainedIds().sort(), ["a", "b"]);
});

test("bound provenance lists every sample attaining the bound", () => {
  const s = storeWith({ a: { n: 5 }, b: { n: 5 }, c: { n: 2 } });
  const n = buildEvidence(s)?.children.get("n");
  assert.deepEqual(n?.numericMax?.sampleIds.sort(), ["a", "b"]);
  assert.deepEqual(n?.numericMin?.sampleIds, ["c"]);
});

test("empty store yields no evidence", () => {
  assert.equal(buildEvidence(new SampleStore()), undefined);
});

test("array element evidence is merged under []", () => {
  const s = storeWith({ a: { tags: ["x", "y"] }, b: { tags: [] } });
  const tags = buildEvidence(s)?.children.get("tags");
  assert.deepEqual(tags?.minItemsBound, { value: 0, sampleIds: ["b"] });
  assert.deepEqual(tags?.maxItemsBound, { value: 2, sampleIds: ["a"] });
  assert.equal(tags?.items?.typeCounts.string, 2);
});
