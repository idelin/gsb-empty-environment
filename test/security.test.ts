import { test } from "node:test";
import assert from "node:assert/strict";
import { executeQuery } from "../src/engine.ts";
import type { Row } from "../src/engine.ts";
import { PolicyError } from "../src/errors.ts";
import { ANALYST, TABLE, makePolicy } from "./fixture.ts";

const policy = makePolicy();

test("output has a fixed public schema with no hidden-row metadata", () => {
  const result = executeQuery(policy, ANALYST, TABLE);
  assert.deepEqual(Object.keys(result), ["columns", "rows", "aggregates"]);
  assert.doesNotMatch(JSON.stringify(Object.keys(result)), /hidden|total|filtered/i);
});

test("hidden rows leave no trace: interleaving invisible rows changes nothing", () => {
  const query = { sortBy: { field: "id" }, aggregates: [{ fn: "count", as: "n" }] as const };
  const base = executeQuery(policy, ANALYST, TABLE, query);
  const extraHidden: readonly Row[] = [
    { id: 30, name: "ghost-a", region: "us", classification: 1, salary: 1, bonus: 1, note: "x" },
    { id: 40, name: "ghost-b", region: "cn", classification: 9, salary: 2, bonus: 2, note: "y" },
  ];
  const interleaved: Row[] = [extraHidden[0], ...TABLE.slice(0, 3), extraHidden[1], ...TABLE.slice(3)];
  const withHidden = executeQuery(policy, ANALYST, interleaved, query);
  assert.deepEqual(withHidden, base);
});

test("hidden row content never appears in the serialized output", () => {
  const result = executeQuery(policy, ANALYST, TABLE);
  const json = JSON.stringify(result);
  for (const secret of ["carol", "dave", "secret-us", "secret-high", "ghost"]) {
    assert.ok(!json.includes(secret), `output must not contain "${secret}"`);
  }
});

test("input row order does not affect the visible set", () => {
  const query = { sortBy: { field: "id" } };
  const forward = executeQuery(policy, ANALYST, TABLE, query);
  const reversed = executeQuery(policy, ANALYST, [...TABLE].reverse(), query);
  assert.deepEqual(reversed, forward);
});

test("sorting by a maskable field is rejected before any data is read", () => {
  assert.throws(
    () => executeQuery(policy, ANALYST, TABLE, { sortBy: { field: "salary" } }),
    (err: unknown) => {
      assert.ok(err instanceof PolicyError);
      assert.match(err.message, /cannot sort by field "salary"/);
      assert.doesNotMatch(err.message, /100|200|300|400|500/);
      return true;
    },
  );
});

test("masked values cannot influence row ordering", () => {
  const query = { sortBy: { field: "id" } };
  const a = executeQuery(policy, ANALYST, TABLE, query);
  const altered: Row[] = TABLE.map((row) => ({ ...row, salary: 999999 }));
  const b = executeQuery(policy, ANALYST, altered, query);
  assert.deepEqual(b, a);
});

test("aggregating a maskable field is rejected", () => {
  assert.throws(
    () =>
      executeQuery(policy, ANALYST, TABLE, {
        aggregates: [{ fn: "sum", field: "salary", as: "s" }],
      }),
    /cannot aggregate field "salary"/,
  );
});

test("error messages never echo subject or row values", () => {
  const secretSubject = { role: "analyst", clearance: "9f9f-SECRET-clearance" };
  assert.throws(
    () => executeQuery(policy, secretSubject, TABLE),
    (err: unknown) => {
      assert.ok(err instanceof PolicyError);
      assert.match(err.message, /subject\.clearance/);
      assert.doesNotMatch(err.message, /9f9f-SECRET-clearance/);
      return true;
    },
  );
});

test("malformed row attributes fail closed without leaking", () => {
  const corrupt: Row = {
    id: 9,
    name: "mallory",
    region: "cn",
    classification: "CORRUPT-SECRET",
    salary: 12345,
    bonus: 1,
    note: "corrupt-note",
  };
  const result = executeQuery(policy, ANALYST, [...TABLE, corrupt], { sortBy: { field: "id" } });
  const json = JSON.stringify(result);
  assert.ok(!json.includes("mallory"));
  assert.ok(!json.includes("CORRUPT-SECRET"));
  assert.ok(!json.includes("corrupt-note"));
  assert.deepEqual(
    result.rows.map((r) => r.id),
    [1, 2, 5],
  );
});

test("masked marker is distinct from null in structure and JSON", () => {
  const result = executeQuery(policy, ANALYST, TABLE, { sortBy: { field: "id" } });
  const bob = result.rows[1];
  assert.strictEqual(bob.bonus, null);
  assert.notDeepEqual(bob.salary, null);
  const cells = JSON.stringify(result.rows[1]);
  assert.ok(cells.includes('"bonus":null'));
  assert.ok(cells.includes('"salary":{"kind":"masked"}'));
});

test("queries cannot select fields outside the public schema", () => {
  assert.throws(
    () => executeQuery(policy, ANALYST, TABLE, { select: ["id", "classification"] }),
    /not in the public schema/,
  );
});
