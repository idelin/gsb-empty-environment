import { createHmac, randomBytes, timingSafeEqual } from "node:crypto";

import { normalizeOrigin, tryNormalizeOrigin } from "./origin.ts";

const TOKEN_VERSION = 1 as const;
const DEFAULT_TOKEN_TTL_MS = 5 * 60 * 1000;

export interface IssueRequest {
  readonly origin: string;
  readonly ttlMs?: number;
}

export interface PrivateStateToken {
  readonly version: typeof TOKEN_VERSION;
  readonly issuer: string;
  readonly origin: string;
  readonly nonce: string;
  readonly issuedAt: number;
  readonly expiresAt: number;
  readonly signature: string;
}

export type TokenFields = Omit<PrivateStateToken, "signature">;

export type RedemptionFailureReason =
  | "malformed-token"
  | "wrong-issuer"
  | "invalid-signature"
  | "expired"
  | "origin-mismatch"
  | "already-redeemed";

export type RedemptionResult =
  | {
      readonly ok: true;
      readonly issuer: string;
      readonly origin: string;
    }
  | {
      readonly ok: false;
      readonly reason: RedemptionFailureReason;
    };

export interface PrivateStateTokenServiceOptions {
  readonly issuer: string;
  readonly secret: string | Uint8Array;
  readonly tokenTtlMs?: number;
  readonly clock?: () => number;
}

function isSafeNonNegativeInteger(value: unknown): value is number {
  return Number.isSafeInteger(value) && (value as number) >= 0;
}

function parseToken(value: unknown): PrivateStateToken | null {
  if (typeof value !== "object" || value === null) {
    return null;
  }

  const candidate = value as Record<string, unknown>;

  if (
    candidate.version !== TOKEN_VERSION ||
    typeof candidate.issuer !== "string" ||
    candidate.issuer.length === 0 ||
    typeof candidate.origin !== "string" ||
    candidate.origin.length === 0 ||
    typeof candidate.nonce !== "string" ||
    candidate.nonce.length === 0 ||
    !isSafeNonNegativeInteger(candidate.issuedAt) ||
    !isSafeNonNegativeInteger(candidate.expiresAt) ||
    candidate.expiresAt <= candidate.issuedAt ||
    typeof candidate.signature !== "string" ||
    candidate.signature.length === 0
  ) {
    return null;
  }

  return {
    version: TOKEN_VERSION,
    issuer: candidate.issuer,
    origin: candidate.origin,
    nonce: candidate.nonce,
    issuedAt: candidate.issuedAt,
    expiresAt: candidate.expiresAt,
    signature: candidate.signature,
  };
}

export class PrivateStateTokenService {
  readonly issuer: string;
  private readonly secret: string | Uint8Array;
  private readonly tokenTtlMs: number;
  private readonly clock: () => number;
  private readonly redeemedNonces = new Map<string, number>();

  constructor(options: PrivateStateTokenServiceOptions) {
    if (
      typeof options?.issuer !== "string" ||
      options.issuer.trim().length === 0
    ) {
      throw new TypeError("issuer must be a non-empty string");
    }

    if (
      typeof options.secret === "string"
        ? options.secret.length === 0
        : !(options.secret instanceof Uint8Array)
    ) {
      throw new TypeError("secret must be a non-empty string or a Uint8Array");
    }

    const tokenTtlMs = options.tokenTtlMs ?? DEFAULT_TOKEN_TTL_MS;
    if (!Number.isSafeInteger(tokenTtlMs) || tokenTtlMs <= 0) {
      throw new TypeError("tokenTtlMs must be a positive safe integer");
    }

    const clock = options.clock ?? Date.now;
    if (typeof clock !== "function") {
      throw new TypeError("clock must be a function");
    }

    this.issuer = options.issuer;
    this.secret = options.secret;
    this.tokenTtlMs = tokenTtlMs;
    this.clock = clock;
  }

  issue(request: IssueRequest): PrivateStateToken {
    let origin: string;

    try {
      origin = normalizeOrigin(request?.origin);
    } catch (error) {
      throw new TypeError(
        `Cannot issue a token for an invalid origin: ${String(
          (error as Error).message,
        )}`,
      );
    }

    const ttlMs = request.ttlMs ?? this.tokenTtlMs;
    if (!Number.isSafeInteger(ttlMs) || ttlMs <= 0) {
      throw new TypeError("ttlMs must be a positive safe integer");
    }

    const issuedAt = Math.floor(this.clock());
    const expiresAt = issuedAt + ttlMs;
    if (!Number.isSafeInteger(expiresAt)) {
      throw new RangeError("Token expiry exceeds the safe integer range");
    }

    const fields: TokenFields = {
      version: TOKEN_VERSION,
      issuer: this.issuer,
      origin,
      nonce: randomBytes(32).toString("base64url"),
      issuedAt,
      expiresAt,
    };

    return {
      ...fields,
      signature: this.sign(fields),
    };
  }

  redeem(token: unknown, redemptionOrigin: string): RedemptionResult {
    const parsed = parseToken(token);
    if (parsed === null) {
      return { ok: false, reason: "malformed-token" };
    }

    if (parsed.issuer !== this.issuer) {
      return { ok: false, reason: "wrong-issuer" };
    }

    if (!this.hasValidSignature(parsed)) {
      return { ok: false, reason: "invalid-signature" };
    }

    // A token produced by this service always stores a canonical origin.
    // Reject anything else even if it carries a valid signature.
    if (tryNormalizeOrigin(parsed.origin) !== parsed.origin) {
      return { ok: false, reason: "malformed-token" };
    }

    // This is the security boundary: compare the complete origin tuple.
    // Never use hostname suffixes, eTLD+1, or parent/subdomain matching.
    const normalizedRedemptionOrigin = tryNormalizeOrigin(redemptionOrigin);
    if (
      normalizedRedemptionOrigin === null ||
      normalizedRedemptionOrigin !== parsed.origin
    ) {
      // Do not consume the token on a failed cross-origin attempt.
      return { ok: false, reason: "origin-mismatch" };
    }

    const now = Math.floor(this.clock());
    if (now >= parsed.expiresAt) {
      return { ok: false, reason: "expired" };
    }

    if (!this.claimNonce(parsed.nonce, parsed.expiresAt)) {
      return { ok: false, reason: "already-redeemed" };
    }

    return {
      ok: true,
      issuer: parsed.issuer,
      origin: normalizedRedemptionOrigin,
    };
  }

  private sign(fields: TokenFields): string {
    const payload = JSON.stringify([
      "private-state-token",
      fields.version,
      fields.issuer,
      fields.origin,
      fields.nonce,
      fields.issuedAt,
      fields.expiresAt,
    ]);

    return createHmac("sha256", this.secret).update(payload).digest("base64url");
  }

  private hasValidSignature(token: PrivateStateToken): boolean {
    const expected = this.sign(token);
    const expectedBytes = Buffer.from(expected, "utf8");
    const actualBytes = Buffer.from(token.signature, "utf8");

    return (
      expectedBytes.length === actualBytes.length &&
      timingSafeEqual(expectedBytes, actualBytes)
    );
  }

  private claimNonce(nonce: string, expiresAt: number): boolean {
    const now = Math.floor(this.clock());

    for (const [redeemedNonce, tokenExpiry] of this.redeemedNonces) {
      if (tokenExpiry <= now) {
        this.redeemedNonces.delete(redeemedNonce);
      }
    }

    if (this.redeemedNonces.has(nonce)) {
      return false;
    }

    this.redeemedNonces.set(nonce, expiresAt);
    return true;
  }
}
