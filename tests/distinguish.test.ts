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
  a: { name: "Ada", age: 36, role: "admin" },
  b: { name: "Grace", age: 29, role: "dev" },
  c: { name: "Linus", age: 54, role: "dev" },
};

function schemas() {
  const r = buildReport(storeWith(SAMPLES));
  return {
    r,
    strict: r.candidates.find((c) => c.profile === "observed-fit")?.schema as Schema,
    gen: r.candidates.find((c) => c.profile === "generalized")?.schema as Schema,
  };
}

test("every reported distinguisher is verified against both candidates", () => {
  const { r, strict, gen } = schemas();
  assert.ok(r.distinguishers.length >= 3, "expected several distinguishers");
  for (const d of r.distinguishers) {
    assert.equal(d.verified, true, `unverified: ${JSON.stringify(d)}`);
    assert.deepEqual(validate(d.instance, gen), [], `generalized must accept ${d.axis}`);
    assert.ok(validate(d.instance, strict).length > 0, `observed-fit must reject ${d.axis}`);
    assert.deepEqual(d.acceptedBy, ["generalized"]);
    assert.equal(d.rejectedBy[0].profile, "observed-fit");
    // rejection reason carries both paths
    assert.match(d.rejectedBy[0].error.instancePath, /^\$/);
    assert.match(d.rejectedBy[0].error.rulePath, /^#\//);
  }
});

test("integer-vs-number distinguisher uses a decimal at the field path", () => {
  const { r } = schemas();
  const d = r.distinguishers.find((x) => x.axis === "integer-vs-number" && x.path === "$.age");
  assert.ok(d);
  const age = (d.instance as Record<string, unknown>).age as number;
  assert.ok(!Number.isInteger(age));
  assert.match(d.rejectedBy[0].error.rulePath, /properties\/age\/type/);
});

test("required distinguisher is an object missing exactly that key", () => {
  const { r } = schemas();
  const d = r.distinguishers.find((x) => x.axis === "required" && x.path === "$.age");
  assert.ok(d);
  assert.equal("age" in (d.instance as object), false);
  assert.match(d.rejectedBy[0].error.rulePath, /#\/required/);
});

test("enum distinguisher uses an unseen value", () => {
  const { r } = schemas();
  const d = r.distinguishers.find((x) => x.axis === "string-enum" && x.path === "$.role");
  assert.ok(d);
  assert.equal((d.instance as Record<string, unknown>).role, "__unseen__");
  assert.match(d.rejectedBy[0].error.rulePath, /properties\/role\/enum/);
});

test("numeric-range distinguisher steps just outside the observed range", () => {
  const { r } = schemas();
  const d = r.distinguishers.find((x) => x.axis === "numeric-range" && x.path === "$.age");
  assert.ok(d);
  assert.equal((d.instance as Record<string, unknown>).age, 55); // observed max 54
  assert.match(d.rejectedBy[0].error.rulePath, /properties\/age\/maximum/);
});
