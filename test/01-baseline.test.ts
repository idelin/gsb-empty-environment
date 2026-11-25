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

function baselineRecords() {
  return buildRecords([
    {
      algorithm: "ed25519-v1",
      keyMaterialId: "key-vendor",
      certificateId: "cert-vendor-a",
      domain: "domain-vendor",
      packageId: PACKAGE_ID,
      payloadSchema: "schema-supplier-v2",
      canonicalVersion: "2.0",
      scope: "scope-supplier",
      coveredRange: { start: LAYOUT.lenSupplier.start, end: LAYOUT.supplier.end },
      declaredAttributes: ["vendor.identity"],
    },
    {
      algorithm: "ed25519-v1",
      keyMaterialId: "key-builder",
      domain: "domain-build",
      packageId: PACKAGE_ID,
      payloadSchema: "schema-build-v2",
      canonicalVersion: "2.0",
      scope: "scope-build",
      coveredRange: { ...LAYOUT.build },
      declaredAttributes: ["build.provenance"],
    },
    {
      algorithm: "ecdsa-p256-v2",
      keyMaterialId: "key-release-1",
      domain: "domain-release",
      packageId: PACKAGE_ID,
      payloadSchema: "schema-release-v2",
      canonicalVersion: "2.0",
      scope: "scope-release",
      coveredRange: { start: LAYOUT.lenRelease.start, end: LAYOUT.release.end },
      declaredAttributes: ["release.policy"],
    },
    {
      algorithm: "ecdsa-p256-v2",
      keyMaterialId: "key-release-2",
      domain: "domain-build",
      packageId: PACKAGE_ID,
      payloadSchema: "schema-release-v2",
      canonicalVersion: "2.0",
      scope: "scope-release",
      coveredRange: { start: LAYOUT.lenRelease.start, end: LAYOUT.release.end },
      declaredAttributes: ["release.policy"],
    },
  ]);
}

test("baseline: minimum global witnesses satisfy all required attributes", () => {
  const bundle = buildBundle(baselineRecords());
  const report = createDefaultVerifier().evaluate(bundle);

  assert.equal(report.diagnostics.length, 0);
  assert.equal(report.verified.length, 4);
  assert.equal(report.totalVerificationCalls, 4);

  const byAttribute = new Map(report.witnesses.map((witness) => [witness.attribute, witness]));
  assert.deepEqual(byAttribute.get("vendor.identity")?.recordIndices, [0]);
  assert.deepEqual(byAttribute.get("build.provenance")?.recordIndices, [1]);
  // Threshold 2 key materials; smallest witness is records 2 and 3.
  assert.deepEqual(byAttribute.get("release.policy")?.recordIndices, [2, 3]);
  assert.equal(byAttribute.get("release.policy")?.distinctKeyMaterials, 2);
  assert.ok(report.witnesses.every((witness) => witness.satisfied));
  assertWitnessesMatchOracle(bundle, report);
});

test("cross-field length coverage: half coverage from two signatures never joins into one consent", () => {
  const records = buildRecords([
    // Covers the supplier encoding but not its governing length field.
    {
      algorithm: "ed25519-v1",
      keyMaterialId: "key-vendor",
      domain: "domain-vendor",
      packageId: PACKAGE_ID,
      payloadSchema: "schema-supplier-v2",
      canonicalVersion: "2.0",
      scope: "scope-supplier",
      coveredRange: { ...LAYOUT.supplier },
      declaredAttributes: ["vendor.identity"],
    },
    // Covers only the length field, under a different signer.
    {
      algorithm: "ed25519-v1",
      keyMaterialId: "key-builder",
      domain: "domain-build",
      packageId: PACKAGE_ID,
      payloadSchema: "schema-supplier-v2",
      canonicalVersion: "2.0",
      scope: "scope-supplier",
      coveredRange: { ...LAYOUT.lenSupplier },
      declaredAttributes: ["vendor.identity"],
    },
  ]);
  const bundle = buildBundle(records);
  const report = createDefaultVerifier().evaluate(bundle);

  const witness = report.witnesses.find((entry) => entry.attribute === "vendor.identity");
  assert.equal(witness?.satisfied, false);
  assert.deepEqual(witness?.recordIndices, []);
  assert.match(witness?.reason ?? "", /no single signature/);
  assertWitnessesMatchOracle(bundle, report);
});

test("cross-field length coverage: a wider signature covering encoding plus length field endorses", () => {
  const records = buildRecords([
    {
      algorithm: "ed25519-v1",
      keyMaterialId: "key-vendor",
      domain: "domain-vendor",
      packageId: PACKAGE_ID,
      payloadSchema: "schema-supplier-v2",
      canonicalVersion: "2.0",
      scope: "scope-supplier",
      coveredRange: { start: LAYOUT.lenSupplier.start, end: LAYOUT.supplier.end },
      declaredAttributes: ["vendor.identity"],
    },
  ]);
  const bundle = buildBundle(records);
  const report = createDefaultVerifier().evaluate(bundle);
  const witness = report.witnesses.find((entry) => entry.attribute === "vendor.identity");
  assert.equal(witness?.satisfied, true);
  assert.deepEqual(witness?.recordIndices, [0]);
  assertWitnessesMatchOracle(bundle, report);
});
