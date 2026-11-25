import assert from "node:assert/strict";
import {
  enumerateCompatibleEndorsementSets,
  evaluateTrust,
  maxIndependentBatchWeight,
  symbolicQuery,
  verifyDomainCertificate,
  verifyIndependenceCertificate,
} from "../src/index.ts";

const T0 = Date.parse("2026-01-01T00:00:00Z");
const at = (days: number) => T0 + days * 86_400_000;

const snapshot = {
  id: "acceptance-small-graph",
  keys: [
    { materialFingerprint: "R" },
    { materialFingerprint: "A" },
    { materialFingerprint: "B" },
    { materialFingerprint: "C" },
    { materialFingerprint: "TARGET", aliases: ["TARGET-ALIAS-1", "TARGET-ALIAS-2"] },
  ],
  userIds: [
    { id: "Alice <alice@example.com>", email: "alice@example.com", key: "TARGET-ALIAS-1" },
    { id: "Alice Two <alice2@example.com>", email: "alice2@example.com", key: "TARGET-ALIAS-2" },
  ],
  signatures: [
    {
      id: "e1",
      from: "R",
      to: "A",
      issuedAt: T0,
      notAfter: at(20),
      depth: 2,
      weight: 8,
      domains: "^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\\.)*example\\.com$",
      adminDomain: "ORG-A",
    },
    {
      id: "e2",
      from: "A",
      to: "TARGET-ALIAS-1",
      issuedAt: T0,
      notAfter: at(12),
      depth: 0,
      weight: 8,
      domains: "^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\\.)*example\\.com$",
      adminDomain: "ORG-A",
    },
    {
      id: "e3",
      from: "R",
      to: "B",
      issuedAt: T0,
      notAfter: at(18),
      depth: 1,
      weight: 7,
      domains: "^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\\.)*example\\.com$",
      adminDomain: "ORG-B",
    },
    {
      id: "e4",
      from: "B",
      to: "TARGET-ALIAS-2",
      issuedAt: T0,
      notAfter: at(15),
      depth: 0,
      weight: 6,
      domains: "^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\\.)*example\\.com$",
    },
    {
      id: "e5",
      from: "R",
      to: "C",
      issuedAt: T0,
      notAfter: at(10),
      depth: 1,
      weight: 4,
      domains: "^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\\.)*example\\.com$",
      adminDomain: "ORG-C",
      revocationPredecessors: ["REVOKER-X"],
    },
    {
      id: "e6",
      from: "C",
      to: "TARGET",
      issuedAt: T0,
      notAfter: at(10),
      depth: 0,
      weight: 4,
      domains: "^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\\.)*example\\.com$",
      revocationPredecessors: ["REVOKER-X"],
    },
  ],
  roots: ["R"],
  revocations: [
    {
      id: "rv-e5",
      signatureId: "e5",
      issuedAt: at(5),
      effectiveAt: at(9),
    },
  ],
};

const evaluation = evaluateTrust(snapshot, {
  target: "alice@example.com",
  at: at(3),
  threshold: 18,
});
assert.equal(evaluation.status, "trusted");
assert.equal(evaluation.maxWeight, 18);
assert.deepEqual(evaluation.selected.map((path) => path.pathId), ["e1>e2", "e3>e4", "e5>e6"]);

const beforeRevocation = evaluateTrust(snapshot, {
  target: "alice@example.com",
  at: at(8),
  threshold: 30,
});
assert.equal(beforeRevocation.maxWeight, 18);
const afterRevocation = evaluateTrust(snapshot, {
  target: "alice@example.com",
  at: at(9),
  threshold: 30,
});
assert.equal(afterRevocation.maxWeight, 14);

const batch = maxIndependentBatchWeight(snapshot, [
  { target: "alice@example.com", at: at(3) },
  { target: "alice2@example.com", at: at(3) },
]);
assert.equal(batch.totalWeight, 18);
assert.equal(batch.perKey.length, 1);

const sets = enumerateCompatibleEndorsementSets(evaluation.candidates.filter((path) => path.valid), {
  maximalOnly: true,
});
assert.equal(sets.sets[0].weight, 18);

const symbolic = symbolicQuery(snapshot, {
  suffix: "example.com",
  at: at(3),
  threshold: 18,
});
assert.equal(symbolic.matches.length, 1);
assert.equal(symbolic.matches[0].status, "trusted");
assert.equal(symbolic.matches[0].sublanguages.length, 3);

for (const match of symbolic.matches) {
  for (const sublanguage of match.sublanguages) {
    const verification = verifyDomainCertificate(snapshot, sublanguage.certificate);
    assert.equal(verification.ok, true, verification.errors.join(","));
  }
  if (match.independenceCertificate) {
    const verification = verifyIndependenceCertificate(snapshot, match.independenceCertificate);
    assert.equal(verification.ok, true, verification.errors.join(","));
  }
}

console.log(
  JSON.stringify(
    {
      evaluation: {
        status: evaluation.status,
        weight: evaluation.maxWeight,
        selected: evaluation.selected.map((path) => path.pathId),
      },
      revocationCutover: {
        before: beforeRevocation.maxWeight,
        atEffectiveTime: afterRevocation.maxWeight,
      },
      batchWeightWithAliasUserIdsCountedOnce: batch.totalWeight,
      symbolic: symbolic.matches.map((match) => ({
        key: match.target.materialFingerprint,
        coversSuffix: match.coversSuffix,
        sublanguages: match.sublanguages.map((item) => ({
          path: item.pathId,
          samples: item.sampleDomains,
          certificate: item.certificate.digest,
        })),
      })),
    },
    null,
    2,
  ),
);
