import {
  createHash,
  generateKeyPairSync,
  sign as cryptoSign,
  verify as cryptoVerify,
} from "node:crypto";
import { canonicalize } from "./canonical.ts";
import type {
  SignedPolicySource,
  SignedTenantRoot,
  SignedWaiver,
  SourceClaims,
  TenantRootClaims,
  TenantRootIssuer,
  TimeInterval,
  TrustOptions,
  WaiverClaims,
} from "./types.ts";
import { intervalContains, toEpochMs } from "./time.ts";

export interface SigningKeyPair {
  publicKey: string;
  privateKey: string;
}

export function generateSigningKeyPair(): SigningKeyPair {
  const pair = generateKeyPairSync("ed25519");
  return {
    publicKey: pair.publicKey.export({ type: "spki", format: "pem" }).toString(),
    privateKey: pair.privateKey.export({ type: "pkcs8", format: "pem" }).toString(),
  };
}

export function signPayload(privateKey: string, payload: unknown): string {
  return cryptoSign(null, Buffer.from(canonicalize(payload), "utf8"), privateKey).toString("base64");
}

export function verifyPayload(publicKey: string, payload: unknown, signature: string): boolean {
  try {
    return cryptoVerify(
      null,
      Buffer.from(canonicalize(payload), "utf8"),
      publicKey,
      Buffer.from(signature, "base64"),
    );
  } catch {
    return false;
  }
}

export function publicKeyFingerprint(publicKey: string): string {
  return createHash("sha256").update(publicKey, "utf8").digest("hex");
}

function rootClaimsWithoutSignature(root: SignedTenantRoot): TenantRootClaims {
  return {
    kind: "tenant-root",
    tenantId: root.tenantId,
    rootId: root.rootId,
    publicKey: root.publicKey,
    issuedAt: root.issuedAt,
    expiresAt: root.expiresAt,
  };
}

export function createTenantRoot(options: {
  tenantId: string;
  rootId?: string;
  issuedAt?: number;
  expiresAt?: number;
  keyPair?: SigningKeyPair;
}): TenantRootIssuer {
  const pair = options.keyPair ?? generateSigningKeyPair();
  const claims: TenantRootClaims = {
    kind: "tenant-root",
    tenantId: options.tenantId,
    rootId: options.rootId ?? `${options.tenantId}:root`,
    publicKey: pair.publicKey,
    issuedAt: options.issuedAt ?? 0,
    expiresAt: options.expiresAt,
  };
  return {
    root: {
      ...claims,
      signature: signPayload(pair.privateKey, claims),
    },
    privateKey: pair.privateKey,
  };
}

export function verifyTenantRoot(
  root: SignedTenantRoot,
  options: { at?: number | string | Date; trust?: TrustOptions } = {},
): { ok: boolean; reason?: "invalid-tenant-root" | "untrusted-tenant-root" | "expired-tenant-root" } {
  if (!verifyPayload(root.publicKey, rootClaimsWithoutSignature(root), root.signature)) {
    return { ok: false, reason: "invalid-tenant-root" };
  }
  const trust = options.trust;
  const fingerprint = publicKeyFingerprint(root.publicKey);
  if (
    (trust?.requireTrustedRoots || trust?.trustedRootFingerprints !== undefined) &&
    !(trust?.trustedRootFingerprints ?? []).includes(fingerprint)
  ) {
    return { ok: false, reason: "untrusted-tenant-root" };
  }
  if (options.at !== undefined && root.expiresAt !== undefined && toEpochMs(options.at) >= root.expiresAt) {
    return { ok: false, reason: "expired-tenant-root" };
  }
  if (options.at !== undefined && toEpochMs(options.at) < root.issuedAt) {
    return { ok: false, reason: "invalid-tenant-root" };
  }
  if (root.expiresAt !== undefined && root.issuedAt >= root.expiresAt) {
    return { ok: false, reason: "expired-tenant-root" };
  }
  return { ok: true };
}

function sourceClaimsWithoutSignature(source: SignedPolicySource): SourceClaims {
  return {
    kind: "policy-source",
    id: source.id,
    signerId: source.signerId,
    publicKey: source.publicKey,
    issuedAt: source.issuedAt,
    expiresAt: source.expiresAt,
    description: source.description,
  };
}

export function createPolicySource(options: {
  id: string;
  signerId: string;
  privateKey?: string;
  publicKey?: string;
  issuedAt?: number;
  expiresAt?: number;
  description?: string;
  keyPair?: SigningKeyPair;
}): SignedPolicySource {
  const keyPair = options.keyPair ?? {
    publicKey: options.publicKey ?? "",
    privateKey: options.privateKey ?? "",
  };
  if (!keyPair.publicKey) {
    throw new TypeError("createPolicySource needs publicKey or keyPair.publicKey");
  }
  const claims: SourceClaims = {
    kind: "policy-source",
    id: options.id,
    signerId: options.signerId,
    publicKey: keyPair.publicKey,
    issuedAt: options.issuedAt ?? 0,
    expiresAt: options.expiresAt,
    description: options.description,
  };
  return {
    ...claims,
    signature: signPayload(options.privateKey ?? keyPair.privateKey, claims),
  };
}

export function verifyPolicySource(
  source: SignedPolicySource,
  options: { at?: number | string | Date; trust?: TrustOptions } = {},
): {
  ok: boolean;
  reason?: "invalid-source-signature" | "untrusted-source" | "expired-source";
} {
  if (!verifyPayload(source.publicKey, sourceClaimsWithoutSignature(source), source.signature)) {
    return { ok: false, reason: "invalid-source-signature" };
  }
  const trust = options.trust;
  const fingerprint = publicKeyFingerprint(source.publicKey);
  if (
    (trust?.requireTrustedSources || trust?.trustedSourceFingerprints !== undefined) &&
    !(trust?.trustedSourceFingerprints ?? []).includes(fingerprint)
  ) {
    return { ok: false, reason: "untrusted-source" };
  }
  if (options.at !== undefined && source.expiresAt !== undefined && toEpochMs(options.at) >= source.expiresAt) {
    return { ok: false, reason: "expired-source" };
  }
  if (options.at !== undefined && toEpochMs(options.at) < source.issuedAt) {
    return { ok: false, reason: "invalid-source-signature" };
  }
  return { ok: true };
}

function waiverClaimsWithoutSignature(waiver: SignedWaiver): WaiverClaims {
  return {
    kind: "waiver",
    id: waiver.id,
    issuer: waiver.issuer,
    target: waiver.target,
    scope: waiver.scope,
    issuedAt: waiver.issuedAt,
    expiresAt: waiver.expiresAt,
  };
}

export function signWaiver(claims: WaiverClaims, privateKey: string): SignedWaiver {
  return {
    ...claims,
    signature: signPayload(privateKey, claims),
  };
}

export function verifyWaiver(
  waiver: SignedWaiver,
  options: {
    at?: number | string | Date;
    trust?: TrustOptions;
    validity?: TimeInterval;
  } = {},
): { ok: boolean; reason?: "invalid-waiver-signature" | "expired" | "invalid-source-signature" | "untrusted-source" | "expired-source" } {
  if (!verifyPayload(waiver.issuer.publicKey, waiverClaimsWithoutSignature(waiver), waiver.signature)) {
    return { ok: false, reason: "invalid-waiver-signature" };
  }
  const source = verifyPolicySource(waiver.issuer, {
    at: options.at,
    trust: options.trust,
  });
  if (!source.ok) {
    return { ok: false, reason: source.reason };
  }
  const validity = options.validity ?? {
    start: waiver.issuedAt,
    end: waiver.expiresAt,
    startInclusive: true,
    endInclusive: false,
  };
  if (options.at !== undefined && !intervalContains(validity, options.at)) {
    return { ok: false, reason: "expired" };
  }
  return { ok: true };
}
