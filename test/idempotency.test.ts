import { test } from "node:test";
import assert from "node:assert/strict";
import { deriveDomains, searchIdempotency } from "../src/index.ts";
import type { Rule } from "../src/index.ts";

const shape = { status: "", when: "", note: "" };

const goodRules: Rule[] = [
  { id: "w", kind: "whitespace", mode: "collapse", targets: [["note"]] },
  { id: "d", kind: "date", formats: ["YYYY-MM-DD", "MM/DD/YYYY"], targets: [["when"]] },
  { id: "e", kind: "enum-alias", aliases: { na: "N/A", "n/a": "N/A" }, targets: [["status"]] },
];

test("proves idempotency when the whole finite domain is checked", () => {
  const report = searchIdempotency(goodRules, shape, 1_000_000);
  assert.equal(report.status, "proven");
  assert.equal(report.checked, report.total);
  assert.ok(report.total > 100, `domain should be non-trivial, got ${report.total}`);
});

test("refutes non-idempotent alias chains with a concrete witness", () => {
  const chained: Rule[] = [
    { id: "e", kind: "enum-alias", aliases: { a: "b", b: "c" }, targets: [["status"]] },
  ];
  const report = searchIdempotency(chained, { status: "" }, 10_000);
  assert.equal(report.status, "refuted");
  const cx = report.counterexample!;
  assert.equal((cx.input as { status: string }).status, "a");
  assert.equal((cx.once as { status: string }).status, "b");
  assert.equal((cx.twice as { status: string }).status, "c");
  assert.equal(cx.path, "/status");
});

test("budget exhaustion is reported as unknown, never as proven", () => {
  const domains = deriveDomains(goodRules, shape);
  const total = [...domains.values()].reduce((acc, list) => acc * list.length, 1);
  const report = searchIdempotency(goodRules, shape, 5);
  assert.equal(report.status, "unknown");
  assert.equal(report.checked, 5);
  assert.ok(report.total > 5);
  assert.ok(total > 5);
  assert.notEqual(report.status, "proven");
});

test("budget exactly covering the domain still proves", () => {
  const rules: Rule[] = [
    { id: "e", kind: "enum-alias", aliases: { x: "X" }, targets: [["status"]] },
  ];
  const domains = deriveDomains(rules, { status: "" });
  const total = [...domains.values()].reduce((acc, list) => acc * list.length, 1);
  const report = searchIdempotency(rules, { status: "" }, total, domains);
  assert.equal(report.status, "proven");
  assert.equal(report.checked, total);
});

test("domain includes null and whitespace strings as distinct values", () => {
  const rules: Rule[] = [
    { id: "w", kind: "whitespace", mode: "trim", targets: [["note"]] },
  ];
  const domains = deriveDomains(rules, { note: "" });
  const domain = domains.get("/note")!;
  assert.ok(domain.some((v) => v === null));
  assert.ok(domain.some((v) => typeof v === "string" && v.trim() === "" && v !== ""));
});
