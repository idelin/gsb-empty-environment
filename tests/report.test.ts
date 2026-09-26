import { test } from "node:test";
import assert from "node:assert/strict";
import { SampleStore } from "../src/evidence.ts";
import { buildReport } from "../src/report.ts";
import { validate } from "../src/validate.ts";
import type { Schema } from "../src/types.ts";

function storeWith(samples: Record<string, unknown>): SampleStore {
  const s = new SampleStore();
  for (const [id, v] of Object.entries(samples)) s.add(id, v);
  return s;
}

const SAMPLES = {
  "u-1": { name: "Ada", age: 36, email: "ada@x.io", tags: ["admin"], active: true },
  "u-2": { name: "Grace", age: 29, email: null, tags: [], active: false },
  "u-3": { name: "Linus", age: 54, email: "l@x.io", tags: ["dev", "ops"], active: true },
  "u-4": { name: "Margaret", age: 41, active: true },
};

test("empty sample set returns unknown, not a guess", () => {
  const r = buildReport(new SampleStore());
  assert.equal(r.status, "unknown");
  assert.equal(r.candidates.length, 0);
  assert.match(r.reason ?? "", /no retained samples/);
});

test("all samples retracted also returns unknown", () => {
  const s = storeWith({ a: { x: 1 } });
  s.retract("a");
  const r = buildReport(s);
  assert.equal(r.status, "unknown");
  assert.deepEqual(r.provenance.retracted, ["a"]);
});

test("emits a bounded candidate set, ranked, all accepting retained samples", () => {
  const r = buildReport(storeWith(SAMPLES));
  assert.equal(r.status, "inferred");
  assert.equal(r.candidates.length, 2);
  // parsimony: generalized (0 inductive assumptions) ranks first
  assert.equal(r.candidates[0].profile, "generalized");
  assert.equal(r.candidates[0].inductiveAssumptions, 0);
  assert.equal(r.candidates[1].profile, "observed-fit");
  assert.ok(r.candidates[1].inductiveAssumptions > 0);
  // independent self-check: every retained sample accepted by every candidate
  for (const c of r.candidates) {
    assert.equal(c.selfCheck.allRetainedSamplesAccepted, true);
    assert.equal(c.support.accepted, 4);
    for (const [, sample] of Object.entries(SAMPLES)) {
      assert.deepEqual(validate(sample, c.schema), []);
    }
  }
});

test("integer-only field yields integer-vs-number axis, flagged inductive", () => {
  const r = buildReport(storeWith(SAMPLES));
  const u = r.uncertainties.find((x) => x.path === "$.age" && x.axis === "integer-vs-number");
  assert.ok(u, "expected integer-vs-number uncertainty");
  assert.match(u.caveat, /NOT ruled out/);
  const strict = r.candidates.find((c) => c.profile === "observed-fit")?.schema;
  const gen = r.candidates.find((c) => c.profile === "generalized")?.schema;
  assert.equal((strict?.properties?.age as Schema).type, "integer");
  assert.equal((gen?.properties?.age as Schema).type, "number");
  assert.equal((strict?.properties?.age as Schema).minimum, 29);
  assert.equal((strict?.properties?.age as Schema).maximum, 54);
  assert.equal((gen?.properties?.age as Schema).minimum, undefined);
});

test("null observed forces nullable in BOTH candidates; missing forces optional", () => {
  const r = buildReport(storeWith(SAMPLES));
  for (const c of r.candidates) {
    const email = c.schema.properties?.email as Schema;
    assert.deepEqual(email.type, ["string", "null"]);
    assert.ok(!(c.schema.required ?? []).includes("email"), "email was missing in u-4");
    assert.ok(!(c.schema.required ?? []).includes("tags"));
  }
  const strict = r.candidates.find((c) => c.profile === "observed-fit")?.schema;
  assert.deepEqual(strict?.required, ["active", "age", "name"]);
});

test("unseen properties are unknown, never forbidden", () => {
  const r = buildReport(storeWith(SAMPLES));
  for (const c of r.candidates) {
    assert.equal("additionalProperties" in c.schema, false);
    assert.deepEqual(validate({ ...SAMPLES["u-1"], futureField: 1 }, c.schema), []);
  }
  assert.ok(r.uncertainties.some((u) => u.axis === "unseen-properties"));
});

test("null never observed is reported as unknown nullability", () => {
  const r = buildReport(storeWith(SAMPLES));
  assert.ok(r.uncertainties.some((u) => u.path === "$.age" && u.axis === "nullability-unknown"));
});
