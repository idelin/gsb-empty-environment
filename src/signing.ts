import {
  createHash,
  createPrivateKey,
  createPublicKey,
  generateKeyPairSync,
  sign,
  verify,
  type KeyObject,
} from "node:crypto";
import type { KeyId, SignedEnvelope } from "./types.ts";

/**
 * Deterministic canonical JSON: object keys are sorted recursively so that a
 * signature means the same bytes regardless of input insertion order.
 */
export function canonicalJSON(value: unknown): string {
  if (value === null || typeof value !== "object") {
    return JSON.stringify(value ?? null);
  }
  if (Array.isArray(value)) {
    return `[${value.map((entry) => canonicalJSON(entry)).join(",")}]`;
  }
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record).sort();
  return `{${keys
    .map((key) => `${JSON.stringify(key)}:${canonicalJSON(record[key])}`)
    .join(",")}}`;
}

export function canonicalDigest(value: unknown): Buffer {
  return createHash("sha256").update(canonicalJSON(value), "utf8").digest();
}

export interface KeyPair {
  keyId: KeyId;
  privateKey: KeyObject;
  publicKey: KeyObject;
}

export function generateKeyPair(keyId: KeyId): KeyPair {
  const { privateKey, publicKey } = generateKeyPairSync("ed25519");
  return { keyId, privateKey, publicKey };
}

export function exportPublicKeyPem(publicKey: KeyObject): string {
  return publicKey.export({ format: "pem", type: "spki" }).toString();
}

export function importPublicKeyPem(pem: string): KeyObject {
  return createPublicKey(pem);
}

export function importPrivateKeyPem(pem: string): KeyObject {
  return createPrivateKey(pem);
}

export function signStatement<T>(statement: T, privateKey: KeyObject): string {
  return sign(null, canonicalDigest(statement), privateKey).toString("base64");
}

export function sealStatement<T>(statement: T, key: KeyPair): SignedEnvelope<T> {
  return { statement, signature: signStatement(statement, key.privateKey) };
}

/**
 * Verify an envelope against a key directory. Returns false rather than
 * throwing so callers can route unverifiable input into "pending".
 */
export function verifyEnvelope<T>(
  envelope: SignedEnvelope<T>,
  directory: { getPublicKey(keyId: KeyId): KeyObject | undefined },
  keyIdOf: (statement: T) => KeyId,
): boolean {
  const publicKey = directory.getPublicKey(keyIdOf(envelope.statement));
  if (!publicKey) {
    return false;
  }
  try {
    const signature = Buffer.from(envelope.signature, "base64");
    return verify(null, canonicalDigest(envelope.statement), publicKey, signature);
  } catch {
    return false;
  }
}
