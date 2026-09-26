import { test } from "node:test";
import assert from "node:assert/strict";
import { paginate } from "../src/paginate.ts";
import { validateLayout } from "../src/validate.ts";
import { paraWords, testConfig, testMetrics, words } from "./helpers.ts";
import type { Document } from "../src/model.ts";

test("footnote reserves space on its reference page", () => {
  const doc: Document = {
    nodes: [paraWords("p1", 40, "f1", 0)],
    footnotes: [{ id: "f1", text: words(10) }],
  };
  const cfg = testConfig({ pageHeight: 45 });
  const r = paginate(doc, testMetrics, cfg);
  assert.equal(r.level, 0);
  const p0 = r.pages[0];
  assert.deepEqual(p0.footnoteIds, ["f1"]);
  assert.equal(p0.footnoteHeight, 12);
  assert.ok(p0.bodyUsed + p0.footnoteHeight <= cfg.pageHeight);
  assert.equal(p0.end - p0.start, 2, "4-line paragraph splits 2/2 because the footnote eats space");
  assert.equal(validateLayout(r, doc, testMetrics, cfg).length, 0);
});

test("taller footnote cascades body lines to later pages", () => {
  const cfg = testConfig({ pageHeight: 60 });
  const shortDoc: Document = {
    nodes: [paraWords("p1", 50, "f1", 0)],
    footnotes: [{ id: "f1", text: words(10) }],
  };
  const tallDoc: Document = {
    nodes: [paraWords("p1", 50, "f1", 0)],
    footnotes: [{ id: "f1", text: words(30) }],
  };
  const short = paginate(shortDoc, testMetrics, cfg);
  const tall = paginate(tallDoc, testMetrics, cfg);
  assert.equal(short.level, 0);
  assert.equal(tall.level, 0);
  assert.equal(short.pages[0].end - short.pages[0].start, 3);
  assert.equal(tall.pages[0].end - tall.pages[0].start, 2);
  assert.ok(short.pages[0].footnoteHeight < tall.pages[0].footnoteHeight);
});

test("footnote is attributed to the page holding its reference line", () => {
  const doc: Document = {
    nodes: [paraWords("p1", 50, "f1", 45)],
    footnotes: [{ id: "f1", text: words(10) }],
  };
  const cfg = testConfig({ pageHeight: 60 });
  const r = paginate(doc, testMetrics, cfg);
  let refPage = -1;
  for (const p of r.pages) {
    for (let k = p.start; k < p.end; k++) {
      const it = r.items[k];
      if (it.type === "line" && it.nodeId === "p1" && it.lineIndex === 4) refPage = p.index;
    }
  }
  assert.ok(refPage >= 0);
  assert.ok(r.pages[refPage].footnoteIds.includes("f1"));
  assert.equal(validateLayout(r, doc, testMetrics, cfg).length, 0);
});

test("unknown footnote reference is an explicit error", () => {
  const doc: Document = { nodes: [paraWords("p1", 10, "nope")], footnotes: [] };
  const r = paginate(doc, testMetrics, testConfig());
  assert.equal(r.feasible, false);
  assert.match(r.error ?? "", /unknown footnote/);
});
