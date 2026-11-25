import test from "node:test";
import assert from "node:assert/strict";
import { evaluateTrust } from "../src/index.ts";
import { at, key, sig, snapshot, uid } from "./helpers.ts";

test("missing globally required offline evidence reports unknown", () => {
  const snap = snapshot({
    keys: [key("R"), key("T")],
    userIds: [uid("a@example.com", "a@example.com", "T")],
    signatures: [sig("e1", "R", "T", { depth: 0, weight: 5 })],
    requiredEvidence: ["root-self-signature"],
  });
  const evaluation = evaluateTrust(snap, {
    target: "a@example.com",
    at: at(1),
    threshold: 1,
  });
  assert.equal(evaluation.status, "unknown");
  assert.equal(evaluation.maxWeight, null);
  assert.equal(
    evaluation.unknownReasons.includes("missing-required-evidence:root-self-signature"),
    true,
  );
});

test("an unresolved revocation on any relevant path makes the optimum unknown", () => {
  const snap = snapshot({
    keys: ["R", "A", "B", "T"].map((item) => key(item)),
    userIds: [uid("a@example.com", "a@example.com", "T")],
    signatures: [
      sig("ok1", "R", "A", { depth: 1, weight: 9, adminDomain: "X" }),
      sig("ok2", "A", "T", { depth: 0, weight: 9 }),
      sig("maybe1", "R", "B", { depth: 1, weight: 8, adminDomain: "Y" }),
      sig("maybe2", "B", "T", { depth: 0, weight: 8 }),
    ],
    revocations: [
      {
        id: "rv",
        signatureId: "maybe2",
        issuedAt: at(1),
        effectiveAt: at(1000),
        status: "conflict",
      },
    ],
  });
  const evaluation = evaluateTrust(snap, {
    target: "a@example.com",
    at: at(2),
    threshold: 9,
  });
  assert.equal(evaluation.status, "unknown");
  assert.equal(evaluation.lowerBoundWeight, 9);
  assert.equal(evaluation.unknownReasons.some((reason) => reason.includes("unresolved-revocation")), true);
});

test("explicit revocation-state conflicts and alias conflicts are unknown", () => {
  const base = snapshot({
    keys: [key("R"), key("T")],
    userIds: [uid("a@example.com", "a@example.com", "T")],
    signatures: [sig("e1", "R", "T", { depth: 0, weight: 5 })],
  });
  const conflict = { ...base, revocationConflicts: ["e1"] };
  assert.equal(
    evaluateTrust(conflict, { target: "a@example.com", at: at(1), threshold: 1 }).status,
    "unknown",
  );

  const aliasConflict = snapshot({
    keys: [
      key("R"),
      key("T"),
      key("M1", { aliases: ["SHARED"] }),
      key("M2", { aliases: ["SHARED"] }),
    ],
    userIds: [uid("a@example.com", "a@example.com", "T")],
    signatures: [sig("e1", "R", "T", { depth: 0, weight: 5 })],
  });
  const aliasEvaluation = evaluateTrust(aliasConflict, {
    target: "a@example.com",
    at: at(1),
    threshold: 1,
  });
  assert.equal(aliasEvaluation.status, "unknown");
  assert.equal(aliasEvaluation.unknownReasons.some((reason) => reason.startsWith("alias-conflict")), true);
});

test("target ambiguity across separate material keys is unknown", () => {
  const snap = snapshot({
    keys: [key("R"), key("T1"), key("T2")],
    userIds: [
      uid("One <a@example.com>", "a@example.com", "T1"),
      uid("Two <a@example.com>", "a@example.com", "T2"),
    ],
    signatures: [
      sig("e1", "R", "T1", { depth: 0, weight: 5 }),
      sig("e2", "R", "T2", { depth: 0, weight: 4 }),
    ],
  });
  const evaluation = evaluateTrust(snap, {
    target: "a@example.com",
    at: at(1),
    threshold: 1,
  });
  assert.equal(evaluation.status, "unknown");
  assert.equal(evaluation.target.ambiguous, true);
});
