import { normalizeId, toBlockHeight } from "./canonical.ts";
import {
  normalizeAuthorization,
  normalizeEvent,
  sortEventsBySequence,
  type EventBlockContext,
} from "./authorization.ts";
import { ledgerError } from "./errors.ts";
import type {
  BlockInput,
  LedgerBlock,
  LedgerEvent,
  RecoverSigner,
} from "./types.ts";

export interface NormalizedBlockInput {
  readonly chainId: string;
  readonly hash: string;
  readonly parentHash: string;
  readonly number: number;
  readonly events: ReadonlyArray<LedgerEvent>;
}

export function inferBlockChainId(
  input: BlockInput,
  defaultChainId?: string,
): string {
  if (input.chainId !== undefined) {
    return normalizeId(input.chainId, "block.chainId");
  }
  const first = input.events?.[0];
  if (first) {
    const authorization = first.signedAuthorization?.authorization ?? first.authorization;
    if (authorization) {
      return normalizeAuthorization(authorization).chainId;
    }
  }
  if (defaultChainId !== undefined) {
    return normalizeId(defaultChainId, "defaultChainId");
  }
  throw ledgerError("INVALID_BLOCK", "block.chainId is required");
}

export function normalizeBlockInput(
  input: BlockInput,
  recoverSigner: RecoverSigner,
  options: { defaultChainId?: string; strictSequence?: boolean } = {},
): NormalizedBlockInput {
  if (!input || typeof input !== "object") {
    throw ledgerError("INVALID_BLOCK", "block must be an object");
  }
  const hash = normalizeId(input.hash ?? input.blockHash ?? "", "block.hash");
  const parentHash = normalizeId(
    input.parentHash ?? input.parentBlockHash ?? "",
    "block.parentHash",
  );
  const chainId = inferBlockChainId(input, options.defaultChainId);
  const rawNumber = input.number ?? input.blockNumber ?? input.height ?? 0;
  const number = toBlockHeight(rawNumber, "block.number");
  const context: EventBlockContext = {
    chainId,
    blockHash: hash,
    parentHash,
    blockNumber: number,
  };
  const events = (input.events ?? []).map((event, index) =>
    normalizeEvent(event, context, recoverSigner, index),
  );
  const sorted = sortEventsBySequence(events, options.strictSequence === true);
  return Object.freeze({
    chainId,
    hash,
    parentHash,
    number,
    events: sorted,
  });
}

export function freezeBlock(block: NormalizedBlockInput): LedgerBlock {
  return Object.freeze({
    chainId: block.chainId,
    hash: block.hash,
    parentHash: block.parentHash,
    number: block.number,
    events: Object.freeze([...block.events]),
  });
}

export function sameBlock(left: LedgerBlock, right: LedgerBlock): boolean {
  if (
    left.chainId !== right.chainId ||
    left.hash !== right.hash ||
    left.parentHash !== right.parentHash ||
    left.number !== right.number ||
    left.events.length !== right.events.length
  ) {
    return false;
  }
  for (let index = 0; index < left.events.length; index += 1) {
    const a = left.events[index];
    const b = right.events[index];
    if (
      a.kind !== b.kind ||
      a.signature !== b.signature ||
      a.amount !== b.amount ||
      a.requestId !== b.requestId ||
      a.revokeAfter !== b.revokeAfter ||
      a.invalidated !== b.invalidated ||
      a.sequence !== b.sequence ||
      a.blockHash !== b.blockHash ||
      a.parentHash !== b.parentHash ||
      a.blockNumber !== b.blockNumber ||
      a.authorization.chainId !== b.authorization.chainId ||
      a.authorization.contract !== b.authorization.contract ||
      a.authorization.issuer !== b.authorization.issuer ||
      a.authorization.beneficiary !== b.authorization.beneficiary ||
      a.authorization.totalAmount !== b.authorization.totalAmount ||
      a.authorization.validFrom !== b.authorization.validFrom ||
      a.authorization.validTo !== b.authorization.validTo ||
      a.authorization.authorizationId !== b.authorization.authorizationId
    ) {
      return false;
    }
  }
  return true;
}
