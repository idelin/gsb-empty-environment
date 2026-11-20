import test from "node:test";
import assert from "node:assert/strict";
import {
  NULL,
  column,
  enumConst,
  enumValue,
  evaluatePlan,
  filter,
  idConst,
  idValue,
  eq,
  innerJoin,
  isNull,
  leftJoin,
  nullConstant,
  or,
  project,
  projection,
  scan,
  unionAll,
} from "../src/index.ts";

test("SQL equality is three-valued and filters keep only TRUE", () => {
  const row = scan("T", "t");
  const predicate = or(
    eq(column("t", "x"), nullConstant()),
    eq(column("t", "id"), idConst("keep")),
  );
  const plan = project(filter(row, predicate), [projection("id", column("t", "id"))]);
  const rows = evaluatePlan(plan, {
    T: [
      { id: idValue("keep") },
      { id: idValue("other") },
      { id: NULL },
    ],
  });
  assert.deepEqual(rows, [[idValue("keep")]]);

  const nullEquality = evaluatePlan(
    filter(row, eq(column("t", "x"), nullConstant())),
    { T: [{ x: NULL }] },
  );
  assert.deepEqual(nullEquality, []);

  const notNull = evaluatePlan(
    filter(row, { kind: "not", expr: eq(column("t", "x"), nullConstant()) }),
    { T: [{ x: NULL }] },
  );
  assert.deepEqual(notNull, []);
});

test("IS NULL evaluates to a definite boolean even for an unknown value", () => {
  const plan = filter(scan("T", "t"), isNull(column("t", "x")));
  assert.deepEqual(evaluatePlan(plan, { T: [{ x: NULL }, { x: enumValue("a") }] }), [[NULL]]);
});

test("UNION ALL and projection preserve duplicate multiplicities", () => {
  const row = scan("T", "t");
  const plan = unionAll(
    project(row, [projection("x", column("t", "x"))]),
    project(row, [projection("x", column("t", "x"))]),
  );
  const rows = evaluatePlan(plan, { T: [{ x: enumValue("a") }, { x: enumValue("a") }] });
  assert.deepEqual(rows, [
    [enumValue("a")],
    [enumValue("a")],
    [enumValue("a")],
    [enumValue("a")],
  ]);
});

test("LEFT JOIN emits one all-NULL extension for an empty right relation", () => {
  const plan = project(
    leftJoin(
      scan("A", "a"),
      scan("B", "b"),
      eq(column("a", "id"), column("b", "id")),
    ),
    [projection("a", column("a", "id")), projection("b", column("b", "id"))],
  );
  assert.deepEqual(
    evaluatePlan(plan, { A: [{ id: idValue("x") }], B: [] }),
    [[idValue("x"), NULL]],
  );
});

test("INNER JOIN duplicates matches according to both input multiplicities", () => {
  const plan = project(
    innerJoin(
      scan("A", "a"),
      scan("B", "b"),
      eq(column("a", "id"), column("b", "id")),
    ),
    [projection("a", column("a", "id"))],
  );
  const rows = evaluatePlan(plan, {
    A: [{ id: idValue("x") }, { id: idValue("x") }],
    B: [{ id: idValue("x") }, { id: idValue("x") }],
  });
  assert.equal(rows.length, 4);
});

test("same-spelled equality identifiers have one fixed identity", () => {
  assert.deepEqual(
    evaluatePlan(filter(scan("T", "t"), eq(column("t", "id"), idConst("x"))), {
      T: [{ id: idValue("x") }, { id: idValue("y") }],
    }),
    [[idValue("x")]],
  );
  assert.deepEqual(
    evaluatePlan(
      filter(scan("T", "t"), eq(enumConst("x"), enumConst("x"))),
      { T: [{ id: enumValue("anything") }] },
    ),
    [[enumValue("anything")]],
  );
});
