import type { JsonWebKey } from "./webauthn-types.ts";

export type PublicKeyInput = JsonWebKey | Uint8Array | { readonly keyObject: unknown };

export type BackupEligibility = boolean;

export interface CredentialRecord {
  /** Credential identifier (raw bytes, base64url string or `Credential.id`). */
  credentialId: Uint8Array | string;
  /** Public key accepted by the signature layer (JWK, SPKI DER or KeyObject). */
  publicKey: PublicKeyInput;
  /** COSE algorithm identifier, e.g. -7 (ES256), -8 (EdDSA), -257 (RS256). */
  algorithm: number;
  /** Last accepted authenticator signature counter. */
  signCount: number;
  /** Backup Eligibility flag captured at registration. */
  backupEligible: BackupEligibility;
  /** Most recently observed Backup State flag. */
  backupState: boolean;
  /** Set to true once clone/state-machine evidence locks the credential. */
  locked: boolean;
  /** Optional human/host reason for the lock. */
  lockedReason?: string;
  /** Last accepted timestamp (epoch milliseconds), maintained by the verifier. */
  updatedAt?: number;
}

export interface CredentialStore {
  get(credentialId: Uint8Array): Promise<CredentialRecord | null> | CredentialRecord | null;
  save(record: CredentialRecord): Promise<void> | void;
}

/**
 * Reference in-memory store. Hosts with a database should implement
 * `CredentialStore` and make `save` participate in the login transaction.
 */
export class InMemoryCredentialStore implements CredentialStore {
  private readonly records = new Map<string, CredentialRecord>();

  get(credentialId: Uint8Array): CredentialRecord | null {
    const key = encodeKey(credentialId);
    return this.records.get(key) ?? null;
  }

  save(record: CredentialRecord): void {
    const key = encodeKey(toIdentifierBytes(record.credentialId));
    this.records.set(key, record);
  }

  size(): number {
    return this.records.size;
  }

  clear(): void {
    this.records.clear();
  }
}

export function toIdentifierBytes(credentialId: Uint8Array | string): Uint8Array {
  if (credentialId instanceof Uint8Array) {
    return new Uint8Array(credentialId);
  }
  if (typeof credentialId === "string") {
    // Browser credential ids are base64url; accept them directly.
    const binary = atobPolished(credentialId);
    const bytes = new Uint8Array(binary.length);
    for (let index = 0; index < binary.length; index += 1) {
      bytes[index] = binary.charCodeAt(index);
    }
    return bytes;
  }
  throw new TypeError("credentialId must be Uint8Array or base64url string");
}

function encodeKey(bytes: Uint8Array): string {
  let output = "";
  for (const byte of bytes) {
    output += byte.toString(16).padStart(2, "0");
  }
  return output;
}

function atobPolished(value: string): string {
  const normalized = value.replace(/-/gu, "+").replace(/_/gu, "/");
  const padded = normalized.padEnd(normalized.length + ((4 - (normalized.length % 4)) % 4), "=");
  if (typeof globalThis.atob === "function") {
    return globalThis.atob(padded);
  }
  return Buffer.from(padded, "base64").toString("binary");
}
