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

function releaseRecord(overrides: {
  index?: number;
  keyMaterialId: string;
  certificateId?: string;
  domain: string;
}) {
  return {
    algorithm: "ecdsa-p256-v2",
    packageId: PACKAGE_ID,
    payloadSchema: "schema-release-v2",
    canonicalVersion: "2.0",
    scope: "scope-release",
    coveredRange: releaseRange,
    declaredAttributes: ["release.policy"],
    ...overrides,
  };
}

test("same public key behind two certificates counts as one material: threshold of two fails", () => {
  const records = buildRecords([
    releaseRecord({ index: 0, keyMaterialId: "key-shared", certificateId: "cert-A", domain: "domain-build" }),
    releaseRecord({ index: 1, keyMaterialId: "key-shared", certificateId: "cert-B", domain: "domain-build" }),
  ]);
  const bundle = buildBundle(records);
  const report = createDefaultVerifier().evaluate(bundle);

  const witness = report.witnesses.find((entry) => entry.attribute === "release.policy");
  assert.equal(witness?.satisfied, false);
  assert.match(witness?.reason ?? "", /distinct public-key material/);
  assert.equal(report.verified.length, 2);
  assertWitnessesMatchOracle(bundle, report);
});

test("two distinct materials with separate certificates meet the threshold", () => {
  const records = buildRecords([
    releaseRecord({ index: 0, keyMaterialId: "key-release-1", certificateId: "cert-A", domain: "domain-build" }),
    releaseRecord({ index: 1, keyMaterialId: "key-release-2", certificateId: "cert-B", domain: "domain-build" }),
  ]);
  const bundle = buildBundle(records);
  const report = createDefaultVerifier().evaluate(bundle);

  const witness = report.witnesses.find((entry) => entry.attribute === "release.policy");
  assert.equal(witness?.satisfied, true);
  assert.deepEqual(witness?.recordIndices, [0, 1]);
  assert.equal(witness?.distinctKeyMaterials, 2);
  assertWitnessesMatchOracle(bundle, report);
});

test("management-domain exclusion forbids combining mutually exclusive signers", () => {
  const records = buildRecords([
    // domain-vendor and domain-release are mutually exclusive by policy.
    releaseRecord({ index: 0, keyMaterialId: "key-release-1", domain: "domain-vendor" }),
    releaseRecord({ index: 1, keyMaterialId: "key-release-2", domain: "domain-release" }),
    // A compatible third material makes the threshold reachable without the pair.
    releaseRecord({ index: 2, keyMaterialId: "key-release-3", domain: "domain-build" }),
  ]);
  const bundle = buildBundle(records);
  const report = createDefaultVerifier().evaluate(bundle);

  const witness = report.witnesses.find((entry) => entry.attribute === "release.policy");
  assert.equal(witness?.satisfied, true);
  // Smallest allowed pair avoids the vendor/release exclusion: records 0 and 2.
  assert.deepEqual(witness?.recordIndices, [0, 2]);
  assertWitnessesMatchOracle(bundle, report);
});

test("minimum call count wins over index order, then index vector breaks ties", () => {
  // Three materials; any pair meets the threshold, so the lexicographically
  // smallest two-index vector must be returned rather than a triple.
  const records = buildRecords([
    releaseRecord({ index: 5, keyMaterialId: "key-release-3", domain: "domain-build" }),
    releaseRecord({ index: 2, keyMaterialId: "key-release-1", domain: "domain-build" }),
    releaseRecord({ index: 7, keyMaterialId: "key-release-2", domain: "domain-build" }),
  ]);
  const bundle = buildBundle(records);
  const report = createDefaultVerifier().evaluate(bundle);

  const witness = report.witnesses.find((entry) => entry.attribute === "release.policy");
  assert.deepEqual(witness?.recordIndices, [2, 5]);
  assert.equal(witness?.verificationCalls, 2);
  assertWitnessesMatchOracle(bundle, report);
});
