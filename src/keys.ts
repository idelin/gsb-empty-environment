import {
  createPrivateKey,
  createPublicKey,
  generateKeyPairSync,
  sign,
  verify,
  type KeyObject,
} from "node:crypto";

import { canonicalJson, sha256 } from "./canonical.ts";
import type { HostKeyPair } from "./types.ts";

export const TOKEN_DOMAIN = "ddd-cross-device/email-token/v1";
export const CONFIRMATION_DOMAIN = "ddd-cross-device/device-confirmation/v1";
export const BROWSER_PROOF_DOMAIN = "ddd-cross-device/browser-proof/v1";
export const RECEIPT_DOMAIN = "ddd-cross-device/receipt/v1";
export const JOURNAL_DOMAIN = "ddd-cross-device/journal-record/v1";

export function createEd25519HostKeyPair(): HostKeyPair {
  const { privateKey, publicKey } = generateKeyPairSync("ed25519");
  const publicKeyPem = publicKey.export({ type: "spki", format: "pem" }).toString();
  return {
    privateKey,
    publicKey,
    publicKeyPem,
    keyId: keyIdForPublicKey(publicKey),
  };
}

export function hostKeyPairFromPem(privateKeyPem: string, publicKeyPem: string): HostKeyPair {
  const privateKey = createPrivateKey(privateKeyPem);
  const publicKey = createPublicKey(publicKeyPem);
  return {
    privateKey,
    publicKey,
    publicKeyPem,
    keyId: keyIdForPublicKey(publicKey),
  };
}

export function keyIdForPublicKey(publicKey: KeyObject | string): string {
  const key = typeof publicKey === "string" ? createPublicKey(publicKey) : publicKey;
  const der = key.export({ type: "spki", format: "der" });
  return `ed25519_${sha256(der)}`;
}

export function signPayload(privateKey: KeyObject, domain: string, payload: unknown): string {
  const message = Buffer.from(domain + "\n" + canonicalJson(payload), "utf8");
  return sign(null, message, privateKey).toString("base64url");
}

export function verifyPayload(
  publicKey: KeyObject | string,
  domain: string,
  payload: unknown,
  signature: string,
): boolean {
  try {
    const key = typeof publicKey === "string" ? createPublicKey(publicKey) : publicKey;
    const message = Buffer.from(domain + "\n" + canonicalJson(payload), "utf8");
    return verify(null, message, key, Buffer.from(signature, "base64url"));
  } catch {
    return false;
  }
}

export function normalizePublicKey(publicKey: string): string {
  return createPublicKey(publicKey).export({ type: "spki", format: "pem" }).toString();
}
