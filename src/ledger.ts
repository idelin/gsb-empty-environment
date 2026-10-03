import {
  createPublicKey,
  generateKeyPairSync,
  sign as edSign,
  verify as edVerify,
  type KeyObject,
} from "node:crypto";
import {
  hashData,
  hashLeaf,
  hashEraseMarker,
  eraseRequestMessage,
  type Hex,
} from "./hash.ts";
import {
  buildProof,
  rootOf,
  verifyProof,
  type MerkleProof,
} from "./tree.ts";

export interface ErasureProof {
  recordId: string;
  leafIndex: number;
  originalLeafHash: Hex;
  eraseMarkerHash: Hex;
  deletionSignature: Hex;
  requestedAt: string;
  officerPublicKeyPem: string;
  merkleProof: MerkleProof;
  root: Hex;
}

export interface RootSnapshot {
  epoch: number;
  root: Hex;
  reason: "append" | "erase";
  recordId: string;
}

interface LeafSlot {
  recordId: string;
  leafHash: Hex;
  erased: boolean;
  erasure?: {
    originalLeafHash: Hex;
    deletionSignature: Hex;
    requestedAt: string;
  };
}

export class ComplianceLedger {
  private readonly officerPrivateKey: KeyObject;
  readonly officerPublicKey: KeyObject;
  readonly officerPublicKeyPem: string;

  private readonly slots: LeafSlot[] = [];
  private readonly recordIndex = new Map<string, number>();
  private readonly plaintextStore = new Map<string, Buffer>();
  private readonly rootHistory: RootSnapshot[] = [];

  constructor(officerKeys?: { privateKey: KeyObject; publicKey: KeyObject }) {
    const keys = officerKeys ?? generateKeyPairSync("ed25519");
    this.officerPrivateKey = keys.privateKey;
    this.officerPublicKey = keys.publicKey;
    this.officerPublicKeyPem = keys.publicKey
      .export({ type: "spki", format: "pem" })
      .toString();
  }

  get size(): number {
    return this.slots.length;
  }

  get root(): Hex {
    if (this.slots.length === 0) throw new Error("ledger is empty");
    return rootOf(this.slots.map((s) => s.leafHash));
  }

  get history(): readonly RootSnapshot[] {
    return this.rootHistory;
  }

  addRecord(recordId: string, data: Buffer | string): MerkleProof {
    if (this.recordIndex.has(recordId)) {
      throw new Error(`record ${recordId} already exists`);
    }
    const payload = typeof data === "string" ? Buffer.from(data, "utf8") : Buffer.from(data);
    const leafHash = hashLeaf(recordId, hashData(payload));
    this.slots.push({ recordId, leafHash, erased: false });
    this.recordIndex.set(recordId, this.slots.length - 1);
    this.plaintextStore.set(recordId, payload);
    this.snapshot("append", recordId);
    return this.getInclusionProof(recordId);
  }

  eraseRecord(recordId: string, requestedAt: string = new Date().toISOString()): ErasureProof {
    const index = this.recordIndex.get(recordId);
    if (index === undefined) throw new Error(`unknown record ${recordId}`);
    const slot = this.slots[index];
    if (slot.erased) throw new Error(`record ${recordId} already erased`);

    const originalLeafHash = slot.leafHash;
    const message = eraseRequestMessage(recordId, originalLeafHash, requestedAt);
    const deletionSignature = edSign(null, message, this.officerPrivateKey).toString("hex");
    const marker = hashEraseMarker(recordId, originalLeafHash, deletionSignature);

    slot.leafHash = marker;
    slot.erased = true;
    slot.erasure = { originalLeafHash, deletionSignature, requestedAt };

    this.securelyDestroyPlaintext(recordId);
    this.snapshot("erase", recordId);

    return {
      recordId,
      leafIndex: index,
      originalLeafHash,
      eraseMarkerHash: marker,
      deletionSignature,
      requestedAt,
      officerPublicKeyPem: this.officerPublicKeyPem,
      merkleProof: buildProof(this.slots.map((s) => s.leafHash), index),
      root: this.root,
    };
  }

  getInclusionProof(recordId: string): MerkleProof {
    const index = this.recordIndex.get(recordId);
    if (index === undefined) throw new Error(`unknown record ${recordId}`);
    return buildProof(this.slots.map((s) => s.leafHash), index);
  }

  leafHashOf(recordId: string): Hex {
    const index = this.recordIndex.get(recordId);
    if (index === undefined) throw new Error(`unknown record ${recordId}`);
    return this.slots[index].leafHash;
  }

  isErased(recordId: string): boolean {
    const index = this.recordIndex.get(recordId);
    if (index === undefined) throw new Error(`unknown record ${recordId}`);
    return this.slots[index].erased;
  }

  hasPlaintext(recordId: string): boolean {
    return this.plaintextStore.has(recordId);
  }

  readPlaintext(recordId: string): Buffer | undefined {
    return this.plaintextStore.get(recordId);
  }

  private securelyDestroyPlaintext(recordId: string): void {
    const payload = this.plaintextStore.get(recordId);
    if (payload) {
      payload.fill(0);
      this.plaintextStore.delete(recordId);
    }
  }

  private snapshot(reason: "append" | "erase", recordId: string): void {
    this.rootHistory.push({
      epoch: this.rootHistory.length,
      root: this.root,
      reason,
      recordId,
    });
  }
}

export function verifyInclusionProof(
  recordId: string,
  data: Buffer | string,
  proof: MerkleProof,
  expectedRoot?: Hex,
): boolean {
  const payload = typeof data === "string" ? Buffer.from(data, "utf8") : data;
  if (proof.leafHash !== hashLeaf(recordId, hashData(payload))) return false;
  return verifyProof(proof, expectedRoot);
}

export function verifyErasureProof(proof: ErasureProof, expectedRoot?: Hex): boolean {
  const message = eraseRequestMessage(proof.recordId, proof.originalLeafHash, proof.requestedAt);
  let publicKey: KeyObject;
  try {
    publicKey = createPublicKey(proof.officerPublicKeyPem);
  } catch {
    return false;
  }
  const signatureOk = edVerify(
    null,
    message,
    publicKey,
    Buffer.from(proof.deletionSignature, "hex"),
  );
  if (!signatureOk) return false;

  const expectedMarker = hashEraseMarker(
    proof.recordId,
    proof.originalLeafHash,
    proof.deletionSignature,
  );
  if (expectedMarker !== proof.eraseMarkerHash) return false;
  if (proof.merkleProof.leafHash !== proof.eraseMarkerHash) return false;
  if (proof.merkleProof.leafIndex !== proof.leafIndex) return false;
  return verifyProof(proof.merkleProof, expectedRoot ?? proof.root);
}
