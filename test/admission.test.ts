import test from "node:test";
import assert from "node:assert/strict";
import {
  NULL,
  checkPartitionCertificate,
  column,
  enumConst,
  eq,
  evaluatePlan,
  filter,
  idConst,
  idValue,
  innerJoin,
  isNull,
  leftJoin,
  planDigest,
  project,
  projection,
  scan,
  unionAll,
  admitPlan,
} from "../src/index.ts";

const visible = { version: "visible-v1", allowAll: true } as const;
const tenantPolicy = {
  version: "tenant-v1",
  subject: "alice",
  allowedTenants: ["t1"],
  tenantColumn: "tenant",
} as const;

function tenantFilter(alias: string) {
  return eq(column(alias, "tenant"), enumConst("t1"));
}

test("admission issues a credential for a policy-equivalent tenant pushdown", () => {
  const a = scan("A", "a");
  const b = scan("B", "b");
  const reference = project(
    innerJoin(a, b, eq(column("a", "id"), column("b", "id"))),
    [projection("id", column("a", "id"))],
  );
  const candidate = project(
    innerJoin(
      filter(a, tenantFilter("a")),
      filter(b, tenantFilter("b")),
      eq(column("a", "id"), column("b", "id")),
    ),
    [projection("id", column("a", "id"))],
  );

  const result = admitPlan({ reference, candidate, policy: tenantPolicy });
  assert.equal(result.admitted, true, result.reason);
  assert.equal(result.ok, true);
  assert.ok(result.credential);
  assert.equal(result.credential.planDigest, result.planDigest);
  assert.equal(result.credential.policyVersion, "tenant-v1");
  assert.equal(result.certificate?.equivalent, true);
  assert.equal(checkPartitionCertificate(result.certificate!).ok, true);
});

test("a raw candidate that exposes hidden rows is rejected", () => {
  const a = scan("A", "a");
  const b = scan("B", "b");
  const plan = project(
    innerJoin(a, b, eq(column("a", "id"), column("b", "id"))),
    [projection("id", column("a", "id"))],
  );
  const result = admitPlan({ reference: plan, candidate: plan, policy: tenantPolicy });
  assert.equal(result.admitted, false);
  assert.ok(result.counterexample);
  assert.equal(result.credential, undefined);
});

test("same output cardinality with different duplicate multiplicity is rejected", () => {
  const base = scan("T", "t");
  const reference = unionAll(
    base,
    filter(base, isNull(column("t", "id"))),
  );
  const candidate = unionAll(
    base,
    filter(base, eq(column("t", "id"), idConst("x"))),
  );
  const db = { T: [{ id: idValue("x") }, { id: NULL }] };
  const referenceRows = evaluatePlan(reference, db);
  const candidateRows = evaluatePlan(candidate, db);
  assert.equal(referenceRows.length, 3);
  assert.equal(candidateRows.length, 3);
  assert.notDeepEqual(referenceRows, candidateRows);

  const result = admitPlan({ reference, candidate, policy: visible });
  assert.equal(result.admitted, false);
});

test("moving a predicate from ON to WHERE is sound for INNER JOIN", () => {
  const a = scan("A", "a");
  const b = scan("B", "b");
  const predicate = eq(column("b", "tenant"), enumConst("t1"));
  const reference = project(
    innerJoin(a, b, {
      kind: "and",
      args: [eq(column("a", "id"), column("b", "id")), predicate],
    }),
    [projection("id", column("a", "id"))],
  );
  const candidate = project(
    filter(
      innerJoin(a, b, eq(column("a", "id"), column("b", "id"))),
      predicate,
    ),
    [projection("id", column("a", "id"))],
  );
  const result = admitPlan({ reference, candidate, policy: visible });
  assert.equal(result.admitted, true, result.reason);
});

test("moving a right-side predicate from ON to WHERE changes LEFT JOIN", () => {
  const a = scan("A", "a");
  const b = scan("B", "b");
  const reference = project(
    leftJoin(a, b, {
      kind: "and",
      args: [eq(column("a", "id"), column("b", "id")), eq(column("b", "tenant"), enumConst("t1"))],
    }),
    [projection("a", column("a", "id")), projection("b", column("b", "id"))],
  );
  const candidate = project(
    filter(
      leftJoin(a, b, eq(column("a", "id"), column("b", "id"))),
      eq(column("b", "tenant"), enumConst("t1")),
    ),
    [projection("a", column("a", "id")), projection("b", column("b", "id"))],
  );
  const result = admitPlan({ reference, candidate, policy: visible });
  assert.equal(result.admitted, false);
  assert.ok(result.counterexample);
});

test("a hidden right row must leave the LEFT JOIN NULL extension intact", () => {
  const a = scan("A", "a");
  const b = scan("B", "b");
  const reference = project(
    leftJoin(a, b, eq(column("a", "id"), column("b", "id"))),
    [projection("a", column("a", "id")), projection("b", column("b", "id"))],
  );
  const candidate = project(
    innerJoin(
      a,
      b,
      {
        kind: "and",
        args: [
          eq(column("a", "id"), column("b", "id")),
          eq(column("b", "tenant"), enumConst("t1")),
        ],
      },
    ),
    [projection("a", column("a", "id")), projection("b", column("b", "id"))],
  );

  const db = {
    A: [{ id: idValue("x"), tenant: "t1" }],
    B: [{ id: idValue("x"), tenant: "t2" }],
  };
  assert.deepEqual(evaluatePlan(reference, db, tenantPolicy), [[idValue("x"), NULL]]);
  assert.deepEqual(evaluatePlan(candidate, db), []);

  const result = admitPlan({ reference, candidate, policy: tenantPolicy });
  assert.equal(result.admitted, false);
});

test("a rewrite with no actual permission difference is admitted", () => {
  const a = scan("A", "a");
  const b = scan("B", "b");
  const allTenants = {
    version: "all-tenants",
    subject: "alice",
    allowedTenants: ["t1", "t2"],
    tenantColumn: "tenant",
  } as const;
  const visibleTenant = (alias: string) => ({
    kind: "or" as const,
    args: [
      eq(column(alias, "tenant"), enumConst("t1")),
      eq(column(alias, "tenant"), enumConst("t2")),
    ],
  });
  const reference = project(
    leftJoin(a, b, eq(column("a", "id"), column("b", "id"))),
    [projection("a", column("a", "id"))],
  );
  const candidate = project(
    leftJoin(
      filter(a, visibleTenant("a")),
      filter(b, visibleTenant("b")),
      eq(column("a", "id"), column("b", "id")),
    ),
    [projection("a", column("a", "id"))],
  );
  const result = admitPlan({ reference, candidate, policy: allTenants });
  assert.equal(result.admitted, true, result.reason);
});

test("the certificate can be independently checked and tampering is detected", () => {
  const a = scan("A", "a");
  const b = scan("B", "b");
  const plan = project(
    leftJoin(a, b, eq(column("a", "id"), column("b", "id"))),
    [projection("id", column("a", "id"))],
  );
  const result = admitPlan({ reference: plan, candidate: plan, policy: visible });
  assert.equal(result.admitted, true);
  const certificate = result.certificate!;
  const checked = checkPartitionCertificate(certificate, {
    reference: plan,
    candidate: plan,
    policy: visible,
    checkVerdict: true,
  });
  assert.equal(checked.ok, true, checked.errors.join("; "));

  const incomplete = {
    ...certificate,
    classes: certificate.classes.slice(1),
    classCount: certificate.classCount - 1,
  };
  const missingCheck = checkPartitionCertificate(incomplete);
  assert.equal(missingCheck.ok, false);
  assert.ok(missingCheck.missing.length > 0);

  const tampered = {
    ...certificate,
    equivalent: false,
  };
  const tamperedCheck = checkPartitionCertificate(tampered);
  assert.equal(tamperedCheck.ok, false);
});

test("certificate covers empty, duplicate, shared-id and NULL rows", () => {
  const a = scan("A", "a");
  const b = scan("B", "b");
  const ax = filter(a, eq(column("a", "id"), idConst("x")));
  const plan = project(
    leftJoin(ax, b, eq(column("a", "id"), column("b", "id"))),
    [projection("id", column("a", "id")), projection("bid", column("b", "id"))],
  );
  const result = admitPlan({ reference: plan, candidate: plan, policy: visible });
  assert.equal(result.admitted, true);
  const tags = new Set(result.certificate!.classes.flatMap((entry) => entry.tags));
  assert.ok(tags.has("empty:A"));
  assert.ok(tags.has("empty:B"));
  assert.ok(tags.has("duplicate:A") || tags.has("duplicate:B"));
  assert.ok(tags.has("all-null-row:A") || tags.has("all-null-row:B"));
  assert.ok([...tags].some((tag) => tag.startsWith("shared-id:")));
  assert.ok([...tags].some((tag) => tag.startsWith("constant-id:")));
});

test("plan and policy version are bound into the credential", () => {
  const a = scan("A", "a");
  const b = scan("B", "b");
  const plan = project(
    innerJoin(a, b, eq(column("a", "id"), column("b", "id"))),
    [projection("id", column("a", "id"))],
  );
  const first = admitPlan({ reference: plan, candidate: plan, policy: visible });
  const second = admitPlan({
    reference: plan,
    candidate: plan,
    policy: { version: "visible-v2", allowAll: true },
  });
  assert.equal(first.admitted, true);
  assert.equal(second.admitted, true);
  assert.notEqual(first.credential!.token, second.credential!.token);
  assert.equal(first.credential!.planDigest, planDigest(plan));
  assert.notEqual(first.credential!.policyVersion, second.credential!.policyVersion);
});

test("a closed enum schema enumerates every finite value without fresh classes", () => {
  const plan = project(scan("T", "t"), [projection("x", column("t", "x"))]);
  const schema = {
    tables: {
      T: {
        columns: {
          x: { kind: "enum" as const, enumValues: ["a", "b"] },
        },
      },
    },
  };
  const result = admitPlan({
    reference: plan,
    candidate: plan,
    policy: visible,
    schema,
  });
  assert.equal(result.admitted, true);
  const columnDomain = result.certificate!.domain.tables[0].columns[0];
  assert.equal(columnDomain.freshEnumClasses, "none");
  assert.deepEqual(columnDomain.enumValues, ["a", "b"]);
  assert.equal(result.certificate!.classCount, 10);
  assert.equal(
    checkPartitionCertificate(result.certificate!, {
      reference: plan,
      candidate: plan,
      policy: visible,
      schema,
    }).ok,
    true,
  );
});
