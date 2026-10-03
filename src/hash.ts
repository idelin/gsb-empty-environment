import { createHash } from "node:crypto";

export type Hex = string;

const DOMAIN_LEAF = Buffer.from("COMPLIANCE_LEAF/v1", "utf8");
const DOMAIN_NODE = Buffer.from("COMPLIANCE_NODE/v1", "utf8");
const DOMAIN_ERASE = Buffer.from("COMPLIANCE_ERASE_MARKER/v1", "utf8");
const DOMAIN_ERASE_REQ = Buffer.from("COMPLIANCE_ERASE_REQUEST/v1", "utf8");

function sha256(...parts: Buffer[]): Hex {
  const h = createHash("sha256");
  for (const p of parts) h.update(p);
  return h.digest("hex");
}

export function hashData(data: Buffer): Hex {
  return sha256(data);
}

export function hashLeaf(recordId: string, dataHash: Hex): Hex {
  return sha256(DOMAIN_LEAF, Buffer.from(recordId, "utf8"), Buffer.from(dataHash, "hex"));
}

export function hashNode(left: Hex, right: Hex): Hex {
  return sha256(DOMAIN_NODE, Buffer.from(left, "hex"), Buffer.from(right, "hex"));
}

export function hashEraseMarker(recordId: string, originalLeafHash: Hex, deletionSignature: Hex): Hex {
  return sha256(
    DOMAIN_ERASE,
    Buffer.from(recordId, "utf8"),
    Buffer.from(originalLeafHash, "hex"),
    Buffer.from(deletionSignature, "hex"),
  );
}

export function eraseRequestMessage(recordId: string, leafHash: Hex, requestedAt: string): Buffer {
  return Buffer.concat([
    DOMAIN_ERASE_REQ,
    Buffer.from(recordId, "utf8"),
    Buffer.from(leafHash, "hex"),
    Buffer.from(requestedAt, "utf8"),
  ]);
}
