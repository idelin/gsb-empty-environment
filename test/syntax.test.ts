import test from "node:test";
import assert from "node:assert/strict";
import type { Plan } from "../src/index.ts";
import {
  admitPlan,
  enumConst,
  eq,
  idConst,
  innerJoin,
  leftJoin,
  normalizePlan,
  project,
  projection,
  scan,
  column,
  validatePlan,
  PlanSyntaxError,
} from "../src/index.ts";

test("unsupported join forms are rejected before a credential is issued", () => {
  const rawPlan = {
    kind: "join",
    joinType: "right",
    left: scan("A", "a"),
    right: scan("B", "b"),
    on: eq(column("a", "id"), column("b", "id")),
  };
  assert.throws(() => normalizePlan(rawPlan), PlanSyntaxError);

  const result = admitPlan({
    reference: scan("A", "a"),
    candidate: rawPlan as unknown as Plan,
    policy: { version: "v1", allowAll: true },
  });
  assert.equal(result.admitted, false);
});

test("two-table fragment accepts INNER and LEFT joins", () => {
  const a = scan("A", "a");
  const b = scan("B", "b");
  const inner = project(innerJoin(a, b, eq(column("a", "id"), column("b", "id"))), [
    projection("id", column("a", "id")),
  ]);
  const left = project(leftJoin(a, b, eq(column("a", "id"), column("b", "id"))), [
    projection("id", column("a", "id")),
  ]);
  validatePlan(inner);
  validatePlan(left);
  const result = admitPlan({
    reference: inner,
    candidate: inner,
    policy: { version: "v1", allowAll: true },
  });
  assert.equal(result.admitted, true);
});

test("three-table joins are outside the admitted fragment", () => {
  const a = scan("A", "a");
  const b = scan("B", "b");
  const c = scan("C", "c");
  const plan = innerJoin(
    innerJoin(a, b, eq(column("a", "id"), column("b", "id"))),
    c,
    eq(column("b", "id"), column("c", "id")),
  );
  assert.throws(() => validatePlan(plan), PlanSyntaxError);
});

test("constants remain distinct identities even when spelled the same", () => {
  const plan = project(
    scan("A", "a"),
    [projection("x", idConst("same")), projection("y", idConst("same"))],
  );
  assert.equal(idConst("same").value.kind, "id");
  assert.equal(enumConst("same").value.kind, "enum");
  const result = admitPlan({
    reference: plan,
    candidate: plan,
    policy: { version: "v1", allowAll: true },
  });
  assert.equal(result.admitted, true);
});
