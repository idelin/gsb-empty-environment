import { createHmac, createPublicKey, sign as cryptoSign, verify as cryptoVerify } from "node:crypto";

import { canonicalJson, sha256Hex } from "./canonical.ts";
import {
  normalizeRangeSet,
  rangeSignature,
  rangeToJSON,
  type RangeInput,
  type RangeSet,
} from "./interval.ts";
import type { BridgeDeclarationInput } from "./types.ts";

export interface BridgeScope {
  readonly bridgeId: string;
  readonly tenant: string | null;
  readonly contentGeneration: number | null;
  readonly keyGeneration: number | null;
  readonly purpose: string | null;
  readonly interval: RangeSet;
}

export interface BridgeSignatureOptions {
  readonly secret?: string;
  readonly publicKey?: unknown;
  readonly assumeSigned?: boolean;
  readonly verify?: (scope: BridgeScope, payload: string, signature: string) => boolean;
}

function firstString(...values: unknown[]): string | undefined {
  for (const value of values) {
    if (typeof value === "string" && value.length > 0) return value;
  }
  return undefined;
}

function firstDefined(...values: unknown[]): unknown {
  for (const value of values) {
    if (value !== undefined && value !== null) return value;
  }
  return undefined;
}

export function parseGeneration(value: unknown, field: string): number {
  if (typeof value === "number") {
    if (!Number.isSafeInteger(value) || value < 0) {
      throw new RangeError(`${field} must be a non-negative safe integer`);
    }
    return value;
  }
  if (typeof value === "string" && /^\d+$/.test(value.trim())) {
    const number = Number(value.trim());
    if (!Number.isSafeInteger(number)) throw new RangeError(`${field} is too large`);
    return number;
  }
  throw new TypeError(`${field} must be a non-negative integer`);
}

export function parseOptionalGeneration(value: unknown, field: string): number | null {
  if (value === undefined || value === null || value === "") return null;
  return parseGeneration(value, field);
}

export function parseThreshold(value: unknown, fallback = 1): number {
  if (value === undefined || value === null) return fallback;
  const number = typeof value === "number" ? value : Number(value);
  if (!Number.isSafeInteger(number) || number < 1) {
    throw new RangeError("threshold must be a positive integer");
  }
  return number;
}

export function extractSignature(input: BridgeDeclarationInput): string | null {
  const signature = input.signature;
  if (typeof signature === "string" && signature.length > 0) return signature;
  if (signature !== null && typeof signature === "object" && typeof signature.value === "string") {
    return signature.value.length > 0 ? signature.value : null;
  }
  return null;
}

export function bridgeScopeOf(input: BridgeDeclarationInput): BridgeScope {
  const tenant = firstString(
    input.tenant,
    input.tenantId,
    (input as Record<string, unknown>).tenantName,
    (input as Record<string, unknown>).tenantKey,
  );
  const contentGeneration = parseOptionalGeneration(
    firstDefined(
      input.contentGeneration,
      input.contentGen,
      input.roundGeneration,
      input.round,
      (input as Record<string, unknown>).contentRound,
      (input as Record<string, unknown>).contentEpoch,
      (input as Record<string, unknown>).epoch,
    ),
    "bridge contentGeneration",
  );
  const keyGeneration = parseOptionalGeneration(
    firstDefined(
      input.keyGeneration,
      input.keyGen,
      input.generation,
      (input as Record<string, unknown>).oldKeyGeneration,
      (input as Record<string, unknown>).keyEpoch,
    ),
    "bridge keyGeneration",
  );
  const purpose = firstString(
    input.purpose,
    input.usage,
    (input as Record<string, unknown>).use,
    (input as Record<string, unknown>).intent,
  );
  const interval = normalizeRangeSet(
    (input.interval ??
      input.deviceInterval ??
      input.range ??
      (input as Record<string, unknown>).deviceRange ??
      (input as Record<string, unknown>).idRange ??
      (input as Record<string, unknown>).deviceIdRange ??
      (input as Record<string, unknown>).allowedInterval) as
      | RangeInput
      | undefined,
  );
  const scopeWithoutId = {
    tenant: tenant ?? null,
    contentGeneration,
    keyGeneration,
    purpose: purpose ?? null,
    interval: rangeToJSON(interval),
  };
  const bridgeId =
    firstString(input.bridgeId, input.id, (input as Record<string, unknown>).keyId) ??
    `bridge:${sha256Hex(canonicalJson(scopeWithoutId)).slice(0, 24)}`;
  return {
    bridgeId,
    tenant: tenant ?? null,
    contentGeneration,
    keyGeneration,
    purpose: purpose ?? null,
    interval,
  };
}

export function bridgeSigningPayload(scope: BridgeScope | BridgeDeclarationInput): string {
  const normalized =
    "bridgeId" in scope && "interval" in scope && Array.isArray((scope as BridgeScope).interval)
      ? (scope as BridgeScope)
      : bridgeScopeOf(scope as BridgeDeclarationInput);
  return canonicalJson({
    kind: "gsb.bridge/v1",
    bridgeId: normalized.bridgeId,
    tenant: normalized.tenant,
    contentGeneration: normalized.contentGeneration,
    keyGeneration: normalized.keyGeneration,
    purpose: normalized.purpose,
    interval: rangeToJSON(normalized.interval),
  });
}

export function signBridge(
  input: BridgeDeclarationInput,
  secret: string,
  algorithm: "hmac-sha256" = "hmac-sha256",
): BridgeDeclarationInput & { bridgeId: string; signature: string } {
  if (algorithm !== "hmac-sha256") throw new RangeError(`unsupported bridge signature algorithm ${algorithm}`);
  if (secret.length === 0) throw new RangeError("bridge signing secret must not be empty");
  const scope = bridgeScopeOf(input);
  const signature = createHmac("sha256", secret).update(bridgeSigningPayload(scope)).digest("hex");
  return { ...input, bridgeId: scope.bridgeId, signature };
}

export function signBridgeEd25519(
  input: BridgeDeclarationInput,
  privateKey: unknown,
): BridgeDeclarationInput & { bridgeId: string; signature: string } {
  const scope = bridgeScopeOf(input);
  const signature = cryptoSign(null, Buffer.from(bridgeSigningPayload(scope)), privateKey as never).toString("base64");
  return { ...input, bridgeId: scope.bridgeId, signature };
}

function signatureBytes(signature: string): Buffer {
  if (/^[0-9a-f]+$/i.test(signature) && signature.length % 2 === 0) {
    return Buffer.from(signature, "hex");
  }
  return Buffer.from(signature, "base64");
}

export function verifyBridgeSignature(
  input: BridgeDeclarationInput | BridgeScope,
  options: BridgeSignatureOptions,
): boolean {
  const scope =
    "bridgeId" in input && "interval" in input && Array.isArray((input as BridgeScope).interval)
      ? (input as BridgeScope)
      : bridgeScopeOf(input as BridgeDeclarationInput);
  const payload = bridgeSigningPayload(scope);

  if (options.verify !== undefined) {
    const signature =
      "signature" in input ? extractSignature(input as BridgeDeclarationInput) : null;
    return options.verify(scope, payload, signature ?? "");
  }

  if (options.secret !== undefined) {
    const signature = "signature" in input ? extractSignature(input as BridgeDeclarationInput) : null;
    if (signature === null) return false;
    const expected = createHmac("sha256", options.secret).update(payload).digest("hex");
    return signature.length === expected.length && timingSafeEqualHex(signature.toLowerCase(), expected);
  }

  if (options.publicKey !== undefined) {
    const signature = "signature" in input ? extractSignature(input as BridgeDeclarationInput) : null;
    if (signature === null) return false;
    try {
      const key =
        typeof options.publicKey === "string" ? createPublicKey(options.publicKey) : options.publicKey;
      return cryptoVerify(
        null,
        Buffer.from(payload),
        key as never,
        signatureBytes(signature),
      );
    } catch {
      return false;
    }
  }

  if (options.assumeSigned === true) {
    return "signature" in input
      ? extractSignature(input as BridgeDeclarationInput) !== null
      : false;
  }

  return false;
}

function timingSafeEqualHex(left: string, right: string): boolean {
  if (left.length !== right.length) return false;
  let difference = 0;
  for (let index = 0; index < left.length; index += 1) {
    difference |= left.charCodeAt(index) ^ right.charCodeAt(index);
  }
  return difference === 0;
}

export function bridgeScopeSignature(scope: BridgeScope): string {
  return rangeSignature(scope.interval);
}
