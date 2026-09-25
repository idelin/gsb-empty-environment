import { test } from "node:test";
import assert from "node:assert/strict";
import { executeQuery } from "../src/engine.ts";
import { MASKED, isMasked } from "../src/mask.ts";
import { ANALYST, HR, TABLE, makePolicy } from "./fixture.ts";

const policy = makePolicy();

test("filters invisible rows and applies mask rules", () => {
  const result = executeQuery(policy, ANALYST, TABLE, { sortBy: { field: "id" } });
  assert.deepEqual(
    result.rows.map((r) => r.id),
    [1, 2, 5],
  );
  assert.deepEqual(result.columns, ["id", "name", "region", "salary", "bonus", "note"]);
  for (const row of result.rows) {
    assert.ok(isMasked(row.salary), "salary must be masked for analysts");
  }
  assert.equal(result.rows[0].note, "n1");
  assert.ok(isMasked(result.rows[1].note), "note masked when classification > 1");
  assert.ok(isMasked(result.rows[2].note));
});

test("hr subject sees salaries unmasked", () => {
  const result = executeQuery(policy, HR, TABLE, { sortBy: { field: "id" } });
  assert.deepEqual(
    result.rows.map((r) => r.salary),
    [100, 200, 500],
  );
});

test("null and masked marker stay distinct", () => {
  const result = executeQuery(policy, ANALYST, TABLE, { sortBy: { field: "id" } });
  const bob = result.rows[1];
  assert.strictEqual(bob.bonus, null);
  assert.notStrictEqual(bob.salary, null);
  assert.deepEqual(bob.salary, MASKED);
  const json = JSON.parse(JSON.stringify(result)) as typeof result;
  assert.strictEqual(json.rows[1].bonus, null);
  assert.deepEqual(json.rows[1].salary, { kind: "masked" });
  assert.ok(Object.isFrozen(MASKED));
});

test("aggregates run over visible rows only", () => {
  const result = executeQuery(policy, ANALYST, TABLE, {
    aggregates: [
      { fn: "count", as: "n" },
      { fn: "sum", field: "bonus", as: "totalBonus" },
      { fn: "avg", field: "bonus", as: "avgBonus" },
      { fn: "min", field: "bonus", as: "minBonus" },
      { fn: "max", field: "bonus", as: "maxBonus" },
    ],
  });
  assert.deepEqual(result.aggregates, {
    n: 3,
    totalBonus: 60,
    avgBonus: 30,
    minBonus: 10,
    maxBonus: 50,
  });
});

test("aggregates ignore limit (computed over all visible rows)", () => {
  const result = executeQuery(policy, ANALYST, TABLE, {
    limit: 1,
    aggregates: [{ fn: "count", as: "n" }],
  });
  assert.equal(result.rows.length, 1);
  assert.equal(result.aggregates.n, 3);
});

test("limit applies after visibility filtering", () => {
  const result = executeQuery(policy, ANALYST, TABLE, {
    sortBy: { field: "id" },
    limit: 2,
  });
  assert.deepEqual(
    result.rows.map((r) => r.id),
    [1, 2],
  );
});

test("sorts ascending and descending on public fields", () => {
  const desc = executeQuery(policy, ANALYST, TABLE, { sortBy: { field: "id", direction: "desc" } });
  assert.deepEqual(
    desc.rows.map((r) => r.id),
    [5, 2, 1],
  );
});

test("select projects only the requested public columns", () => {
  const result = executeQuery(policy, ANALYST, TABLE, {
    select: ["id", "salary"],
    sortBy: { field: "id" },
  });
  assert.deepEqual(result.columns, ["id", "salary"]);
  for (const row of result.rows) {
    assert.deepEqual(Object.keys(row).sort(), ["id", "salary"]);
  }
});

test("empty visible set yields empty rows and null aggregates", () => {
  const result = executeQuery(policy, { role: "analyst", clearance: 0 }, TABLE, {
    aggregates: [
      { fn: "count", as: "n" },
      { fn: "sum", field: "bonus", as: "s" },
    ],
  });
  assert.deepEqual(result.rows, []);
  assert.deepEqual(result.aggregates, { n: 0, s: null });
});
