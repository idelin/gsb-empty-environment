import { normalizeId } from "./canonical.ts";
import { normalizeBlockInput } from "./block.ts";
import { ledgerError } from "./errors.ts";
import {
  applyEvent,
  emptyState,
  stateToView,
} from "./reducer.ts";
import type {
  AuthorizationStateView,
  EventInput,
  LedgerBlock,
  LedgerEvent,
  RecoverSigner,
  ReplayBlockInput,
  ReplayReport,
} from "./types.ts";

export interface ReplayOptions {
  chainId?: string;
  headHash?: string;
}

function toEventInput(event: EventInput | LedgerEvent): EventInput {
  if (!("recoveredSigner" in event)) return event;
  return {
    kind: event.kind,
    authorization: event.authorization,
    signature: event.signature,
    amount: event.amount ?? undefined,
    requestId: event.requestId ?? undefined,
    revoke: event.revokeAfter,
    invalidated: event.invalidated,
    blockHash: event.blockHash,
    parentHash: event.parentHash,
    blockNumber: event.blockNumber,
    sequence: event.sequence,
  };
}

/**
 * Independent reference replay. It deliberately does not consult an
 * AuthorizationLedger instance; callers can compare its report with the relay's
 * branch view after arbitrary reorganizations.
 */
export function replayEvents(
  blocks: ReadonlyArray<ReplayBlockInput>,
  recoverSigner: RecoverSigner,
  options: ReplayOptions = {},
): ReplayReport {
  if (!Array.isArray(blocks) || blocks.length === 0) {
    throw ledgerError("INVALID_BLOCK", "replay requires at least one block");
  }

  const normalized = blocks.map((block) =>
    normalizeBlockInput(
      {
        chainId: block.chainId,
        hash: block.hash,
        blockHash: block.blockHash,
        parentHash: block.parentHash,
        parentBlockHash: block.parentBlockHash,
        number: block.number,
        blockNumber: block.blockNumber,
        height: block.height,
        events: block.events.map(toEventInput),
      },
      recoverSigner,
      {
        defaultChainId: options.chainId,
        strictSequence: false,
      },
    ),
  );
  const byHash = new Map<string, (typeof normalized)[number]>();
  for (const block of normalized) {
    if (byHash.has(block.hash)) {
      throw ledgerError("BLOCK_CONFLICT", `replay input repeats block ${block.hash}`);
    }
    byHash.set(block.hash, block);
  }

  let chainId = options.chainId;
  for (const block of normalized) {
    if (chainId === undefined) chainId = block.chainId;
    else if (block.chainId !== chainId) {
      throw ledgerError("CHAIN_MISMATCH", "replay input contains multiple chains");
    }
  }
  if (chainId === undefined) throw ledgerError("CHAIN_MISMATCH", "replay chain is missing");

  const children = new Map<string, string[]>();
  const roots: string[] = [];
  for (const block of normalized) {
    const parent = byHash.get(block.parentHash);
    if (parent) {
      if (parent.number + 1 !== block.number) {
        throw ledgerError("INVALID_BLOCK", `block ${block.hash} height does not follow its parent`);
      }
      const list = children.get(parent.hash) ?? [];
      list.push(block.hash);
      children.set(parent.hash, list);
    } else {
      roots.push(block.hash);
    }
  }
  if (roots.length !== 1) {
    throw ledgerError(
      "INVALID_BLOCK",
      `replay input must have one root, found ${roots.length}`,
    );
  }

  const tips = normalized.filter((block) => !children.has(block.hash)).map((block) => block.hash);
  let headHash = options.headHash
    ? normalizeId(options.headHash, "replay.headHash")
    : undefined;
  if (headHash === undefined) {
    if (tips.length !== 1) {
      throw ledgerError(
        "INVALID_BLOCK",
        `replay has ${tips.length} tips; pass headHash to select a branch`,
      );
    }
    headHash = tips[0];
  }
  if (!byHash.has(headHash)) {
    throw ledgerError("UNKNOWN_BLOCK", `replay head ${headHash} is not in the input`);
  }

  const path: (typeof normalized)[number][] = [];
  const seen = new Set<string>();
  let cursor: string | undefined = headHash;
  while (cursor !== undefined) {
    if (seen.has(cursor)) {
      throw ledgerError("INVALID_BLOCK", `replay parent cycle at ${cursor}`);
    }
    seen.add(cursor);
    const block = byHash.get(cursor);
    if (!block) throw ledgerError("UNKNOWN_BLOCK", `replay parent ${cursor} is missing`);
    path.push(block);
    cursor = byHash.get(block.parentHash)?.hash;
  }
  path.reverse();

  const state = emptyState();
  const ledgerBlocks: LedgerBlock[] = [];
  for (const block of path) {
    for (const event of block.events) applyEvent(state, event);
    ledgerBlocks.push(
      Object.freeze({
        chainId: block.chainId,
        hash: block.hash,
        parentHash: block.parentHash,
        number: block.number,
        events: block.events,
      }),
    );
  }

  const authorizations: Record<string, AuthorizationStateView> = {};
  const remainingByKey: Record<string, bigint> = {};
  const usedRequestsByKey: Record<string, ReadonlyArray<string>> = {};
  for (const key of Array.from(state.keys()).sort()) {
    const value = state.get(key)!;
    authorizations[key] = stateToView(value);
    remainingByKey[key] = value.totalAmount - value.consumed;
    usedRequestsByKey[key] = Object.freeze(
      Array.from(value.requests.keys()).sort((left, right) => left.localeCompare(right)),
    );
  }

  const head = path[path.length - 1];
  return Object.freeze({
    chainId,
    headHash: head.hash,
    headNumber: head.number,
    blocks: Object.freeze(ledgerBlocks),
    authorizations: Object.freeze(authorizations),
    remainingByKey: Object.freeze(remainingByKey),
    usedRequestsByKey: Object.freeze(usedRequestsByKey),
  });
}

export const replayFullChain = replayEvents;
export const replayHistory = replayEvents;
export const replayAuthorizationEvents = replayEvents;
