import { hashValue } from "./canonical.ts";
import {
  authorizationKey,
  normalizeAuthorization,
  recoverAuthorizationSigner,
} from "./authorization.ts";
import { ledgerError } from "./errors.ts";
import type {
  AuthorizationStateView,
  LedgerEvent,
  RequestRecord,
  RequestStatus,
  RecoverSigner,
} from "./types.ts";

export interface InternalRequestRecord {
  status: RequestStatus;
}

export interface InternalAuthorizationState {
  key: string;
  authorization: LedgerEvent["authorization"];
  signature: string;
  recoveredSigner: string;
  totalAmount: bigint;
  consumed: bigint;
  requests: Map<string, InternalRequestRecord>;
  revoked: boolean;
  invalidated: boolean;
}

export type InternalLedgerState = Map<string, InternalAuthorizationState>;

export function emptyState(): InternalLedgerState {
  return new Map();
}

export function cloneState(state: InternalLedgerState): InternalLedgerState {
  const next: InternalLedgerState = new Map();
  for (const [key, value] of state) {
    next.set(key, {
      ...value,
      authorization: { ...value.authorization },
      requests: new Map(
        Array.from(value.requests, ([requestId, record]) => [requestId, { ...record }]),
      ),
    });
  }
  return next;
}

export function hasRequest(state: InternalLedgerState, key: string, requestId: string): boolean {
  return state.get(key)?.requests.has(requestId) ?? false;
}

export function stateToView(value: InternalAuthorizationState): AuthorizationStateView {
  const remaining = value.totalAmount - value.consumed;
  const spendable = value.revoked || value.invalidated ? 0n : remaining;
  const requests = Array.from(value.requests, ([requestId, record]) => ({
    requestId,
    status: record.status,
  })).sort((left, right) => left.requestId.localeCompare(right.requestId));
  return Object.freeze({
    key: value.key,
    authorization: { ...value.authorization },
    signature: value.signature,
    recoveredSigner: value.recoveredSigner,
    totalAmount: value.totalAmount,
    consumed: value.consumed,
    remaining,
    available: spendable,
    spendable,
    revoked: value.revoked,
    invalidated: value.invalidated,
    requests,
  });
}

function internalStateFromEvent(event: LedgerEvent): InternalAuthorizationState {
  return {
    key: authorizationKey(event.authorization, event.signature),
    authorization: event.authorization,
    signature: event.signature,
    recoveredSigner: event.recoveredSigner,
    totalAmount: event.authorization.totalAmount,
    consumed: 0n,
    requests: new Map(),
    revoked: false,
    invalidated: false,
  };
}

function assertEventAuthorization(
  state: InternalAuthorizationState,
  event: LedgerEvent,
): void {
  if (event.authorization.chainId !== state.authorization.chainId) {
    throw ledgerError("CHAIN_MISMATCH", "event authorization belongs to another chain");
  }
  if (event.authorization.totalAmount !== state.totalAmount) {
    throw ledgerError(
      "INVALID_AUTHORIZATION",
      "the same signed authorization identity changed its total allowance",
    );
  }
}

function markRequest(
  state: InternalAuthorizationState,
  requestId: string,
  status: RequestStatus,
): void {
  const existing = state.requests.get(requestId);
  if (existing) {
    existing.status = status === "active" ? existing.status : status;
    return;
  }
  state.requests.set(requestId, { status });
}

export function applyEvent(state: InternalLedgerState, event: LedgerEvent): void {
  if (
    event.blockNumber < event.authorization.validFrom ||
    event.blockNumber > event.authorization.validTo
  ) {
    throw ledgerError(
      "OUT_OF_VALIDITY",
      `block ${event.blockNumber} is outside authorization validity ` +
        `[${event.authorization.validFrom}, ${event.authorization.validTo}]`,
    );
  }

  const key = authorizationKey(event.authorization, event.signature);
  let authorizationState = state.get(key);
  if (!authorizationState) {
    authorizationState = internalStateFromEvent(event);
    state.set(key, authorizationState);
  } else {
    assertEventAuthorization(authorizationState, event);
    if (authorizationState.signature !== event.signature) {
      throw ledgerError("INVALID_SIGNATURE", "authorization identity signature mismatch");
    }
  }

  if (event.kind === "spend") {
    if (authorizationState.revoked) {
      throw ledgerError("AUTHORIZATION_REVOKED", "authorization has been revoked");
    }
    if (authorizationState.invalidated) {
      throw ledgerError("AUTHORIZATION_INVALIDATED", "authorization has been invalidated");
    }
    if (event.amount === null || event.amount <= 0n) {
      throw ledgerError("INVALID_AMOUNT", "spend amount must be greater than zero");
    }
    if (!event.requestId) {
      throw ledgerError("INVALID_EVENT", "spend event requires a business request id");
    }
    if (authorizationState.requests.has(event.requestId)) {
      throw ledgerError(
        "DUPLICATE_REQUEST",
        `business request ${event.requestId} was already used by this signed authorization`,
      );
    }

    const nextConsumed = authorizationState.consumed + event.amount;
    if (nextConsumed > authorizationState.totalAmount) {
      throw ledgerError(
        "INSUFFICIENT_ALLOWANCE",
        `spend would consume ${nextConsumed.toString()} of ` +
          `${authorizationState.totalAmount.toString()}`,
      );
    }
    authorizationState.consumed = nextConsumed;
    markRequest(
      authorizationState,
      event.requestId,
      event.invalidated ? "invalidated" : "active",
    );
  } else if (event.kind === "invalidate") {
    if (event.requestId) {
      markRequest(authorizationState, event.requestId, "invalidated");
    } else {
      authorizationState.invalidated = true;
    }
  } else {
    if (event.requestId) {
      markRequest(authorizationState, event.requestId, "revoked");
    } else {
      authorizationState.revoked = true;
    }
  }

  if (event.revokeAfter) {
    authorizationState.revoked = true;
  }
}

export function replayEventPath(
  events: ReadonlyArray<LedgerEvent>,
  recoverSigner?: RecoverSigner,
): InternalLedgerState {
  const state = emptyState();
  for (const event of events) {
    if (recoverSigner) {
      const recovered = recoverAuthorizationSigner(
        event.authorization,
        event.signature,
        recoverSigner,
      );
      if (recovered !== event.recoveredSigner) {
        throw ledgerError(
          "INVALID_SIGNATURE",
          "recorded signer recovery is not reproducible for this event",
        );
      }
    }
    applyEvent(state, event);
  }
  return state;
}

export function requestSetHash(
  records:
    | Iterable<RequestRecord | InternalRequestRecord | string>
    | ReadonlyMap<string, InternalRequestRecord>,
): string {
  const entries = (
    records instanceof Map
      ? Array.from(records, ([requestId, record]) => ({
          requestId,
          status: record.status,
        }))
      : Array.from(records, (record) => {
          if (typeof record === "string") {
            return { requestId: record, status: "active" as const };
          }
          return { requestId: record.requestId, status: record.status };
        })
  ).sort((left, right) => left.requestId.localeCompare(right.requestId));
  return hashValue("request-set-v1", entries);
}

export function authorizationLeafHash(value: {
  key: string;
  authorization: { totalAmount: bigint };
  consumed: bigint;
  requests:
    | Iterable<RequestRecord | InternalRequestRecord | string>
    | ReadonlyMap<string, InternalRequestRecord>;
  revoked?: boolean;
  invalidated?: boolean;
}): string {
  return hashValue(
    "authorization-commitment-v1",
    value.key,
    value.authorization.totalAmount,
    value.consumed,
    requestSetHash(value.requests),
    value.revoked === true,
    value.invalidated === true,
  );
}

export function stateLeafHash(value: InternalAuthorizationState): string {
  return authorizationLeafHash(value);
}

export function stateFromView(value: AuthorizationStateView): InternalAuthorizationState {
  const authorization = normalizeAuthorization(value.authorization);
  const requests = new Map<string, InternalRequestRecord>();
  for (const request of value.requests) {
    requests.set(request.requestId, { status: request.status });
  }
  return {
    key: value.key,
    authorization,
    signature: value.signature,
    recoveredSigner: value.recoveredSigner,
    totalAmount: authorization.totalAmount,
    consumed: value.consumed,
    requests,
    revoked: value.revoked,
    invalidated: value.invalidated,
  };
}
