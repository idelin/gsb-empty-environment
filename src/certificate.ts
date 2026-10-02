import { MerkleTree, hashLeaf, type ProofStep } from './merkle.ts';
import { Anchorer, type SignedAnchor } from './anchorer.ts';

/** Proof that a piece of data existed at or before the anchor's timestamp. */
export interface AuditCertificate {
  leafHash: string;     // hex, hashLeaf(data)
  leafIndex: number;
  treeSize: number;
  proof: ProofStep[];   // Merkle inclusion path
  anchor: SignedAnchor; // timestamped, chained, signed anchor of the tree root
}

export interface CertificateVerdict {
  valid: boolean;
  /** The data is proven to have existed at or before this timestamp. */
  existedBefore?: number;
  reason?: string;
}

export function issueCertificate(tree: MerkleTree, leafIndex: number, anchor: SignedAnchor): AuditCertificate {
  if (anchor.record.merkleRoot !== tree.root.toString('hex')) {
    throw new Error('anchor does not match this tree root');
  }
  return {
    leafHash: tree.leafHash(leafIndex).toString('hex'),
    leafIndex,
    treeSize: tree.size,
    proof: tree.getProof(leafIndex),
    anchor,
  };
}

/**
 * Verify a certificate against the anchorer's trusted public key.
 * Succeeds only if the Merkle path reconstructs the anchored root AND the
 * anchor signature is valid — binding the data to the anchor's timestamp.
 */
export function verifyCertificate(
  data: Buffer | string,
  cert: AuditCertificate,
  trustedPublicKeyPem: string,
): CertificateVerdict {
  if (hashLeaf(data).toString('hex') !== cert.leafHash) {
    return { valid: false, reason: 'data does not match certificate leaf hash' };
  }
  if (cert.anchor.publicKey !== trustedPublicKeyPem) {
    return { valid: false, reason: 'anchor signed by untrusted key' };
  }
  if (!MerkleTree.verifyProof(cert.leafHash, cert.proof, cert.anchor.record.merkleRoot)) {
    return { valid: false, reason: 'merkle inclusion proof does not reconstruct the anchored root' };
  }
  if (!Anchorer.verifySignature(cert.anchor)) {
    return { valid: false, reason: 'invalid anchor signature' };
  }
  return { valid: true, existedBefore: cert.anchor.record.timestamp };
}
