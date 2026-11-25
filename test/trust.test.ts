import test from "node:test";
import assert from "node:assert/strict";
import {
  evaluateTrust,
  maxIndependentBatchWeight,
  verifyDomainCertificate,
  verifyIndependenceCertificate,
} from "../src/index.ts";
import { DAY, T0, at, key, sig, snapshot, uid } from "./helpers.ts";

test("chain language is intersected and the earliest deadline wins", () => {
  const snap = snapshot({
    keys: [key("R"), key("A"), key("T")],
    userIds: [
      uid("Alice <alice@dev.alice.example.com>", "alice@dev.alice.example.com", "T"),
      uid("Alice Ops <alice@ops.alice.example.com>", "alice@ops.alice.example.com", "T"),
    ],
    signatures: [
      sig("e1", "R", "A", {
        depth: 2,
        weight: 8,
        notAfter: at(10 * DAY),
        domains: "^(?:[a-z]+\\.)*example\\.com$",
      }),
      sig("e2", "A", "T", {
        depth: 0,
        weight: 5,
        notAfter: at(7 * DAY),
        domains: "^dev\\.[a-z]+\\.example\\.com$",
      }),
    ],
  });

  const evaluation = evaluateTrust(snap, {
    target: "alice@dev.alice.example.com",
    at: at(DAY),
    threshold: 5,
  });
  assert.equal(evaluation.status, "trusted");
  assert.equal(evaluation.maxWeight, 5);
  assert.equal(evaluation.selected.length, 1);
  const path = evaluation.selected[0];
  assert.equal(path.weight, 5);
  assert.equal(path.expiresAt, at(7 * DAY));
  assert.equal(path.depthRemaining, 0);
  assert.deepEqual(path.acceptedEmails, ["alice@dev.alice.example.com"]);

  const other = evaluateTrust(snap, {
    target: "alice@ops.alice.example.com",
    at: at(DAY),
    threshold: 1,
  });
  assert.equal(other.status, "untrusted");

  for (const certificate of evaluation.certificates) {
    const verification = verifyDomainCertificate(snap, certificate);
    assert.equal(verification.ok, true, verification.errors.join(","));
  }
  assert.ok(evaluation.independenceCertificate);
  assert.equal(
    verifyIndependenceCertificate(snap, evaluation.independenceCertificate!).ok,
    true,
  );
});

test("delegation depth decrements at every hop", () => {
  const make = (depths: number[], nodes: string[]) =>
    snapshot({
      keys: nodes.map((node) => key(node)),
      userIds: [uid("a@example.com", "a@example.com", nodes[nodes.length - 1])],
      signatures: depths.map((depth, index) =>
        sig(`e${index}`, nodes[index], nodes[index + 1], {
          depth,
          weight: 1,
        }),
      ),
    });

  const valid = evaluateTrust(make([2, 1, 0], ["R", "A", "B", "T"]), {
    target: "a@example.com",
    at: at(1),
    threshold: 1,
  });
  assert.equal(valid.status, "trusted");
  assert.equal(valid.selected[0].edgeIds.join(">"), "e0>e1>e2");
  assert.equal(valid.selected[0].depthRemaining, 0);

  const tooDeep = evaluateTrust(make([2, 0, 0], ["R", "A", "B", "T"]), {
    target: "a@example.com",
    at: at(1),
    threshold: 1,
  });
  assert.equal(tooDeep.status, "untrusted");
  assert.equal(
    tooDeep.candidates[0].invalidReasons.some((reason) => reason.startsWith("depth-exhausted")),
    true,
  );
});

test("validity and revocation boundaries are half-open at expiry and inclusive at effect", () => {
  const base = snapshot({
    keys: [key("R"), key("T")],
    userIds: [uid("a@example.com", "a@example.com", "T")],
    signatures: [
      sig("e1", "R", "T", {
        depth: 0,
        weight: 5,
        notAfter: at(100_000),
      }),
    ],
  });
  const revoked = {
    ...base,
    revocations: [
      {
        id: "rv1",
        signatureId: "e1",
        issuedAt: at(20_000),
        effectiveAt: at(30_000),
      },
    ],
  };

  for (const offset of [19_999, 20_000, 29_999]) {
    assert.equal(
      evaluateTrust(revoked, { target: "a@example.com", at: T0 + offset, threshold: 5 }).status,
      "trusted",
      `offset ${offset}`,
    );
  }
  for (const offset of [30_000, 30_001, 99_999]) {
    assert.equal(
      evaluateTrust(revoked, { target: "a@example.com", at: T0 + offset, threshold: 5 }).status,
      "untrusted",
      `offset ${offset}`,
    );
  }
  const beforeEffect = evaluateTrust(revoked, {
    target: "a@example.com",
    at: at(20_000),
    threshold: 5,
  });
  assert.equal(beforeEffect.selected[0].expiresAt, at(30_000));
  assert.equal(
    evaluateTrust(base, { target: "a@example.com", at: T0 + 100_000, threshold: 5 }).status,
    "untrusted",
  );

  const futureCertificate = {
    ...base,
    revocations: [
      {
        id: "rv2",
        signatureId: "e1",
        issuedAt: at(40_000),
        effectiveAt: at(50_000),
      },
    ],
  };
  assert.equal(
    evaluateTrust(futureCertificate, {
      target: "a@example.com",
      at: at(45_000),
      threshold: 5,
    }).status,
    "trusted",
  );
  assert.equal(
    evaluateTrust(futureCertificate, {
      target: "a@example.com",
      at: at(50_000),
      threshold: 5,
    }).status,
    "untrusted",
  );

  const earlyEffect = {
    ...base,
    revocations: [
      {
        id: "rv3",
        signatureId: "e1",
        issuedAt: at(40_000),
        effectiveAt: at(20_000),
      },
    ],
  };
  assert.equal(
    evaluateTrust(earlyEffect, { target: "a@example.com", at: at(30_000), threshold: 5 }).status,
    "trusted",
  );
  assert.equal(
    evaluateTrust(earlyEffect, { target: "a@example.com", at: at(40_000), threshold: 5 }).status,
    "untrusted",
  );
});

test("external evidence is required for unresolved and missing-signature states", () => {
  const base = snapshot({
    keys: [key("R"), key("T")],
    userIds: [uid("a@example.com", "a@example.com", "T")],
    signatures: [sig("e1", "R", "T", { depth: 0, weight: 5 })],
  });

  const unresolved = {
    ...base,
    revocations: [
      {
        id: "rv3",
        signatureId: "e1",
        issuedAt: at(10_000),
        effectiveAt: at(90_000),
        status: "unresolved" as const,
      },
    ],
  };
  assert.equal(
    evaluateTrust(unresolved, { target: "a@example.com", at: at(9_999), threshold: 5 }).status,
    "trusted",
  );
  assert.equal(
    evaluateTrust(unresolved, { target: "a@example.com", at: at(10_000), threshold: 5 }).status,
    "unknown",
  );

  const missing = {
    ...base,
    signatures: [
      sig("e1", "R", "T", { depth: 0, weight: 5, requires: ["binding-signature"] }),
    ],
  };
  assert.equal(
    evaluateTrust(missing, { target: "a@example.com", at: at(1), threshold: 5 }).status,
    "unknown",
  );
  const withEvidence = { ...missing, evidence: ["binding-signature"] };
  assert.equal(
    evaluateTrust(withEvidence, { target: "a@example.com", at: at(1), threshold: 5 }).status,
    "trusted",
  );
});

test("alias fingerprints of one material key do not duplicate votes", () => {
  const snap = snapshot({
    keys: [
      key("R"),
      key("A"),
      key("B"),
      key("MATERIAL", { aliases: ["K1", "K2"] }),
    ],
    userIds: [
      uid("Alice <alice@example.com>", "alice@example.com", "K1"),
      uid("Alice Two <alice2@example.com>", "alice2@example.com", "K2"),
    ],
    signatures: [
      sig("p1a", "R", "A", { depth: 1, weight: 5, adminDomain: "X" }),
      sig("p1b", "A", "K1", { depth: 0, weight: 5 }),
      sig("p2a", "R", "B", { depth: 1, weight: 4, adminDomain: "Y" }),
      sig("p2b", "B", "K2", { depth: 0, weight: 4 }),
    ],
  });

  assert.equal(
    evaluateTrust(snap, { target: "alice@example.com", at: at(1), threshold: 5 }).status,
    "trusted",
  );
  assert.equal(
    evaluateTrust(snap, { target: "alice2@example.com", at: at(1), threshold: 4 }).status,
    "trusted",
  );

  const batch = maxIndependentBatchWeight(snap, [
    { target: "alice@example.com", at: at(1) },
    { target: "alice2@example.com", at: at(1) },
  ]);
  assert.equal(batch.perKey.length, 1);
  assert.equal(batch.perKey[0].materialFingerprint, "MATERIAL");
  assert.equal(batch.totalWeight, 9);
  assert.deepEqual(batch.perKey[0].pathIds, ["p1a>p1b", "p2a>p2b"]);
});

test("threshold comparison is inclusive", () => {
  const snap = snapshot({
    keys: [key("R"), key("T")],
    userIds: [uid("a@example.com", "a@example.com", "T")],
    signatures: [sig("e1", "R", "T", { depth: 0, weight: 5 })],
  });
  assert.equal(
    evaluateTrust(snap, { target: "a@example.com", at: at(1), threshold: 5 }).status,
    "trusted",
  );
  assert.equal(
    evaluateTrust(snap, { target: "a@example.com", at: at(1), threshold: 5.000001 }).status,
    "untrusted",
  );
});
