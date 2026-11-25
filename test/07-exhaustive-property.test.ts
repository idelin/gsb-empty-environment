import { test } from "node:test";
import assert from "node:assert/strict";
import {
  createDefaultVerifier,
  enumerateSubsets,
} from "../src/index.ts";
import {
  buildBundle,
  buildRecords,
  LAYOUT,
  PACKAGE_ID,
} from "../src/fixtures/builder.ts";
import { allSubsets, assertWitnessesMatchOracle, oracleWitness } from "./oracle.ts";
import type { DistributionBundle, RecordInput } from "../src/fixtures/builder.ts";

/** Deterministic PRNG so the property run is reproducible. */
function mulberry32(seed: number): () => number {
  let state = seed;
  return () => {
    state |= 0;
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const releaseRange = { start: LAYOUT.lenRelease.start, end: LAYOUT.release.end };
const domains = ["domain-vendor", "domain-build", "domain-release"];

function randomBundle(seed: number, recordCount: number): DistributionBundle {
  const random = mulberry32(seed);
  const inputs: RecordInput[] = [];
  for (let i = 0; i < recordCount; i += 1) {
    // Reuse a small key-material pool so same-key/different-cert cases appear.
    const keyNumber = 1 + Math.floor(random() * 3);
    inputs.push({
      algorithm: random() < 0.9 ? "ecdsa-p256-v2" : "ed25519-v1",
      keyMaterialId: `key-release-${keyNumber}`,
      certificateId: `cert-${i}`,
      domain: domains[Math.floor(random() * domains.length)],
      packageId: PACKAGE_ID,
      payloadSchema: "schema-release-v2",
      canonicalVersion: "2.0",
      scope: "scope-release",
      coveredRange: { ...releaseRange },
      declaredAttributes: ["release.policy"],
      validSignature: random() >= 0.1,
    });
  }
  return buildBundle(buildRecords(inputs));
}

test("exhaustive small-package enumeration: solver matches the independent oracle across random bundles", () => {
  for (let seed = 1; seed <= 60; seed += 1) {
    const bundle = randomBundle(seed, 1 + (seed % 5));
    const report = createDefaultVerifier().evaluate(bundle);
    assertWitnessesMatchOracle(bundle, report);

    const requirement = bundle.manifest.requirements.find(
      (entry) => entry.attribute === "release.policy",
    )!;
    const expected = oracleWitness(requirement, bundle, report);

    // Independently verify minimality: no strictly smaller subset is feasible.
    const candidates = report.verified.filter((signature) =>
      signature.declaredAttributes.includes("release.policy"),
    );
    for (const subset of allSubsets(candidates)) {
      if (subset.length >= expected.length) continue;
      const materials = new Set(subset.map((signature) => signature.keyMaterialId));
      const present = new Set(subset.map((signature) => signature.domain));
      const excluded = bundle.manifest.exclusionRules.some(
        (rule) => present.has(rule.domainA) && present.has(rule.domainB),
      );
      assert.ok(
        excluded || materials.size < requirement.distinctKeyMaterials,
        `seed ${seed}: a smaller feasible subset exists, solver result is not minimal`,
      );
    }
  }
});

test("enumerateSubsets yields (size, ascending index vector) order", () => {
  const items = [{ id: 0 }, { id: 1 }, { id: 2 }];
  const seen = Array.from(enumerateSubsets(items), (subset) => subset.map((item) => item.id));
  assert.deepEqual(seen, [
    [0],
    [1],
    [2],
    [0, 1],
    [0, 2],
    [1, 2],
    [0, 1, 2],
  ]);
  // Every non-empty subset appears exactly once, matching the brute oracle.
  const expectedVectors = allSubsets(items)
    .filter((subset) => subset.length > 0)
    .map((subset) => subset.map((item) => item.id))
    .sort((a, b) => a.length - b.length || a.join(",").localeCompare(b.join(",")));
  assert.deepEqual(seen, expectedVectors);
});

test("verification call count never exceeds the number of structurally valid records", () => {
  const bundle = randomBundle(999, 6);
  const report = createDefaultVerifier().evaluate(bundle);
  const structuralDefects = report.diagnostics.filter(
    (diagnostic) => diagnostic.code !== "CRYPTO_FAILURE",
  ).length;
  assert.ok(report.totalVerificationCalls <= bundle.records.length);
  assert.equal(
    report.totalVerificationCalls,
    bundle.records.length - structuralDefects,
  );
});
