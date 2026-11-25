import test from "node:test";
import assert from "node:assert/strict";
import {
  deserializeDfa,
  dfaAccepts,
  dfaIsEmpty,
  symbolicQuery,
  verifyDomainCertificate,
  verifyIndependenceCertificate,
} from "../src/index.ts";
import { at, key, sig, snapshot, uid } from "./helpers.ts";

function symbolicSnapshot() {
  return snapshot({
    keys: ["R", "A", "B", "T"].map((item) => key(item)),
    userIds: [
      uid("Alice <alice@example.com>", "alice@example.com", "T"),
      uid("Bob <bob@dev.alice.example.com>", "bob@dev.alice.example.com", "T"),
    ],
    signatures: [
      sig("p1a", "R", "A", {
        depth: 1,
        weight: 4,
        adminDomain: "ONE",
        domains: "^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\\.)*example\\.com$",
      }),
      sig("p1b", "A", "T", {
        depth: 0,
        weight: 4,
        domains: "^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\\.)*example\\.com$",
      }),
      sig("p2a", "R", "B", {
        depth: 1,
        weight: 3,
        adminDomain: "TWO",
        domains: "^dev\\.[a-z]+\\.example\\.com$",
      }),
      sig("p2b", "B", "T", {
        depth: 0,
        weight: 3,
        domains: "^dev\\.[a-z]+\\.example\\.com$",
      }),
    ],
  });
}

test("symbolic query returns exact suffix sublanguages and certificates", () => {
  const snap = symbolicSnapshot();
  const result = symbolicQuery(snap, {
    suffix: "example.com",
    at: at(1),
    threshold: 7,
  });
  assert.equal(result.matches.length, 1);
  const match = result.matches[0];
  assert.equal(match.status, "trusted");
  assert.equal(match.meetsThreshold, true);
  assert.equal(match.maxIndependentWeight, 7);
  assert.equal(match.selectedPathIds.length, 2);
  assert.equal(match.coversSuffix, true);
  assert.equal(match.sublanguages.length, 2);

  const dev = match.sublanguages.find((item) => item.pathId === "p2a>p2b");
  assert.ok(dev);
  assert.equal(dev!.sampleDomains.includes("dev.a.example.com"), true);
  assert.equal(dev!.certificate.acceptance.accepted, true);
  const devLanguage = deserializeDfa(dev!.language);
  assert.equal(dfaAccepts(devLanguage, "dev.alice.example.com"), true);
  assert.equal(dfaAccepts(devLanguage, "ops.alice.example.com"), false);
  assert.equal(dfaIsEmpty(devLanguage), false);

  for (const sublanguage of match.sublanguages) {
    const verification = verifyDomainCertificate(snap, sublanguage.certificate);
    assert.equal(verification.ok, true, verification.errors.join(","));
  }
  assert.ok(match.independenceCertificate);
  const independence = verifyIndependenceCertificate(snap, match.independenceCertificate!);
  assert.equal(independence.ok, true, independence.errors.join(","));
});

test("certificate verification rejects tampering", () => {
  const snap = symbolicSnapshot();
  const result = symbolicQuery(snap, {
    suffix: "example.com",
    at: at(1),
    threshold: 7,
  });
  const certificate = structuredClone(result.matches[0].sublanguages[0].certificate);
  certificate.weight = 99;
  const verification = verifyDomainCertificate(snap, certificate);
  assert.equal(verification.ok, false);
  assert.equal(verification.errors.includes("certificate-digest-mismatch"), true);
  assert.equal(verification.errors.includes("weight-mismatch"), true);

  const independence = structuredClone(result.matches[0].independenceCertificate!);
  independence.certificates[0].weight = 99;
  const independenceVerification = verifyIndependenceCertificate(snap, independence);
  assert.equal(independenceVerification.ok, false);
});

test("qualifiedOnly filters targets below the independent-weight threshold", () => {
  const snap = symbolicSnapshot();
  const result = symbolicQuery(snap, {
    suffix: "example.com",
    at: at(1),
    threshold: 8,
    qualifiedOnly: true,
  });
  assert.equal(result.matches.length, 0);
  assert.equal(result.qualified.length, 0);

  const all = symbolicQuery(snap, {
    suffix: "example.com",
    at: at(1),
    threshold: 8,
  });
  assert.equal(all.matches.length, 1);
  assert.equal(all.matches[0].status, "untrusted");
  assert.equal(all.matches[0].meetsThreshold, false);
  assert.equal(all.matches[0].lowerBoundWeight, 7);
});
