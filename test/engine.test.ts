import test from "node:test";
import assert from "node:assert/strict";
import {
  MASKED,
  executeQuery,
  toPublicJSON,
  type Policy,
  type Row,
} from "../src/engine.ts";
import { PolicyError } from "../src/policy.ts";

const ATTRS = ["dept", "clearance", "active"];

const policy: Policy = {
  fields: ["name", "salary", "note"],
  row: {
    op: "and",
    args: [
      { op: "in", attr: "dept", values: ["eng", "fin"] },
      { op: "in", attr: "active", values: [true] },
    ],
  },
  reveal: {
    name: { op: "const", value: true },
    salary: { op: "int", attr: "clearance", cmp: "gte", value: 2 },
    // note: deny by default
  },
};

const rows: Row[] = [
  { attrs: { dept: "eng", clearance: 3, active: true },  data: { name: "Ada",    salary: 185000, note: null, secret: "HIDDEN-ROW-FIELD" } },
  { attrs: { dept: "eng", clearance: 1, active: true },  data: { name: "Bjarne", salary: 142000, note: "x" } },
  { attrs: { dept: "fin", clearance: 2, active: true },  data: { name: "Cleo",   salary: 160000, note: null } },
  { attrs: { dept: "fin", clearance: 9, active: false }, data: { name: "Dan",    salary: 98000,  note: "GHOST-1" } },
  { attrs: { dept: "ops", clearance: 9, active: true },  data: { name: "Eve",    salary: 121000, note: "GHOST-2" } },
];

test("hidden rows are absent from rows, aggregates, and meta", () => {
  const out = executeQuery(rows, policy, {
    aggregates: [
      { fn: "count", as: "n" },
      { fn: "sum", field: "salary", as: "total" },
    ],
  }, ATTRS);
  assert.equal(out.meta.visibleRows, 3);
  assert.equal(out.rows.length, 3);
  assert.equal(out.aggregates["n"], 3);
  // sum over visible, unmasked salaries only: 185000 + 160000 (Bjarne masked)
  assert.equal(out.aggregates["total"], 345000);
  const names = out.rows.map((r) => r["name"]);
  assert.deepEqual(names.sort(), ["Ada", "Bjarne", "Cleo"]);
});

test("serialized output never contains hidden-row content or counts", () => {
  const json = toPublicJSON(executeQuery(rows, policy, {}, ATTRS));
  assert.ok(!json.includes("Dan") && !json.includes("Eve"));
  assert.ok(!json.includes("GHOST-1") && !json.includes("GHOST-2"));
  assert.ok(!json.includes("98000") && !json.includes("121000"));
  assert.ok(!json.includes("HIDDEN-ROW-FIELD")); // non-schema field dropped
  assert.ok(!json.includes("hiddenRows"));
});

test("output conforms to the fixed public schema", () => {
  const out = executeQuery(rows, policy, {}, ATTRS);
  assert.equal(out.schema, "secure-view/v1");
  assert.deepEqual(out.fields, ["name", "salary", "note"]);
  for (const r of out.rows) assert.deepEqual(Object.keys(r), ["name", "salary", "note"]);
});

test("masked marker is distinct from null", () => {
  const out = executeQuery(rows, policy, {}, ATTRS);
  const ada = out.rows.find((r) => r["name"] === "Ada")!;
  assert.equal(ada["note"], MASKED); // deny-by-default masking
  assert.notEqual(ada["note"], null);
  const bjarne = out.rows.find((r) => r["name"] === "Bjarne")!;
  assert.equal(bjarne["salary"], MASKED); // clearance 1 < 2
  const json = toPublicJSON(out);
  assert.ok(json.includes('"[MASKED]"'));
  assert.ok(json.includes('"note":null') === false); // note is masked everywhere
});

test("missing payload field is a genuine null, not masked", () => {
  const p: Policy = { ...policy, reveal: { ...policy.reveal, note: { op: "const", value: true } } };
  const sparse: Row[] = [
    { attrs: { dept: "eng", clearance: 3, active: true }, data: { name: "Ada", salary: 1 } },
  ];
  const out = executeQuery(sparse, p, {}, ATTRS);
  assert.equal(out.rows[0]!["note"], null);
  assert.notEqual(out.rows[0]!["note"], MASKED);
});

test("sorting cannot leak masked originals", () => {
  // Masked salaries: 142000 (Bjarne). If the sort used originals, Bjarne would
  // sit between Cleo and Ada on desc; instead masked entries always sort last.
  const out = executeQuery(rows, policy, { sort: [{ field: "salary", dir: "desc" }] }, ATTRS);
  const names = out.rows.map((r) => r["name"]);
  assert.deepEqual(names, ["Ada", "Cleo", "Bjarne"]);
  // Ascending: masked still last, position independent of hidden value.
  const asc = executeQuery(rows, policy, { sort: [{ field: "salary", dir: "asc" }] }, ATTRS);
  assert.deepEqual(asc.rows.map((r) => r["name"]), ["Cleo", "Ada", "Bjarne"]);
});

test("aggregates ignore masked and null values", () => {
  const p: Policy = { ...policy, reveal: { ...policy.reveal, note: { op: "const", value: true } } };
  const data: Row[] = [
    { attrs: { dept: "eng", clearance: 3, active: true }, data: { name: "a", salary: 10, note: null } },
    { attrs: { dept: "eng", clearance: 0, active: true }, data: { name: "b", salary: 999, note: null } }, // salary masked
    { attrs: { dept: "eng", clearance: 3, active: true }, data: { name: "c", salary: 20, note: null } },
  ];
  const out = executeQuery(data, p, {
    aggregates: [
      { fn: "sum", field: "salary", as: "s" },
      { fn: "avg", field: "salary", as: "a" },
      { fn: "min", field: "salary", as: "lo" },
      { fn: "max", field: "salary", as: "hi" },
    ],
  }, ATTRS);
  assert.equal(out.aggregates["s"], 30);
  assert.equal(out.aggregates["a"], 15);
  assert.equal(out.aggregates["lo"], 10);
  assert.equal(out.aggregates["hi"], 20);
});

test("query validation errors name fields, never values", () => {
  assert.throws(
    () => executeQuery(rows, policy, { sort: [{ field: "secret" }] }, ATTRS),
    (e: unknown) => {
      assert.ok(e instanceof PolicyError);
      assert.ok(e.message.includes("secret"));
      assert.ok(!e.message.includes("HIDDEN-ROW-FIELD"));
      return true;
    },
  );
  assert.throws(
    () => executeQuery(rows, policy, { aggregates: [{ fn: "sum", field: "secret", as: "x" }] }, ATTRS),
    PolicyError,
  );
});

test("policy validation rejects undeclared attributes and bad reveal targets", () => {
  assert.throws(
    () => executeQuery(rows, { ...policy, row: { op: "in", attr: "ssn", values: ["x"] } }, {}, ATTRS),
    /undeclared attribute: ssn/,
  );
  assert.throws(
    () => executeQuery(rows, { ...policy, reveal: { nope: { op: "const", value: true } } }, {}, ATTRS),
    /non-schema field: nope/,
  );
});

test("conditions read attributes only, never payload data", () => {
  // A condition naming a payload field is rejected at validation time...
  assert.throws(
    () => executeQuery(rows, { ...policy, row: { op: "in", attr: "name", values: ["Ada"] } }, {}, ATTRS),
    PolicyError,
  );
  // ...and the derived allow-list only ever contains attribute names, never
  // payload field names, so the same condition is rejected there too.
  const p: Policy = { fields: ["name"], row: { op: "in", attr: "name", values: ["Ada"] }, reveal: {} };
  assert.throws(() => executeQuery(rows, p, {}), PolicyError);
});

test("empty input yields empty visible view with stable schema", () => {
  const out = executeQuery([], policy, { aggregates: [{ fn: "count", as: "n" }, { fn: "sum", field: "salary", as: "s" }] }, ATTRS);
  assert.equal(out.meta.visibleRows, 0);
  assert.deepEqual(out.rows, []);
  assert.equal(out.aggregates["n"], 0);
  assert.equal(out.aggregates["s"], null);
});
