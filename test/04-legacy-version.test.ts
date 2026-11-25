import { test } from "node:test";
import assert from "node:assert/strict";
import { createDefaultVerifier } from "../src/index.ts";
import {
  buildBundle,
  buildRecords,
  LAYOUT,
  PACKAGE_ID,
} from "../src/fixtures/builder.ts";
import { assertWitnessesMatchOracle } from "./oracle.ts";

test("witness valid only under an old canonical version is rejected once the manifest moves on", () => {
  const records = buildRecords([
    {
      algorithm: "ed25519-v1",
      keyMaterialId: "key-vendor",
      domain: "domain-vendor",
      packageId: PACKAGE_ID,
      payloadSchema: "schema-legacy-v1",
      canonicalVersion: "1.0",
      scope: "scope-supplier",
      coveredRange: { start: LAYOUT.lenSupplier.start, end: LAYOUT.supplier.end },
      declaredAttributes: ["vendor.identity"],
    },
  ]);
  // Current manifest canonicalizations are all version 2.0.
  const bundle = buildBundle(records, { currentVersion: "2.0" });
  const report = createDefaultVerifier().evaluate(bundle);

  const diagnostic = report.diagnostics.find((entry) => entry.code === "CANONICAL_VERSION_MISMATCH");
  assert.ok(diagnostic);
  assert.equal(diagnostic?.recordIndex, 0);
  assert.equal(report.verified.length, 0);
  const witness = report.witnesses.find((entry) => entry.attribute === "vendor.identity");
  assert.equal(witness?.satisfied, false);
  assertWitnessesMatchOracle(bundle, report);
});

test("the same record endorses when the package still publishes the legacy version", () => {
  const records = buildRecords([
    {
      algorithm: "ed25519-v1",
      keyMaterialId: "key-vendor",
      domain: "domain-vendor",
      packageId: PACKAGE_ID,
      payloadSchema: "schema-legacy-v1",
      canonicalVersion: "1.0",
      scope: "scope-supplier",
      coveredRange: { start: LAYOUT.lenSupplier.start, end: LAYOUT.supplier.end },
      declaredAttributes: ["vendor.identity"],
    },
  ]);
  const bundle = buildBundle(records, { currentVersion: "1.0" });
  const report = createDefaultVerifier().evaluate(bundle);

  assert.equal(report.diagnostics.length, 0);
  const witness = report.witnesses.find((entry) => entry.attribute === "vendor.identity");
  assert.equal(witness?.satisfied, true);
  assert.deepEqual(witness?.recordIndices, [0]);
  assertWitnessesMatchOracle(bundle, report);
});
