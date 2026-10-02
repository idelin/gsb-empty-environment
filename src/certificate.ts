import type { KeyObject } from "node:crypto";
import {
  buildTree,
  getProof,
  hashLeaf,
  verifyProof,
  type MerkleTree,
  type ProofStep,
} from "./merkle.ts";
import {
  verifyAnchorSignature,
  type AnchorRecord,
} from "./anchor.ts";

export interface AuditCertificate {
  leafHash: string;
  leafIndex: number;
  proof: ProofStep[];
  anchor: AnchorRecord;
  issuedAt: number;
}

export interface VerificationResult {
  ok: boolean;
  checks: Record<string, boolean>;
  reason?: string;
}

export class CertificateIssuer {
  private tree: MerkleTree | null = null;
  private leafData: Buffer[] = [];

  ingest(records: (string | Buffer)[]): void {
    this.leafData = records.map((r) =>
      typeof r === "string" ? Buffer.from(r, "utf8") : r,
    );
    this.tree = buildTree(this.leafData.map((d) => hashLeaf(d)));
  }

  get root(): Buffer {
    if (!this.tree) throw new Error("no records ingested yet");
    return this.tree.root;
  }

  issue(data: string | Buffer, anchor: AnchorRecord, issuedAt: number): AuditCertificate {
    if (!this.tree) throw new Error("no records ingested yet");
    const payload = typeof data === "string" ? Buffer.from(data, "utf8") : data;
    const index = this.leafData.findIndex((d) => d.equals(payload));
    if (index === -1) {
      throw new Error("data was not part of the anchored batch");
    }
    return {
      leafHash: hashLeaf(payload).toString("hex"),
      leafIndex: index,
      proof: getProof(this.tree, index),
      anchor,
      issuedAt,
    };
  }
}

export function verifyCertificate(
  cert: AuditCertificate,
  data: string | Buffer,
  publicKey: KeyObject,
  options: { notLaterThan?: number } = {},
): VerificationResult {
  const checks: Record<string, boolean> = {};
  const payload = typeof data === "string" ? Buffer.from(data, "utf8") : data;
  const leafHash = hashLeaf(payload);

  checks.leafHashMatches = leafHash.toString("hex") === cert.leafHash;
  if (!checks.leafHashMatches) {
    return { ok: false, checks, reason: "data does not match certificate leaf hash" };
  }

  const root = Buffer.from(cert.anchor.merkleRoot, "hex");
  checks.merkleProofValid = verifyProof(leafHash, cert.proof, root);
  if (!checks.merkleProofValid) {
    return { ok: false, checks, reason: "merkle proof does not lead to anchored root" };
  }

  checks.anchorSignatureValid = verifyAnchorSignature(cert.anchor, publicKey);
  if (!checks.anchorSignatureValid) {
    return { ok: false, checks, reason: "anchor signature invalid" };
  }

  if (options.notLaterThan !== undefined) {
    checks.anchoredBeforeDeadline = cert.anchor.timestamp <= options.notLaterThan;
    if (!checks.anchoredBeforeDeadline) {
      return {
        ok: false,
        checks,
        reason: `anchor timestamp ${cert.anchor.timestamp} is after required bound ${options.notLaterThan}`,
      };
    }
  }

  return { ok: true, checks };
}
