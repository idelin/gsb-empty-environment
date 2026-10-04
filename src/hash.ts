import { createHash } from "node:crypto";

export type Hash = Uint8Array;

export const HASH_LENGTH = 32;

/** Domain-separation prefixes. */
export const LEAF_PREFIX = 0x00;
export const NODE_PREFIX = 0x01;
export const EMPTY_TREE_PREFIX = 0x02;
export const COMMITMENT_DOMAIN = "MRV1"; // Merkle Root V1

export function sha256(...parts: Uint8Array[]): Hash {
  const h = createHash("sha256");
  for (const p of parts) h.update(p);
  return new Uint8Array(h.digest());
}

export function u64le(value: number): Uint8Array {
  const b = Buffer.alloc(8);
  b.writeBigUInt64LE(BigInt(value));
  return new Uint8Array(b);
}

export function u32le(value: number): Uint8Array {
  const b = Buffer.alloc(4);
  b.writeUInt32LE(value);
  return new Uint8Array(b);
}

export function hashesEqual(a: Hash, b: Hash): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a[i] ^ b[i];
  return diff === 0;
}

export function toHex(h: Hash): string {
  return Buffer.from(h).toString("hex");
}

export function fromHex(s: string): Hash {
  return new Uint8Array(Buffer.from(s, "hex"));
}
