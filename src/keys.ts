import { createPrivateKey, createPublicKey, generateKeyPairSync, sign as signBytes, verify as verifyBytes, type KeyObject } from "node:crypto";
import { canonicalize } from "./canonical.ts";

/**
 * In-memory Ed25519 key directory. This is the entire identity model:
 * principals are plain strings bound to public keys by explicit registration.
 * No network, certificates, or external identity service are ever consulted.
 */
export class KeyDirectory {
  private readonly publicKeys = new Map<string, KeyObject>();
  private readonly privateKeys = new Map<string, KeyObject>();

  generatePrincipal(name: string): KeyObject {
    if (this.publicKeys.has(name)) {
      throw new Error(`principal already registered: ${name}`);
    }
    const { publicKey, privateKey } = generateKeyPairSync("ed25519");
    this.publicKeys.set(name, publicKey);
    this.privateKeys.set(name, privateKey);
    return privateKey;
  }

  registerPublicKey(name: string, pem: string): void {
    if (this.publicKeys.has(name)) {
      throw new Error(`principal already registered: ${name}`);
    }
    this.publicKeys.set(name, createPublicKey(pem));
  }

  registerKeyPair(name: string, privateKeyPem: string): void {
    const privateKey = createPrivateKey(privateKeyPem);
    const publicKey = createPublicKey(privateKey);
    this.publicKeys.set(name, publicKey);
    this.privateKeys.set(name, privateKey);
  }

  has(name: string): boolean {
    return this.publicKeys.has(name);
  }

  exportPrivatePem(name: string): string {
    const key = this.privateKeys.get(name);
    if (!key) throw new Error(`no private key for principal: ${name}`);
    return key.export({ type: "pkcs8", format: "pem" }).toString();
  }

  sign(name: string, payload: unknown): string {
    const key = this.privateKeys.get(name);
    if (!key) throw new Error(`no private key for principal: ${name}`);
    return signBytes(null, Buffer.from(canonicalize(payload), "utf8"), key).toString("base64");
  }

  verify(name: string, payload: unknown, signatureB64: string): boolean {
    const key = this.publicKeys.get(name);
    if (!key) return false;
    let signature: Buffer;
    try {
      signature = Buffer.from(signatureB64, "base64");
    } catch {
      return false;
    }
    try {
      return verifyBytes(null, Buffer.from(canonicalize(payload), "utf8"), key, signature);
    } catch {
      return false;
    }
  }
}
