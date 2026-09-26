import { test } from "node:test";
import assert from "node:assert/strict";
import { SampleStore } from "../src/evidence.ts";
import { buildReport } from "../src/report.ts";
import type { Schema } from "../src/types.ts";

function storeWith(samples: Record<string, unknown>): SampleStore {
  const s = new SampleStore();
  for (const [id, v] of Object.entries(samples)) s.add(id, v);
  return s;
}

test("decimals observed: both candidates use number, no integer claim", () => {
  const r = buildReport(storeWith({ a: { v: 1.5 }, b: { v: 2 } }));
  for (const c of r.candidates) {
    const t = (c.schema.properties?.v as Schema).type;
    assert.ok(t === "number" || (Array.isArray(t) && t.includes("number")));
  }
  assert.equal(r.uncertainties.some((u) => u.axis === "integer-vs-number"), false);
});

test("mixed types produce an evidence-forced union in both candidates", () => {
  const r = buildReport(storeWith({ a: { v: "x" }, b: { v: 1 }, c: { v: true } }));
  for (const c of r.candidates) {
    assert.deepEqual((c.schema.properties?.v as Schema).type, ["boolean", "number", "string"]);
  }
  assert.ok(r.uncertainties.some((u) => u.axis === "mixed-types"));
});

test("only-null field has unknown domain and makes no type claim", () => {
  const r = buildReport(storeWith({ a: { v: null }, b: { v: null } }));
  for (const c of r.candidates) {
    assert.equal((c.schema.properties?.v as Schema).type, undefined);
  }
  assert.ok(r.uncertainties.some((u) => u.path === "$.v" && u.axis === "domain-unknown"));
});

test("nested objects and array items are inferred recursively", () => {
  const r = buildReport(
    storeWith({
      a: { user: { age: 3 }, tags: ["x", "y"] },
      b: { user: { age: 9 }, tags: ["z"] },
    }),
  );
  const strict = r.candidates.find((c) => c.profile === "observed-fit")?.schema as Schema;
  const age = strict.properties?.user?.properties?.age as Schema;
  assert.equal(age.type, "integer");
  assert.deepEqual([age.minimum, age.maximum], [3, 9]);
  const tags = strict.properties?.tags as Schema;
  assert.deepEqual([tags.minItems, tags.maxItems], [1, 2]);
  assert.equal((tags.items as Schema).type, "string");
  assert.ok(r.distinguishers.some((d) => d.path === "$.user.age"));
  assert.ok(r.distinguishers.some((d) => d.axis === "array-length" && d.path === "$.tags"));
});

test("retraction updates the emitted range and keeps provenance", () => {
  const s = storeWith({ a: { age: 30 }, b: { age: 40 }, out: { age: 121 } });
  let r = buildReport(s);
  let strict = r.candidates.find((c) => c.profile === "observed-fit")?.schema as Schema;
  assert.equal((strict.properties?.age as Schema).maximum, 121);
  assert.equal(r.sampleCount, 3);

  s.retract("out");
  r = buildReport(s);
  strict = r.candidates.find((c) => c.profile === "observed-fit")?.schema as Schema;
  assert.equal((strict.properties?.age as Schema).maximum, 40);
  assert.equal(r.sampleCount, 2);
  assert.deepEqual(r.provenance.retracted, ["out"]);
  assert.deepEqual(r.provenance.retained.sort(), ["a", "b"]);
});

test("primitive (non-object) samples are handled", () => {
  const r = buildReport(storeWith({ a: 1, b: 2, c: 3 }));
  const strict = r.candidates.find((c) => c.profile === "observed-fit")?.schema as Schema;
  assert.equal(strict.type, "integer");
  assert.deepEqual([strict.minimum, strict.maximum], [1, 3]);
});

test("string enum candidate only when few distinct values", () => {
  const few = buildReport(storeWith({ a: { s: "x" }, b: { s: "y" } }));
  const strictFew = few.candidates.find((c) => c.profile === "observed-fit")?.schema as Schema;
  assert.deepEqual((strictFew.properties?.s as Schema).enum, ["x", "y"]);

  const many = buildReport(
    storeWith(Object.fromEntries(Array.from({ length: 8 }, (_, i) => [`s${i}`, { s: `v${i}` }]))),
  );
  const strictMany = many.candidates.find((c) => c.profile === "observed-fit")?.schema as Schema;
  assert.equal((strictMany.properties?.s as Schema).enum, undefined);
});
