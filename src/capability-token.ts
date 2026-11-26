import { parseMatchPattern } from "./match-pattern.ts";
import { normalizeOrigin, targetAllowedByToken } from "./targets.ts";
import type {
  CapabilityTokenClaims,
  IssueTokenInput,
  MessageRoute,
  PrivilegedOperation,
} from "./types.ts";

const TOKEN_PREFIX = "mv3cap";
const TOKEN_VERSION = 1;
const DEFAULT_TTL_MS = 5 * 60 * 1000;
const MAX_TTL_MS = 60 * 60 * 1000;
const MAX_TOKEN_LENGTH = 8 * 1024;

export type TokenVerificationFailure =
  | "malformed"
  | "oversized"
  | "unknown-version"
  | "bad-signature"
  | "expired"
  | "not-yet-valid"
  | "route-mismatch"
  | "action-mismatch"
  | "origin-mismatch"
  | "tab-mismatch"
  | "permissions-mismatch"
  | "target-mismatch";

export interface TokenExpectation {
  routeId: string;
  actionId?: string;
  origin: string;
  tabId: number;
  tabPermissions?: string[];
  operation?: PrivilegedOperation;
}

export type TokenVerificationResult =
  | { valid: true; claims: CapabilityTokenClaims }
  | { valid: false; reason: TokenVerificationFailure; detail: string };

export interface CapabilityAuthorityOptions {
  secret: string | Uint8Array;
  clock?: () => number;
  maxTtlMs?: number;
  maxTokenAgeMs?: number;
}

export class CapabilityTokenError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "CapabilityTokenError";
  }
}

export class CapabilityAuthority {
  private readonly clock: () => number;
  private readonly maxTtlMs: number;
  private readonly maxTokenAgeMs: number;
  private readonly keyPromise: Promise<CryptoKey>;

  constructor(options: CapabilityAuthorityOptions) {
    const secretBytes =
      typeof options.secret === "string" ? new TextEncoder().encode(options.secret) : options.secret;
    if (secretBytes.byteLength < 16) {
      throw new CapabilityTokenError("HMAC secret must be at least 16 bytes");
    }
    this.clock = options.clock ?? (() => Date.now());
    this.maxTtlMs = Math.min(options.maxTtlMs ?? MAX_TTL_MS, MAX_TTL_MS);
    this.maxTokenAgeMs = options.maxTokenAgeMs ?? defaultMaxTokenAge(this.maxTtlMs);
    this.keyPromise = crypto.subtle.importKey(
      "raw",
      secretBytes,
      { name: "HMAC", hash: "SHA-256" },
      false,
      ["sign", "verify"],
    );
  }

  async issueToken(input: IssueTokenInput, route?: MessageRoute): Promise<string> {
    const origin = normalizeOrigin(input.origin);
    if (!origin) {
      throw new CapabilityTokenError(`capability token origin must be an absolute web origin, got ${input.origin}`);
    }
    if (!Number.isInteger(input.tabId) || input.tabId < 0) {
      throw new CapabilityTokenError(`tabId must be a non-negative integer, got ${input.tabId}`);
    }
    if (input.tabPermissions.length === 0) {
      throw new CapabilityTokenError("tabPermissions must not be empty");
    }
    for (const permission of input.tabPermissions) {
      if (permission === "activeTab") continue;
      try {
        parseMatchPattern(permission);
      } catch {
        throw new CapabilityTokenError(`tab permission is not a Chrome match pattern: ${permission}`);
      }
    }

    const ttlMs = Math.min(input.ttlMs ?? DEFAULT_TTL_MS, this.maxTtlMs);
    if (ttlMs <= 0) throw new CapabilityTokenError("token ttl must be positive");
    const now = this.clock();
    const claims: CapabilityTokenClaims = {
      v: TOKEN_VERSION,
      routeId: input.routeId,
      actionId: input.actionId ?? route?.actionId ?? route?.id ?? input.routeId,
      origin,
      tabId: input.tabId,
      permissions: canonicalPermissions(input.tabPermissions),
      target: input.target,
      issuedAt: now,
      expiresAt: now + ttlMs,
      nonce: input.nonce ?? randomNonce(),
    };

    const payload = base64UrlEncode(new TextEncoder().encode(JSON.stringify(claims)));
    const signed = `${TOKEN_PREFIX}.${payload}`;
    const signature = await crypto.subtle.sign("HMAC", await this.keyPromise, new TextEncoder().encode(signed));
    return `${signed}.${base64UrlEncode(new Uint8Array(signature))}`;
  }

  async verifyToken(token: string, expected: TokenExpectation): Promise<TokenVerificationResult> {
    if (typeof token !== "string" || token.length === 0) {
      return { valid: false, reason: "malformed", detail: "token is empty" };
    }
    if (token.length > MAX_TOKEN_LENGTH) {
      return { valid: false, reason: "oversized", detail: "token exceeds maximum length" };
    }
    const parts = token.split(".");
    if (parts.length !== 3 || parts[0] !== TOKEN_PREFIX) {
      return { valid: false, reason: "malformed", detail: "token structure is invalid" };
    }
    const [, payloadPart, signaturePart] = parts;

    let signature: Uint8Array;
    let payload: Uint8Array;
    try {
      signature = base64UrlDecode(signaturePart);
      payload = base64UrlDecode(payloadPart);
    } catch {
      return { valid: false, reason: "malformed", detail: "token encoding is invalid" };
    }

    const signed = new TextEncoder().encode(`${TOKEN_PREFIX}.${payloadPart}`);
    const validSignature = await crypto.subtle.verify(
      "HMAC",
      await this.keyPromise,
      signature,
      signed,
    );
    if (!validSignature) {
      return { valid: false, reason: "bad-signature", detail: "HMAC verification failed" };
    }

    let claimsValue: unknown;
    try {
      claimsValue = JSON.parse(new TextDecoder().decode(payload));
    } catch {
      return { valid: false, reason: "malformed", detail: "token payload is not JSON" };
    }
    const claims = claimsValue as Partial<CapabilityTokenClaims>;
    if (
      claims.v !== TOKEN_VERSION ||
      typeof claims.routeId !== "string" ||
      typeof claims.actionId !== "string" ||
      typeof claims.origin !== "string" ||
      typeof claims.tabId !== "number" ||
      !Array.isArray(claims.permissions) ||
      typeof claims.issuedAt !== "number" ||
      typeof claims.expiresAt !== "number" ||
      typeof claims.nonce !== "string"
    ) {
      return { valid: false, reason: "unknown-version", detail: "token claims are incomplete" };
    }
    const completeClaims = claims as CapabilityTokenClaims;

    const now = this.clock();
    if (completeClaims.expiresAt <= now) {
      return { valid: false, reason: "expired", detail: "token has expired" };
    }
    if (completeClaims.issuedAt > now + 60_000) {
      return { valid: false, reason: "not-yet-valid", detail: "token issuedAt is in the future" };
    }
    if (now - completeClaims.issuedAt > this.maxTokenAgeMs) {
      return { valid: false, reason: "expired", detail: "token is older than the maximum age" };
    }

    if (completeClaims.routeId !== expected.routeId) {
      return { valid: false, reason: "route-mismatch", detail: "token route does not match" };
    }
    const expectedAction = expected.actionId ?? expected.routeId;
    if (completeClaims.actionId !== expectedAction) {
      return { valid: false, reason: "action-mismatch", detail: "token action does not match" };
    }
    const expectedOrigin = normalizeOrigin(expected.origin);
    if (!expectedOrigin || completeClaims.origin !== expectedOrigin) {
      return { valid: false, reason: "origin-mismatch", detail: "token origin does not match" };
    }
    if (completeClaims.tabId !== expected.tabId) {
      return { valid: false, reason: "tab-mismatch", detail: "token tabId does not match" };
    }
    if (
      expected.tabPermissions &&
      !permissionSetsEqual(completeClaims.permissions, expected.tabPermissions)
    ) {
      return {
        valid: false,
        reason: "permissions-mismatch",
        detail: "token tab permission set does not match the live tab grant",
      };
    }
    if (expected.operation && !targetAllowedByToken(expected.operation, completeClaims.target)) {
      return { valid: false, reason: "target-mismatch", detail: "token target does not cover the operation target" };
    }

    return { valid: true, claims: completeClaims };
  }
}

function defaultMaxTokenAge(maxTtlMs: number): number {
  return Math.max(MAX_TTL_MS, maxTtlMs);
}

function canonicalPermissions(permissions: readonly string[]): string[] {
  return [...new Set(permissions)].sort();
}

function permissionSetsEqual(left: readonly string[], right: readonly string[]): boolean {
  const leftSet = canonicalPermissions(left);
  const rightSet = canonicalPermissions(right);
  return leftSet.length === rightSet.length && leftSet.every((value, index) => value === rightSet[index]);
}

function randomNonce(): string {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  return base64UrlEncode(bytes);
}

function base64UrlEncode(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function base64UrlDecode(value: string): Uint8Array {
  if (!/^[A-Za-z0-9_-]*$/.test(value)) throw new Error("invalid base64url");
  const padded = value.replace(/-/g, "+").replace(/_/g, "/");
  const binary = atob(padded + "=".repeat((4 - (padded.length % 4)) % 4));
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
  return bytes;
}
