import {
  hashValue,
  normalizeAddress,
  normalizeId,
  toBigInt,
  toBlockHeight,
} from "./canonical.ts";
import { ledgerError } from "./errors.ts";
import type {
  Authorization,
  AuthorizationInput,
  EventInput,
  EventKind,
  EventKindInput,
  LedgerEvent,
  PersistedEvent,
  RecoverSigner,
  SignedAuthorizationInput,
} from "./types.ts";

export interface AuthorizationWithKey {
  readonly authorization: Authorization;
  readonly signature: string;
  readonly key: string;
  readonly recoveredSigner: string;
}

export interface EventBlockContext {
  readonly chainId: string;
  readonly blockHash: string;
  readonly parentHash: string;
  readonly blockNumber: number;
}

export function normalizeAuthorization(input: AuthorizationInput): Authorization {
  if (!input || typeof input !== "object") {
    throw ledgerError("INVALID_AUTHORIZATION", "authorization must be an object");
  }

  const totalAmount = toBigInt(input.totalAmount, "authorization.totalAmount");
  if (totalAmount <= 0n) {
    throw ledgerError("INVALID_AMOUNT", "authorization.totalAmount must be greater than zero");
  }

  const validFrom = toBlockHeight(input.validFrom, "authorization.validFrom");
  const validTo = toBlockHeight(input.validTo, "authorization.validTo");
  if (validFrom > validTo) {
    throw ledgerError(
      "INVALID_AUTHORIZATION",
      `authorization validity is inverted (${validFrom} > ${validTo})`,
    );
  }

  return Object.freeze({
    chainId: normalizeId(input.chainId, "authorization.chainId"),
    contract: normalizeAddress(input.contract, "authorization.contract"),
    issuer: normalizeAddress(input.issuer, "authorization.issuer"),
    beneficiary: normalizeAddress(input.beneficiary, "authorization.beneficiary"),
    totalAmount,
    validFrom,
    validTo,
    authorizationId: normalizeId(input.authorizationId, "authorization.authorizationId"),
  });
}

export function normalizeSignature(signature: string): string {
  if (typeof signature !== "string" || signature.trim() === "") {
    throw ledgerError("INVALID_SIGNATURE", "signature must be a non-empty string");
  }
  return signature.trim();
}

export function authorizationKey(authorization: Authorization, signature: string): string {
  return hashValue(
    "wallet-authorization-v1",
    authorization.chainId,
    authorization.contract,
    authorization.issuer,
    authorization.beneficiary,
    authorization.totalAmount,
    authorization.validFrom,
    authorization.validTo,
    authorization.authorizationId,
    normalizeSignature(signature),
  );
}

export const signedAuthorizationId = authorizationKey;
export const authorizationIdentity = authorizationKey;

function assertSynchronousRecovery(value: unknown): asserts value is string {
  if (typeof value !== "string") {
    if (value && typeof (value as { then?: unknown }).then === "function") {
      throw ledgerError(
        "INVALID_SIGNATURE",
        "signature recovery must be synchronous; the injected recovery returned a Promise",
      );
    }
    throw ledgerError("INVALID_SIGNATURE", "signature recovery must return an address string");
  }
}

export function recoverAuthorizationSigner(
  authorization: Authorization,
  signature: string,
  recoverSigner: RecoverSigner,
): string {
  let recovered: unknown;
  try {
    recovered = recoverSigner(Object.freeze({ ...authorization }), signature);
  } catch (error) {
    throw ledgerError(
      "INVALID_SIGNATURE",
      `signature recovery failed: ${error instanceof Error ? error.message : String(error)}`,
    );
  }
  assertSynchronousRecovery(recovered);
  const normalized = normalizeAddress(recovered, "recovered signer");
  if (normalized !== authorization.issuer) {
    throw ledgerError(
      "INVALID_SIGNATURE",
      `recovered signer ${normalized} does not match issuer ${authorization.issuer}`,
    );
  }
  return normalized;
}

export function signAuthorization(
  input: SignedAuthorizationInput,
  recoverSigner: RecoverSigner,
): AuthorizationWithKey {
  const authorization = normalizeAuthorization(input.authorization);
  const signature = normalizeSignature(input.signature);
  const recoveredSigner = recoverAuthorizationSigner(authorization, signature, recoverSigner);
  return Object.freeze({
    authorization,
    signature,
    key: authorizationKey(authorization, signature),
    recoveredSigner,
  });
}

function normalizeKind(input: EventInput): EventKind {
  const candidate = (input.kind ?? input.type) as EventKindInput | undefined;
  if (candidate === undefined) {
    if (input.amount !== undefined) return "spend";
    if (input.revoke === true || input.revoked === true || input.revocation === true) return "revoke";
    if (input.invalidated === true || input.invalidate === true) return "invalidate";
    throw ledgerError("INVALID_EVENT", "event kind is required when no amount or flag implies it");
  }

  switch (candidate) {
    case "spend":
    case "consume":
    case "consumption":
    case "charge":
      return "spend";
    case "invalidate":
    case "invalidation":
    case "fail":
      return "invalidate";
    case "revoke":
    case "revocation":
    case "cancel":
      return "revoke";
    default:
      throw ledgerError("INVALID_EVENT", `unsupported event kind ${String(candidate)}`);
  }
}

function extractSignedAuthorization(input: EventInput): SignedAuthorizationInput {
  if (input.signedAuthorization) {
    const nested = input.signedAuthorization;
    if (input.authorization && input.authorization !== nested.authorization) {
      throw ledgerError(
        "INVALID_EVENT",
        "event contains both signedAuthorization and a different authorization object",
      );
    }
    return {
      authorization: nested.authorization,
      signature: nested.signature ?? input.signature ?? "",
    };
  }

  if (!input.authorization) {
    throw ledgerError("INVALID_EVENT", "event must carry an authorization");
  }
  return {
    authorization: input.authorization,
    signature: input.signature ?? "",
  };
}

function normalizeSequence(raw: unknown, fallback: number, label: string): number {
  if (raw === undefined) return fallback;
  const value = typeof raw === "bigint" ? raw : (raw as string | number);
  const integer = toBlockHeight(value, label);
  return integer;
}

export function normalizeEvent(
  input: EventInput,
  block: EventBlockContext,
  recoverSigner: RecoverSigner,
  fallbackSequence: number,
): LedgerEvent {
  if (!input || typeof input !== "object") {
    throw ledgerError("INVALID_EVENT", "event must be an object");
  }

  const signed = extractSignedAuthorization(input);
  const authorization = normalizeAuthorization(signed.authorization);
  if (authorization.chainId !== block.chainId) {
    throw ledgerError(
      "CHAIN_MISMATCH",
      `event authorization chain ${authorization.chainId} does not match block chain ${block.chainId}`,
    );
  }

  const signature = normalizeSignature(signed.signature);
  const recoveredSigner = recoverAuthorizationSigner(authorization, signature, recoverSigner);
  const kind = normalizeKind(input);
  const requestIdRaw = input.requestId ?? input.businessRequestId ?? input.requestKey;
  const requestId =
    requestIdRaw === undefined ? null : normalizeId(requestIdRaw, "event.requestId");

  let amount: bigint | null = null;
  if (kind === "spend") {
    if (input.amount === undefined) {
      throw ledgerError("INVALID_AMOUNT", "spend event requires an amount");
    }
    amount = toBigInt(input.amount, "event.amount");
    if (amount <= 0n) {
      throw ledgerError("INVALID_AMOUNT", "spend amount must be greater than zero");
    }
    if (!requestId) {
      throw ledgerError("INVALID_EVENT", "spend event requires a business request id");
    }
  }

  if (input.blockHash !== undefined) {
    const blockHash = normalizeId(input.blockHash, "event.blockHash");
    if (blockHash !== block.blockHash) {
      throw ledgerError("INVALID_BLOCK", "event blockHash does not match its block");
    }
  }
  const eventParentHash = input.parentHash ?? input.parentBlockHash;
  if (eventParentHash !== undefined) {
    const parentHash = normalizeId(eventParentHash, "event.parentHash");
    if (parentHash !== block.parentHash) {
      throw ledgerError("INVALID_BLOCK", "event parentHash does not match its block");
    }
  }
  if (input.blockNumber !== undefined) {
    const blockNumber = toBlockHeight(input.blockNumber, "event.blockNumber");
    if (blockNumber !== block.blockNumber) {
      throw ledgerError("INVALID_BLOCK", "event blockNumber does not match its block");
    }
  }

  const rawSequence = input.sequence ?? input.index ?? input.logIndex ??
    input.eventIndex ?? input.txIndex;
  const sequence = normalizeSequence(rawSequence, fallbackSequence, "event.sequence");
  const revokeAfter =
    kind !== "revoke" &&
    (input.revoke === true || input.revoked === true || input.revocation === true);
  const invalidated = input.invalidated === true || input.invalidate === true;

  return Object.freeze({
    kind,
    authorization,
    signature,
    recoveredSigner,
    amount,
    requestId,
    revokeAfter,
    invalidated,
    blockHash: block.blockHash,
    parentHash: block.parentHash,
    blockNumber: block.blockNumber,
    sequence,
  });
}

export function sortEventsBySequence(
  events: ReadonlyArray<LedgerEvent>,
  strictSequence = false,
): ReadonlyArray<LedgerEvent> {
  const sorted = [...events].sort((left, right) => left.sequence - right.sequence);
  for (let index = 0; index < sorted.length; index += 1) {
    if (index > 0 && sorted[index - 1].sequence === sorted[index].sequence) {
      throw ledgerError(
        "INVALID_EVENT",
        `duplicate event sequence ${sorted[index].sequence} in block ${sorted[index].blockHash}`,
      );
    }
    if (strictSequence && sorted[index].sequence !== index) {
      throw ledgerError(
        "INVALID_EVENT",
        `event sequence must be contiguous from zero; expected ${index}, got ${sorted[index].sequence}`,
      );
    }
  }
  return Object.freeze(sorted);
}

export function persistedEvent(event: LedgerEvent): PersistedEvent {
  return Object.freeze({
    kind: event.kind,
    authorization: {
      chainId: event.authorization.chainId,
      contract: event.authorization.contract,
      issuer: event.authorization.issuer,
      beneficiary: event.authorization.beneficiary,
      totalAmount: event.authorization.totalAmount.toString(),
      validFrom: event.authorization.validFrom,
      validTo: event.authorization.validTo,
      authorizationId: event.authorization.authorizationId,
    },
    signature: event.signature,
    amount: event.amount === null ? null : event.amount.toString(),
    requestId: event.requestId,
    revokeAfter: event.revokeAfter,
    invalidated: event.invalidated,
    blockHash: event.blockHash,
    parentHash: event.parentHash,
    blockNumber: event.blockNumber,
    sequence: event.sequence,
  });
}

export function eventFromPersisted(event: PersistedEvent, recoveredSigner: string): LedgerEvent {
  const authorization = normalizeAuthorization({
    ...event.authorization,
    totalAmount: event.authorization.totalAmount,
  });
  return Object.freeze({
    kind: event.kind,
    authorization,
    signature: event.signature,
    recoveredSigner,
    amount: event.amount === null ? null : toBigInt(event.amount, "event.amount"),
    requestId: event.requestId,
    revokeAfter: event.revokeAfter,
    invalidated: event.invalidated,
    blockHash: event.blockHash,
    parentHash: event.parentHash,
    blockNumber: event.blockNumber,
    sequence: event.sequence,
  });
}

export function cloneAuthorization(authorization: Authorization): Authorization {
  return { ...authorization };
}
