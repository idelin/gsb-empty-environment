import { test } from "node:test";
import assert from "node:assert/strict";
import { paginate, prepare } from "../src/paginate.ts";
import { buildCertificate, exhaustiveVerify } from "../src/certificate.ts";
import { validateLayout } from "../src/validate.ts";
import { fixtureMetrics, sampleConfig, sampleDocument } from "../src/fixtures.ts";
import { heading, paraWords, testConfig, testMetrics } from "./helpers.ts";
import type { Document } from "../src/model.ts";

test("certificate verifies engine cost exhaustively on the sample document", () => {
  const doc = sampleDocument();
  const cfg = sampleConfig();
  const metrics = fixtureMetrics();
  const r = paginate(doc, metrics, cfg);
  assert.equal(r.feasible, true);
  const cert = buildCertificate(r, doc, metrics, cfg);
  assert.equal(cert.exhaustive.verified, true);
  assert.equal(cert.exhaustive.truncated, false);
  assert.ok(cert.exhaustive.nodesExplored > 0);
  assert.equal(cert.pages.length, r.pages.length);
  assert.ok(cert.pages.every((p) => p.capacity === cfg.pageHeight));
});

test("exhaustive verification rejects an inflated cost claim", () => {
  const doc = sampleDocument();
  const cfg = sampleConfig();
  const metrics = fixtureMetrics();
  const prepared = prepare(doc, metrics, cfg);
  if ("error" in prepared) throw new Error(prepared.error);
  const r = paginate(doc, metrics, cfg);
  const check = exhaustiveVerify(prepared.items, prepared.ctx, r.level, r.cost + 1);
  assert.ok(check.cheaperFound > 0, "a cheaper pagination than the inflated claim exists");
});

test("certificate lists capacity, orphan checks and footnote attribution per page", () => {
  const doc = sampleDocument();
  const cfg = sampleConfig();
  const metrics = fixtureMetrics();
  const r = paginate(doc, metrics, cfg);
  const cert = buildCertificate(r, doc, metrics, cfg);
  const page0 = cert.pages[0];
  assert.ok(page0.checks.some((c) => c.startsWith("capacity:")));
  assert.ok(page0.checks.some((c) => c.startsWith("orphans:")));
  assert.deepEqual(page0.footnoteIds, ["f1"]);
  assert.ok(page0.checks.some((c) => c.includes("f1")));
});

test("validator catches silent violations that lack a degradation record", () => {
  const doc: Document = { nodes: [paraWords("p1", 10), heading("h2")], footnotes: [] };
  const cfg = testConfig({ pageHeight: 25 });
  const r = paginate(doc, testMetrics, cfg);
  assert.ok(r.degradations.length > 0);
  assert.equal(validateLayout(r, doc, testMetrics, cfg).length, 0);
  const stripped = { ...r, degradations: [] };
  const problems = validateLayout(stripped, doc, testMetrics, cfg);
  assert.ok(problems.some((p) => p.includes("silently violates")));
});
