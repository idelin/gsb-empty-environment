import { test } from "node:test";
import assert from "node:assert/strict";
import {
  ComplianceLedger,
  verifyInclusionProof,
  verifyErasureProof,
  hashLeaf,
  hashData,
  hashEraseMarker,
  type MerkleProof,
} from "../src/index.ts";

const customers: Array<[string, string]> = [
  ["cust-001", JSON.stringify({ name: "Alice Zhang", idCard: "110101199001011234", phone: "13800000001" })],
  ["cust-002", JSON.stringify({ name: "Bob Li", idCard: "310104198805052345", phone: "13800000002" })],
  ["cust-003", JSON.stringify({ name: "Carol Wang", idCard: "440305199212123456", phone: "13800000003" })],
  ["cust-004", JSON.stringify({ name: "Dave Chen", idCard: "330106199507074567", phone: "13800000004" })],
  ["cust-005", JSON.stringify({ name: "Eve Liu", idCard: "510107199811115678", phone: "13800000005" })],
];

function freshLedger(): ComplianceLedger {
  const ledger = new ComplianceLedger();
  for (const [id, data] of customers) ledger.addRecord(id, data);
  return ledger;
}

test("inclusion proofs verify for all records against current root", () => {
  const ledger = freshLedger();
  for (const [id, data] of customers) {
    const proof = ledger.getInclusionProof(id);
    assert.equal(verifyInclusionProof(id, data, proof, ledger.root), true);
  }
});

test("erasure replaces leaf with Erase_Marker and root evolves smoothly", () => {
  const ledger = freshLedger();
  const rootBefore = ledger.root;

  const erasure = ledger.eraseRecord("cust-003", "2026-10-04T08:00:00.000Z");

  assert.notEqual(ledger.root, rootBefore, "root must evolve after erasure");
  assert.equal(ledger.isErased("cust-003"), true);

  const expectedMarker = hashEraseMarker(
    "cust-003",
    erasure.originalLeafHash,
    erasure.deletionSignature,
  );
  assert.equal(ledger.leafHashOf("cust-003"), expectedMarker);
  assert.equal(ledger.leafHashOf("cust-003"), erasure.eraseMarkerHash);

  const lastSnapshot = ledger.history[ledger.history.length - 1];
  assert.equal(lastSnapshot.reason, "erase");
  assert.equal(lastSnapshot.recordId, "cust-003");
  assert.equal(lastSnapshot.root, ledger.root);
});

test("original customer data is physically destroyed after erasure", () => {
  const ledger = freshLedger();
  const plaintext = ledger.readPlaintext("cust-003");
  assert.ok(plaintext, "plaintext exists before erasure");
  assert.ok(plaintext.includes("Carol Wang"));

  ledger.eraseRecord("cust-003");

  assert.equal(ledger.hasPlaintext("cust-003"), false, "plaintext store entry removed");
  assert.equal(ledger.readPlaintext("cust-003"), undefined);
  assert.ok(plaintext.every((byte) => byte === 0), "in-memory bytes zeroed out");
});

test("other customers' proofs remain fully valid against the new root after erasure", () => {
  const ledger = freshLedger();
  ledger.eraseRecord("cust-003");

  const newRoot = ledger.root;
  for (const [id, data] of customers) {
    if (id === "cust-003") continue;
    const proof = ledger.getInclusionProof(id);
    assert.equal(
      verifyInclusionProof(id, data, proof, newRoot),
      true,
      `proof for ${id} must verify against post-erasure root`,
    );
  }
});

test("historical proofs still verify against historical roots (audit trail)", () => {
  const ledger = freshLedger();
  const historicalProofs = new Map<string, { proof: MerkleProof; root: string }>();
  for (const [id] of customers) {
    historicalProofs.set(id, { proof: ledger.getInclusionProof(id), root: ledger.root });
  }
  const preErasureRoot = ledger.root;

  ledger.eraseRecord("cust-003");

  for (const [id, data] of customers) {
    if (id === "cust-003") continue;
    const { proof } = historicalProofs.get(id)!;
    assert.equal(
      verifyInclusionProof(id, data, proof, preErasureRoot),
      true,
      `historical proof for ${id} must verify against pre-erasure root`,
    );
  }
});

test("erasure proof is publicly verifiable: signature + marker + merkle path", () => {
  const ledger = freshLedger();
  const erasure = ledger.eraseRecord("cust-002", "2026-10-04T09:30:00.000Z");

  assert.equal(verifyErasureProof(erasure, ledger.root), true);
  assert.equal(verifyErasureProof(erasure), true, "self-contained proof carries its root");
});

test("tampered erasure proofs are rejected", () => {
  const ledger = freshLedger();
  const erasure = ledger.eraseRecord("cust-002");

  const wrongRecord = { ...erasure, recordId: "cust-999" };
  assert.equal(verifyErasureProof(wrongRecord, ledger.root), false);

  const wrongSignature = { ...erasure, deletionSignature: "00".repeat(64) };
  assert.equal(verifyErasureProof(wrongSignature, ledger.root), false);

  const wrongMarker = { ...erasure, eraseMarkerHash: "ff".repeat(32) };
  assert.equal(verifyErasureProof(wrongMarker, ledger.root), false);

  const wrongRoot = { ...erasure, root: "ab".repeat(32) };
  assert.equal(verifyErasureProof(wrongRoot), false);
});

test("tampered inclusion proofs are rejected", () => {
  const ledger = freshLedger();
  const proof = ledger.getInclusionProof("cust-001");

  assert.equal(verifyInclusionProof("cust-001", "forged data", proof, ledger.root), false);
  assert.equal(
    verifyInclusionProof("cust-002", customers[0][1], proof, ledger.root),
    false,
  );
});

test("double erasure and unknown records are rejected", () => {
  const ledger = freshLedger();
  ledger.eraseRecord("cust-004");
  assert.throws(() => ledger.eraseRecord("cust-004"), /already erased/);
  assert.throws(() => ledger.eraseRecord("cust-999"), /unknown record/);
  assert.throws(() => ledger.getInclusionProof("cust-999"), /unknown record/);
});

test("multiple erasures keep remaining proofs valid", () => {
  const ledger = freshLedger();
  ledger.eraseRecord("cust-001");
  ledger.eraseRecord("cust-005");
  ledger.eraseRecord("cust-002");

  const root = ledger.root;
  for (const id of ["cust-003", "cust-004"]) {
    const data = customers.find(([cid]) => cid === id)![1];
    assert.equal(verifyInclusionProof(id, data, ledger.getInclusionProof(id), root), true);
  }
  for (const id of ["cust-001", "cust-002", "cust-005"]) {
    assert.equal(ledger.isErased(id), true);
    assert.equal(ledger.hasPlaintext(id), false);
  }
});

test("leaf hash commits to record id and data hash", () => {
  const ledger = freshLedger();
  const expected = hashLeaf("cust-001", hashData(Buffer.from(customers[0][1], "utf8")));
  assert.equal(ledger.leafHashOf("cust-001"), expected);
});

test("root history records every append and erase epoch", () => {
  const ledger = freshLedger();
  assert.equal(ledger.history.length, customers.length);
  ledger.eraseRecord("cust-001");
  ledger.eraseRecord("cust-002");
  assert.equal(ledger.history.length, customers.length + 2);
  const roots = ledger.history.map((s) => s.root);
  assert.equal(new Set(roots).size, roots.length, "every epoch has a distinct root");
});
