import { test } from "node:test";
import assert from "node:assert/strict";
import {
  ABSENT,
  applyPatchOps,
  applyRules,
  deepEqual,
  getAt,
} from "../src/index.ts";
import type { Rule } from "../src/index.ts";

const wsRule: Rule = {
  id: "ws",
  kind: "whitespace",
  mode: "collapse",
  targets: [["user", "name"], ["user", "tags", "*"], ["user", "bio"]],
};

const dateRule: Rule = {
  id: "date",
  kind: "date",
  formats: ["YYYY-MM-DD", "MM/DD/YYYY"],
  targets: [["events", "*", "when"]],
};

const enumRule: Rule = {
  id: "enum",
  kind: "enum-alias",
  aliases: { na: "N/A", "n/a": "N/A" },
  targets: [["status"]],
};

const rules: Rule[] = [wsRule, dateRule, enumRule];

function sampleDoc() {
  return {
    user: {
      name: "  Ada   Lovelace ",
      bio: null,
      tags: ["  math ", "poetry  "],
    },
    events: [
      { when: "03/04/2025", kind: "talk" },
      { when: "2025-13-01", kind: "deadline" },
      { when: "not a date", kind: "note" },
    ],
    status: "na",
  };
}

test("whitespace collapse trims and collapses strings only", () => {
  const r = applyRules(sampleDoc(), rules);
  assert.equal(getAt(r.output, ["user", "name"]).value, "Ada Lovelace");
  assert.equal(getAt(r.output, ["user", "tags", 0]).value, "math");
  assert.equal(getAt(r.output, ["user", "tags", 1]).value, "poetry");
});

test("null is never equated with whitespace or empty string", () => {
  const r = applyRules(sampleDoc(), rules);
  assert.equal(getAt(r.output, ["user", "bio"] as const).found, true);
  assert.equal(getAt(r.output, ["user", "bio"]).value, null);
  assert.ok(!r.changes.some((c) => c.field === "/user/bio"));

  const empty = applyRules({ a: "", b: "   ", c: null }, [
    { id: "w", kind: "whitespace", mode: "trim", targets: [["a"], ["b"], ["c"]] },
  ]);
  assert.equal(getAt(empty.output, ["a"]).value, "");
  assert.equal(getAt(empty.output, ["b"]).value, "");
  assert.equal(getAt(empty.output, ["c"]).value, null);
  assert.ok(!empty.changes.some((c) => c.field === "/a"));
  assert.ok(!empty.changes.some((c) => c.field === "/c"));
});

test("dates parse only via declared formats and render as ISO", () => {
  const r = applyRules(sampleDoc(), rules);
  assert.equal(getAt(r.output, ["events", 0, "when"]).value, "2025-03-04");
});

test("unparseable or invalid dates keep the original and report path-located errors", () => {
  const r = applyRules(sampleDoc(), rules);
  assert.equal(getAt(r.output, ["events", 1, "when"]).value, "2025-13-01");
  assert.equal(getAt(r.output, ["events", 2, "when"]).value, "not a date");
  assert.equal(r.errors.length, 2);
  assert.equal(r.errors[0].field, "/events/1/when");
  assert.equal(r.errors[0].ruleId, "date");
  assert.match(r.errors[0].message, /invalid month/);
  assert.equal(r.errors[1].field, "/events/2/when");
  assert.match(r.errors[1].message, /no declared format matches/);
});

test("no guessing of year/month/day order for undeclared formats", () => {
  const onlyIso: Rule[] = [
    { id: "d", kind: "date", formats: ["YYYY-MM-DD"], targets: [["when"]] },
  ];
  const r = applyRules({ when: "04/03/2025" }, onlyIso);
  assert.equal(getAt(r.output, ["when"]).value, "04/03/2025");
  assert.equal(r.errors.length, 1);

  const usStyle: Rule[] = [
    { id: "d", kind: "date", formats: ["MM/DD/YYYY"], targets: [["when"]] },
  ];
  const r2 = applyRules({ when: "04/03/2025" }, usStyle);
  assert.equal(getAt(r2.output, ["when"]).value, "2025-04-03");
});

test("leap years are validated through the calendar", () => {
  const rule: Rule[] = [
    { id: "d", kind: "date", formats: ["YYYY-MM-DD"], targets: [["when"]] },
  ];
  assert.equal(
    getAt(applyRules({ when: "2024-02-29" }, rule).output, ["when"]).value,
    "2024-02-29",
  );
  const bad = applyRules({ when: "2023-02-29" }, rule);
  assert.equal(getAt(bad.output, ["when"]).value, "2023-02-29");
  assert.equal(bad.errors.length, 1);
});

test("enum aliases map declared values and leave everything else", () => {
  const r = applyRules(sampleDoc(), rules);
  assert.equal(getAt(r.output, ["status"]).value, "N/A");
  const untouched = applyRules({ status: "unknown-value" }, [enumRule]);
  assert.equal(getAt(untouched.output, ["status"]).value, "unknown-value");
  assert.equal(untouched.changes.length, 0);
});

test("change records carry field identity, before/after and rule id", () => {
  const r = applyRules(sampleDoc(), rules);
  const nameChange = r.changes.find((c) => c.field === "/user/name");
  assert.ok(nameChange);
  assert.equal(nameChange.ruleId, "ws");
  assert.equal(nameChange.before, "  Ada   Lovelace ");
  assert.equal(nameChange.after, "Ada Lovelace");
  const seqs = r.changes.map((c) => c.seq);
  assert.deepEqual(seqs, [...seqs].sort((a, b) => a - b));
});

test("inverse patch restores the exact input", () => {
  const doc = sampleDoc();
  const r = applyRules(doc, rules);
  const restored = applyPatchOps(r.output, r.inverse);
  assert.ok(deepEqual(restored, doc));
});

test("normalizing twice changes nothing further", () => {
  const once = applyRules(sampleDoc(), rules);
  const twice = applyRules(once.output, rules);
  assert.ok(deepEqual(once.output, twice.output));
  assert.equal(twice.changes.length, 0);
  assert.equal(twice.errors.length, once.errors.length);
});

test("object key order is irrelevant, array order is preserved", () => {
  const a = applyRules({ x: " a ", y: { p: " b ", q: " c " } }, [
    { id: "w", kind: "whitespace", mode: "trim", targets: [["x"], ["y", "p"], ["y", "q"]] },
  ]);
  const b = applyRules({ y: { q: " c ", p: " b " }, x: " a " }, [
    { id: "w", kind: "whitespace", mode: "trim", targets: [["x"], ["y", "p"], ["y", "q"]] },
  ]);
  assert.ok(deepEqual(a.output, b.output));
  assert.deepEqual(
    a.changes.map((c) => c.field),
    b.changes.map((c) => c.field),
  );
});

test("copy rule records ABSENT before-value and inverse removes the field", () => {
  const copy: Rule[] = [
    { id: "c", kind: "copy", from: ["a"], to: ["b"], targets: [] },
  ];
  const r = applyRules({ a: "x" }, copy);
  assert.equal(getAt(r.output, ["b"]).value, "x");
  assert.equal(r.changes[0].before, ABSENT);
  const restored = applyPatchOps(r.output, r.inverse);
  assert.ok(deepEqual(restored, { a: "x" }));
});
