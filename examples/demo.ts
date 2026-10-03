import {
  ComplianceLedger,
  verifyInclusionProof,
  verifyErasureProof,
} from "../src/index.ts";

function show(label: string, value: unknown): void {
  console.log(`${label}:`, value);
}

const ledger = new ComplianceLedger();

console.log("=== 1. Onboard customers (append records) ===");
const records: Array<[string, string]> = [
  ["cust-001", JSON.stringify({ name: "Alice Zhang", idCard: "110101199001011234" })],
  ["cust-002", JSON.stringify({ name: "Bob Li", idCard: "310104198805052345" })],
  ["cust-003", JSON.stringify({ name: "Carol Wang", idCard: "440305199212123456" })],
  ["cust-004", JSON.stringify({ name: "Dave Chen", idCard: "330106199507074567" })],
];
for (const [id, data] of records) ledger.addRecord(id, data);
show("ledger root", ledger.root);

console.log("\n=== 2. Prove inclusion for cust-002 ===");
const proofBob = ledger.getInclusionProof("cust-002");
show("proof path length", proofBob.path.length);
show("verifies against root", verifyInclusionProof("cust-002", records[1][1], proofBob, ledger.root));

console.log("\n=== 3. cust-003 exercises the right to be forgotten ===");
const rootBefore = ledger.root;
const erasure = ledger.eraseRecord("cust-003", "2026-10-04T08:00:00.000Z");
show("root before erasure", rootBefore);
show("root after erasure ", ledger.root);
show("erase marker hash  ", erasure.eraseMarkerHash);
show("deletion signature ", `${erasure.deletionSignature.slice(0, 32)}...`);
show("plaintext retained?", ledger.hasPlaintext("cust-003"));

console.log("\n=== 4. Anyone can verify the erasure proof ===");
show("erasure proof valid", verifyErasureProof(erasure, ledger.root));

console.log("\n=== 5. Remaining customers' proofs still verify against the NEW root ===");
for (const [id, data] of records) {
  if (id === "cust-003") continue;
  const proof = ledger.getInclusionProof(id);
  show(`  ${id} valid`, verifyInclusionProof(id, data, proof, ledger.root));
}

console.log("\n=== 6. Root evolution history (smooth audit trail) ===");
for (const snap of ledger.history) {
  console.log(`  epoch ${snap.epoch} [${snap.reason.padEnd(6)}] ${snap.recordId} -> ${snap.root.slice(0, 16)}...`);
}
