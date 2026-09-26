import { test } from "node:test";
import assert from "node:assert/strict";
import { paginate } from "../src/paginate.ts";
import { validateLayout } from "../src/validate.ts";
import { resolveFixpoint } from "../src/fixpoint.ts";
import { applyEdit, applyFootnoteEdit, repaginateLocal } from "../src/update.ts";
import { fixtureMetrics, para, sampleConfig, sampleDocument } from "../src/fixtures.ts";
import { paraWords, testConfig, testMetrics } from "./helpers.ts";
import type { Document } from "../src/model.ts";

test("local edit reuses unaffected prefix pages and matches full re-pagination", () => {
  const doc = sampleDocument();
  const metrics = fixtureMetrics();
  const cfg = sampleConfig();
  const prev = paginate(doc, metrics, cfg);
  const edited = applyEdit(doc, {
    nodeId: "p3",
    replace: para("p3", 90, { seed: 7, footnoteRef: "f2", refAtWord: 20 }),
  });
  const outcome = repaginateLocal(doc, prev, edited, metrics, cfg);
  assert.equal(outcome.ok, true);
  assert.ok(outcome.reusedPages >= 1, "prefix pages are reused");
  assert.equal(outcome.fellBackToFull, false);
  const full = paginate(edited, metrics, cfg);
  assert.ok(outcome.result);
  assert.deepEqual(
    outcome.result.pages.map((p) => [p.start, p.end]),
    full.pages.map((p) => [p.start, p.end]),
  );
  assert.equal(validateLayout(outcome.result, edited, metrics, cfg).length, 0);
});

test("footnote growth invalidates reuse and still converges correctly", () => {
  const doc = sampleDocument();
  const metrics = fixtureMetrics();
  const cfg = sampleConfig();
  const prev = paginate(doc, metrics, cfg);
  const edited = applyFootnoteEdit(
    doc,
    "f1",
    Array.from({ length: 60 }, (_, i) => `w${i}`).join(" "),
  );
  const outcome = repaginateLocal(doc, prev, edited, metrics, cfg);
  assert.equal(outcome.ok, true);
  assert.equal(outcome.reusedPages, 0, "footnote height change forbids prefix reuse");
  const full = paginate(edited, metrics, cfg);
  assert.ok(outcome.result);
  assert.deepEqual(
    outcome.result.pages.map((p) => [p.start, p.end]),
    full.pages.map((p) => [p.start, p.end]),
  );
});

test("fixpoint resolves for the sample document", () => {
  const out = resolveFixpoint(sampleDocument(), fixtureMetrics(), sampleConfig());
  assert.equal(out.ok, true);
  if (out.ok) assert.ok(out.iterations >= 1);
});

test("fixpoint reports explicit no-solution for infeasible input", () => {
  const doc: Document = { nodes: [paraWords("p1", 10)], footnotes: [] };
  const out = resolveFixpoint(doc, testMetrics, testConfig({ pageHeight: 5 }));
  assert.equal(out.ok, false);
  if (!out.ok) assert.match(out.reason, /infeasible/);
});
