import { createPrivateKey, createPublicKey, generateKeyPairSync, type KeyObject } from "node:crypto";

import { keyIdForPublicKey, signPayload } from "./keys.ts";
import type { Ed25519Credential } from "./types.ts";

export interface Ed25519CredentialDouble extends Ed25519Credential {
  publicKey: string;
  privateKey: KeyObject;
}

export interface CredentialDoubleOptions {
  privateKeyPem?: string;
  publicKeyPem?: string;
}

export function createEd25519CredentialDouble(
  options: CredentialDoubleOptions = {},
): Ed25519CredentialDouble {
  let privateKey: KeyObject;
  let publicKey: KeyObject;
  if (options.privateKeyPem && options.publicKeyPem) {
    privateKey = createPrivateKey(options.privateKeyPem);
    publicKey = createPublicKey(options.publicKeyPem);
  } else {
    const pair = generateKeyPairSync("ed25519");
    privateKey = pair.privateKey;
    publicKey = pair.publicKey;
  }
  const publicKeyPem = publicKey.export({ type: "spki", format: "pem" }).toString();
  return {
    keyId: keyIdForPublicKey(publicKey),
    publicKey: publicKeyPem,
    privateKey,
    signPayload(domain: string, payload: unknown): string {
      return signPayload(privateKey, domain, payload);
    },
  };
}

export function createDeviceCredentialDouble(
  options: CredentialDoubleOptions & { deviceId?: string } = {},
): Ed25519CredentialDouble & { deviceId?: string } {
  const credential = createEd25519CredentialDouble(options);
  return { ...credential, deviceId: options.deviceId };
}

export function createBrowserCredentialDouble(
  options: CredentialDoubleOptions & { browserId?: string; browserSessionId?: string | null } = {},
): Ed25519CredentialDouble & { browserId?: string; browserSessionId?: string | null } {
  const credential = createEd25519CredentialDouble(options);
  return {
    ...credential,
    browserId: options.browserId,
    browserSessionId: options.browserSessionId,
  };
}
