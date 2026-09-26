import { test } from "node:test";
import assert from "node:assert/strict";
import { paginate } from "../src/paginate.ts";
import { validateLayout } from "../src/validate.ts";
import { heading, paraWords, testConfig, testMetrics } from "./helpers.ts";
import type { Document } from "../src/model.ts";

test("two-line paragraph is never split 1/1", () => {
  const doc: Document = {
    nodes: [paraWords("p1", 30), paraWords("p2", 20)],
    footnotes: [],
  };
  const cfg = testConfig({ pageHeight: 45 });
  const r = paginate(doc, testMetrics, cfg);
  assert.equal(r.feasible, true);
  assert.equal(r.level, 0);
  const pagesWithP2 = r.pages.filter((p) => {
    for (let k = p.start; k < p.end; k++) {
      const it = r.items[k];
      if (it.type === "line" && it.nodeId === "p2") return true;
    }
    return false;
  });
  assert.equal(pagesWithP2.length, 1, "both lines of p2 stay on one page");
  assert.equal(validateLayout(r, doc, testMetrics, cfg).length, 0);
});

test("paragraph split respects orphans and widows minimums", () => {
  const doc: Document = { nodes: [paraWords("p1", 50)], footnotes: [] };
  const cfg = testConfig({ pageHeight: 45 });
  const r = paginate(doc, testMetrics, cfg);
  assert.equal(r.level, 0);
  const counts = r.pages.map((p) => p.end - p.start);
  assert.deepEqual([...counts].sort(), [2, 3]);
  for (const c of counts) assert.ok(c >= 2, "no fragment smaller than 2 lines");
});

test("heading keeps with the following paragraph", () => {
  const doc: Document = { nodes: [heading("h1"), paraWords("p1", 50)], footnotes: [] };
  const cfg = testConfig({ pageHeight: 45 });
  const r = paginate(doc, testMetrics, cfg);
  assert.equal(r.level, 0);
  for (const p of r.pages) {
    assert.notEqual(r.items[p.end - 1].type, "heading", "no page ends with a heading");
  }
});

test("unsatisfiable keep-with-next degrades explicitly, not silently", () => {
  const doc: Document = { nodes: [paraWords("p1", 10), heading("h2")], footnotes: [] };
  const cfg = testConfig({ pageHeight: 25 });
  const r = paginate(doc, testMetrics, cfg);
  assert.equal(r.feasible, true);
  assert.equal(r.level, 1);
  assert.ok(
    r.degradations.some((d) => d.constraint === "keep-with-next" && d.nodeId === "h2"),
    "degradation is reported",
  );
  assert.equal(validateLayout(r, doc, testMetrics, cfg).length, 0);
});

test("oversized unbreakable block is flagged as overflow without blank pages", () => {
  const doc: Document = {
    nodes: [paraWords("p1", 10), { kind: "block", id: "b1", height: 100 }, paraWords("p2", 10)],
    footnotes: [],
  };
  const cfg = testConfig({ pageHeight: 45 });
  const r = paginate(doc, testMetrics, cfg);
  assert.equal(r.feasible, true);
  const blockPage = r.pages.find((p) => r.items[p.start].type === "block");
  assert.ok(blockPage?.overflow, "block page is marked overflow");
  assert.equal(blockPage?.end - blockPage?.start, 1, "oversized block sits alone");
  assert.ok(r.degradations.some((d) => d.constraint === "block-overflow" && d.nodeId === "b1"));
  for (const p of r.pages) assert.ok(p.end > p.start, "no blank pages from backtracking");
  assert.equal(validateLayout(r, doc, testMetrics, cfg).length, 0);
});

test("line taller than a page is an explicit no-solution", () => {
  const doc: Document = { nodes: [paraWords("p1", 10)], footnotes: [] };
  const r = paginate(doc, testMetrics, testConfig({ pageHeight: 5 }));
  assert.equal(r.feasible, false);
  assert.match(r.error ?? "", /exceeds page capacity/);
});
