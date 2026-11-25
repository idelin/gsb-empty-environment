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

const releaseRange = { start: LAYOUT.lenRelease.start, end: LAYOUT.release.end };

test("duplicate path alias references the same raw bytes without creating ambiguity", () => {
  const records = buildRecords([
    {
      algorithm: "ecdsa-p256-v2",
      keyMaterialId: "key-release-1",
      domain: "domain-build",
      packageId: PACKAGE_ID,
      payloadSchema: "schema-release-v2",
      canonicalVersion: "2.0",
      scope: "scope-release",
      coveredRange: releaseRange,
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
      coveredRange: releaseRange,
      declaredAttributes: ["release.policy"],
    },
  ]);
  const bundle = buildBundle(records, { includeAlias: true });

  // Two region paths, one shared raw interval: legal aliasing, no diagnostic.
  const releasePaths = bundle.manifest.regions.filter(
    (region) => region.range.start === LAYOUT.release.start,
  );
  assert.equal(releasePaths.length, 2);
  assert.equal(releasePaths[1].aliasOf, "regions/release.bin");
  assert.notEqual(releasePaths[0].scope, releasePaths[1].scope);

  const report = createDefaultVerifier().evaluate(bundle);
  // No ambiguity diagnostic: aliasing is legal. The release-scope records are
  // scope-rejected only because they now also touch the distinct alias scope.
  assert.ok(
    !report.diagnostics.some((entry) => entry.code === "AMBIGUOUS_REVERSE_MAP"),
  );
  assert.ok(
    report.diagnostics.every((entry) => entry.code === "CANONICAL_SCOPE_MISMATCH"),
  );
  const witness = report.witnesses.find((entry) => entry.attribute === "release.policy");
  assert.equal(witness?.satisfied, false);
  assertWitnessesMatchOracle(bundle, report);
});

test("alias-free package: the same release records witness once no foreign scope aliases the bytes", () => {
  const records = buildRecords([
    {
      algorithm: "ecdsa-p256-v2",
      keyMaterialId: "key-release-1",
      domain: "domain-build",
      packageId: PACKAGE_ID,
      payloadSchema: "schema-release-v2",
      canonicalVersion: "2.0",
      scope: "scope-release",
      coveredRange: releaseRange,
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
      coveredRange: releaseRange,
      declaredAttributes: ["release.policy"],
    },
  ]);
  const bundle = buildBundle(records);
  const report = createDefaultVerifier().evaluate(bundle);
  assert.equal(report.diagnostics.length, 0);
  const witness = report.witnesses.find((entry) => entry.attribute === "release.policy");
  assert.deepEqual(witness?.recordIndices, [0, 1]);
  assertWitnessesMatchOracle(bundle, report);
});

test("a commitment under the alias scope cannot be reused for the release-scope requirement", () => {
  const records = buildRecords([
    {
      algorithm: "ecdsa-p256-v2",
      keyMaterialId: "key-release-1",
      domain: "domain-build",
      packageId: PACKAGE_ID,
      payloadSchema: "schema-release-v2",
      canonicalVersion: "2.0",
      // This record only commits under the alias scope, even though the raw
      // bytes are identical to the release region.
      scope: "scope-alias",
      coveredRange: { ...LAYOUT.release },
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
      coveredRange: releaseRange,
      declaredAttributes: ["release.policy"],
    },
  ]);
  const bundle = buildBundle(records, { includeAlias: true });
  const report = createDefaultVerifier().evaluate(bundle);

  // The alias-scope record is rejected; only one usable material remains, so
  // the two-material threshold cannot be met.
  assert.ok(
    report.diagnostics.some((entry) => entry.code === "CANONICAL_SCOPE_MISMATCH"),
  );
  const witness = report.witnesses.find((entry) => entry.attribute === "release.policy");
  assert.equal(witness?.satisfied, false);
  assertWitnessesMatchOracle(bundle, report);
});
