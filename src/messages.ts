import { canonicalJson, deepFreeze, randomId, sha256 } from "./canonical.ts";
import {
  BROWSER_PROOF_DOMAIN,
  CONFIRMATION_DOMAIN,
  TOKEN_DOMAIN,
  keyIdForPublicKey,
  signPayload,
  verifyPayload,
} from "./keys.ts";
import type {
  BrowserProof,
  BrowserProofPayload,
  DeviceConfirmation,
  DeviceConfirmationPayload,
  Ed25519Credential,
  EmailToken,
  EmailTokenClaims,
  HostKeyPair,
} from "./types.ts";

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object";
}

function requireString(record: Record<string, unknown>, key: string): string {
  const value = record[key];
  if (typeof value !== "string") throw new TypeError(`Expected string field ${key}`);
  return value;
}

function requireNumber(record: Record<string, unknown>, key: string): number {
  const value = record[key];
  if (typeof value !== "number" || !Number.isFinite(value)) {
    throw new TypeError(`Expected number field ${key}`);
  }
  return value;
}

function validateTokenClaims(value: unknown): EmailTokenClaims {
  if (!isRecord(value)) throw new TypeError("Email token claims must be an object");
  if (value.v !== 1) throw new TypeError("Unsupported email token version");
  const session = value.browserSessionId;
  if (session !== null && typeof session !== "string") {
    throw new TypeError("browserSessionId must be a string or null");
  }
  return {
    v: 1,
    tokenId: requireString(value, "tokenId"),
    challengeId: requireString(value, "challengeId"),
    accountId: requireString(value, "accountId"),
    email: requireString(value, "email"),
    browserId: requireString(value, "browserId"),
    browserSessionId: session,
    browserKeyId: requireString(value, "browserKeyId"),
    browserPublicKey: requireString(value, "browserPublicKey"),
    challenge: requireString(value, "challenge"),
    purpose: requireString(value, "purpose"),
    userCode: requireString(value, "userCode"),
    issuedAt: requireNumber(value, "issuedAt"),
    expiresAt: requireNumber(value, "expiresAt"),
  };
}

export function createEmailToken(claims: EmailTokenClaims, hostKeyPair: HostKeyPair): EmailToken {
  const signature = signPayload(hostKeyPair.privateKey, TOKEN_DOMAIN, claims);
  const encoded = Buffer.from(JSON.stringify(claims), "utf8").toString("base64url") + "." + signature;
  return deepFreeze({ claims: structuredClone(claims), signature, encoded });
}

export function parseEmailToken(input: EmailToken | string): EmailToken {
  if (typeof input !== "string") {
    if (!isRecord(input) || !isRecord(input.claims)) throw new TypeError("Invalid email token object");
    return input as EmailToken;
  }
  const parts = input.split(".");
  if (parts.length !== 2) throw new TypeError("Malformed email token");
  const [body, signature] = parts;
  if (!body || !signature) throw new TypeError("Malformed email token");
  const claims = validateTokenClaims(JSON.parse(Buffer.from(body, "base64url").toString("utf8")));
  const encoded = `${body}.${signature}`;
  return deepFreeze({ claims, signature, encoded });
}

export function verifyEmailToken(token: EmailToken, hostPublicKey: string): boolean {
  return verifyPayload(hostPublicKey, TOKEN_DOMAIN, token.claims, token.signature);
}

export function emailTokenHash(token: EmailToken): string {
  return sha256(token.encoded);
}

export function createDeviceConfirmation(
  credential: Ed25519Credential,
  tokenInput: EmailToken | string,
  options: {
    deviceId: string;
    browserId?: string;
    browserSessionId?: string | null;
    confirmedAt?: number;
  },
): DeviceConfirmation {
  const token = parseEmailToken(tokenInput);
  const selectedBrowserId = options.browserId ?? token.claims.browserId;
  const selectedBrowserSessionId =
    options.browserSessionId === undefined
      ? token.claims.browserSessionId
      : options.browserSessionId;
  const payload: DeviceConfirmationPayload = {
    v: 1,
    confirmationId: randomId("cnf"),
    tokenId: token.claims.tokenId,
    challengeId: token.claims.challengeId,
    challenge: token.claims.challenge,
    accountId: token.claims.accountId,
    email: token.claims.email,
    purpose: token.claims.purpose,
    expiresAt: token.claims.expiresAt,
    browserId: token.claims.browserId,
    browserSessionId: token.claims.browserSessionId,
    browserKeyId: token.claims.browserKeyId,
    selectedBrowserId,
    selectedBrowserSessionId,
    deviceId: options.deviceId,
    deviceKeyId: credential.keyId,
    confirmedAt: options.confirmedAt ?? Date.now(),
  };
  const signature = credential.signPayload(CONFIRMATION_DOMAIN, payload);
  return deepFreeze({ payload, signature, signerPublicKey: credential.publicKey });
}

export function verifyDeviceConfirmation(confirmation: DeviceConfirmation): boolean {
  try {
    const payload = confirmation.payload;
    if (payload.v !== 1) return false;
    if (keyIdForPublicKey(confirmation.signerPublicKey) !== payload.deviceKeyId) return false;
    return verifyPayload(
      confirmation.signerPublicKey,
      CONFIRMATION_DOMAIN,
      payload,
      confirmation.signature,
    );
  } catch {
    return false;
  }
}

export function confirmationPayloadHash(confirmation: DeviceConfirmation): string {
  return sha256(CONFIRMATION_DOMAIN + "\n" + canonicalJson(confirmation.payload));
}

export function createBrowserProof(
  credential: Ed25519Credential,
  tokenInput: EmailToken | string,
  options: { browserId?: string; browserSessionId?: string | null; signedAt?: number } = {},
): BrowserProof {
  const token = parseEmailToken(tokenInput);
  const payload: BrowserProofPayload = {
    v: 1,
    proofId: randomId("prf"),
    tokenId: token.claims.tokenId,
    challengeId: token.claims.challengeId,
    challenge: token.claims.challenge,
    accountId: token.claims.accountId,
    browserId: options.browserId ?? token.claims.browserId,
    browserSessionId:
      options.browserSessionId === undefined
        ? token.claims.browserSessionId
        : options.browserSessionId,
    browserKeyId: token.claims.browserKeyId,
    purpose: token.claims.purpose,
    signedAt: options.signedAt ?? Date.now(),
  };
  const signature = credential.signPayload(BROWSER_PROOF_DOMAIN, payload);
  return deepFreeze({ payload, signature, signerPublicKey: credential.publicKey });
}

export function verifyBrowserProof(proof: BrowserProof, expectedBrowserPublicKey?: string): boolean {
  try {
    const payload = proof.payload;
    if (payload.v !== 1) return false;
    const signerKeyId = keyIdForPublicKey(proof.signerPublicKey);
    if (signerKeyId !== payload.browserKeyId) return false;
    if (expectedBrowserPublicKey !== undefined) {
      if (keyIdForPublicKey(expectedBrowserPublicKey) !== payload.browserKeyId) return false;
    }
    return verifyPayload(
      proof.signerPublicKey,
      BROWSER_PROOF_DOMAIN,
      payload,
      proof.signature,
    );
  } catch {
    return false;
  }
}

export function browserProofPayloadHash(proof: BrowserProof): string {
  return sha256(BROWSER_PROOF_DOMAIN + "\n" + canonicalJson(proof.payload));
}
