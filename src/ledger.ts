import {
  isRootParent,
  normalizeId,
  toBlockHeight,
} from "./canonical.ts";
import {
  authorizationKey,
  normalizeAuthorization,
  recoverAuthorizationSigner,
} from "./authorization.ts";
import {
  freezeBlock,
  normalizeBlockInput,
  sameBlock,
  type NormalizedBlockInput,
} from "./block.ts";
import { ledgerError } from "./errors.ts";
import {
  buildAuthorizationTree,
  createMerkleProof,
} from "./merkle.ts";
import {
  applyEvent,
  cloneState,
  emptyState,
  stateLeafHash,
  stateToView,
  type InternalAuthorizationState,
  type InternalLedgerState,
} from "./reducer.ts";
import {
  createCompressedSnapshot as createSnapshotFromStates,
  snapshotStateToInternal,
  verifyCompressedSnapshot as verifySnapshotProofs,
} from "./snapshot.ts";
import { replayEvents } from "./replay.ts";
import type {
  AppendBlockResult,
  AtomicPersistence,
  Authorization,
  AuthorizationInput,
  AuthorizationStateView,
  AuthorizationTree,
  BlockInput,
  ChainHead,
  CompressedSnapshot,
  EventInput,
  FinalityCertificate,
  LedgerBlock,
  LedgerEvent,
  LedgerOptions,
  PersistedBlock,
  PersistedLedgerState,
  PersistedSnapshotOverlay,
  RecoverSigner,
  ReplayReport,
  SignerRecovery,
  SnapshotChangeInput,
  SnapshotVerification,
} from "./types.ts";
import { persistedEvent } from "./authorization.ts";

type AuthorizationReference =
  | string
  | Authorization
  | AuthorizationInput
  | {
      key?: string;
      signature?: string;
      authorizationId?: string;
    };

interface SnapshotOverlay {
  readonly chainId: string;
  readonly headHash: string;
  readonly root: string;
  readonly state: InternalLedgerState;
}

const MISSING_RECOVERY: SignerRecovery = () => {
  throw ledgerError(
    "INVALID_SIGNATURE",
    "an injected signature recovery function is required",
  );
};

function defaultOptions(options?: LedgerOptions): {
  recoverSigner: SignerRecovery;
  persistence?: AtomicPersistence;
  defaultChainId?: string;
  strictSequence: boolean;
} {
  if (!options) {
    return { recoverSigner: MISSING_RECOVERY, strictSequence: false };
  }
  return {
    recoverSigner:
      options.recoverSigner ?? options.recover ?? options.signatureRecovery ?? MISSING_RECOVERY,
    persistence: options.persistence,
    defaultChainId: options.defaultChainId,
    strictSequence: options.strictSequence === true,
  };
}

function reverse<T>(items: T[]): T[] {
  return items.reverse();
}

function recordEntries<T>(map: Map<string, T>): Record<string, T> {
  const result: Record<string, T> = {};
  for (const key of Array.from(map.keys()).sort()) {
    result[key] = map.get(key) as T;
  }
  return result;
}

export class AuthorizationLedger {
  private readonly recoverSigner: SignerRecovery;
  private readonly defaultChainId?: string;
  private readonly strictSequence: boolean;
  private persistenceAdapter?: AtomicPersistence;

  private readonly blocks = new Map<string, LedgerBlock>();
  private readonly order: LedgerBlock[] = [];
  private readonly heads = new Map<string, string>();
  private readonly finality = new Map<string, FinalityCertificate>();
  private readonly stateCache = new Map<string, InternalLedgerState>();
  private readonly acceptedSnapshots = new Map<string, CompressedSnapshot>();
  private readonly overlays = new Map<string, SnapshotOverlay>();

  private revision = 0;
  private pauseDepth = 0;
  private pendingSave = false;

  constructor(options?: LedgerOptions | SignerRecovery, extra?: LedgerOptions) {
    let resolved: LedgerOptions | undefined;
    if (typeof options === "function") {
      resolved = { ...(extra ?? {}), recoverSigner: options };
    } else {
      resolved = options;
    }
    const normalized = defaultOptions(resolved);
    this.recoverSigner = normalized.recoverSigner;
    this.persistenceAdapter = normalized.persistence;
    this.defaultChainId = normalized.defaultChainId;
    this.strictSequence = normalized.strictSequence;
  }

  get recover(): SignerRecovery {
    return this.recoverSigner;
  }

  get revisionNumber(): number {
    return this.revision;
  }

  setPersistence(persistence: AtomicPersistence | undefined): void {
    this.persistenceAdapter = persistence;
  }

  appendBlock(input: BlockInput): AppendBlockResult {
    const normalized = normalizeBlockInput(input, this.recoverSigner, {
      defaultChainId: this.defaultChainId,
      strictSequence: this.strictSequence,
    });
    return this.insertNormalizedBlock(normalized, true);
  }

  addBlock(input: BlockInput): AppendBlockResult {
    return this.appendBlock(input);
  }

  submitBlock(input: BlockInput): AppendBlockResult {
    return this.appendBlock(input);
  }

  applyBlock(input: BlockInput): AppendBlockResult {
    return this.appendBlock(input);
  }

  appendBlocks(inputs: ReadonlyArray<BlockInput>): AppendBlockResult[] {
    return inputs.map((input) => this.appendBlock(input));
  }

  appendEvents(
    events: ReadonlyArray<EventInput>,
    block?: Omit<BlockInput, "events">,
  ): AppendBlockResult {
    if (block) return this.appendBlock({ ...block, events: [...events] });
    const first = events[0];
    if (!first?.blockHash || first.parentHash === undefined) {
      throw ledgerError(
        "INVALID_EVENT",
        "appendEvents without a block requires blockHash and parentHash on every event",
      );
    }
    return this.appendBlock({
      chainId:
        first.signedAuthorization?.authorization.chainId ??
        first.authorization?.chainId ??
        this.defaultChainId,
      hash: first.blockHash,
      parentHash: first.parentHash,
      number: first.blockNumber ?? 0,
      events: [...events],
    });
  }

  appendEvent(event: EventInput): AppendBlockResult {
    return this.submitEvent(event);
  }

  addEvent(event: EventInput): AppendBlockResult {
    return this.submitEvent(event);
  }

  submitEvent(event: EventInput): AppendBlockResult {
    const hash = event.blockHash;
    const parentHash = event.parentHash;
    if (!hash || parentHash === undefined) {
      throw ledgerError("INVALID_EVENT", "submitEvent requires event.blockHash and parentHash");
    }
    const chainId = event.signedAuthorization?.authorization.chainId ??
      event.authorization?.chainId ??
      this.defaultChainId;
    return this.appendBlock({
      chainId,
      hash,
      parentHash,
      number: event.blockNumber ?? 0,
      events: [event],
    });
  }

  getEvents(chainId?: string, endHash?: string): LedgerEvent[] {
    return this.getHistory(chainId, endHash).flatMap((entry) => [...entry.events]);
  }

  replay(chainIdOrHash?: string, endHash?: string): ReplayReport {
    const blocks = this.getHistory(chainIdOrHash, endHash);
    if (blocks.length === 0) {
      throw ledgerError("INVALID_BLOCK", "cannot replay an empty history");
    }
    const head = blocks[blocks.length - 1];
    return replayEvents(blocks, this.recoverSigner, {
      chainId: head.chainId,
      headHash: head.hash,
    });
  }

  replayHistory(chainIdOrHash?: string, endHash?: string): ReplayReport {
    return this.replay(chainIdOrHash, endHash);
  }

  private insertNormalizedBlock(
    normalized: NormalizedBlockInput,
    adoptHead: boolean,
  ): AppendBlockResult {
    const existing = this.blocks.get(normalized.hash);
    if (existing) {
      const candidate = freezeBlock(normalized);
      if (!sameBlock(existing, candidate)) {
        throw ledgerError(
          "BLOCK_CONFLICT",
          `block ${normalized.hash} was already stored with different contents`,
        );
      }
      return {
        block: existing,
        head: this.requireHead(normalized.chainId),
        changedHead: false,
      };
    }

    if (normalized.hash === normalized.parentHash) {
      throw ledgerError("INVALID_BLOCK", "block cannot be its own parent");
    }

    const parent = this.blocks.get(normalized.parentHash);
    if (parent) {
      if (parent.chainId !== normalized.chainId) {
        throw ledgerError("CHAIN_MISMATCH", "block and parent belong to different chains");
      }
      if (normalized.number !== parent.number + 1) {
        throw ledgerError(
          "INVALID_BLOCK",
          `block ${normalized.hash} has height ${normalized.number}; ` +
            `parent ${parent.hash} has height ${parent.number}`,
        );
      }
    } else if (!isRootParent(normalized.parentHash) && normalized.number !== 0) {
      throw ledgerError(
        "UNKNOWN_BLOCK",
        `parent ${normalized.parentHash} is unknown for block ${normalized.hash}`,
      );
    }

    const parentState = parent ? this.stateAtBlock(parent.hash) : emptyState();
    const nextState = cloneState(parentState);
    for (const event of normalized.events) {
      applyEvent(nextState, event);
    }

    const current = this.heads.get(normalized.chainId);
    const willAdoptHead =
      adoptHead && (current === undefined || current === normalized.parentHash);
    const finalized = this.finality.get(normalized.chainId);
    if (
      willAdoptHead &&
      finalized &&
      !this.isAncestor(finalized.blockHash, normalized.parentHash)
    ) {
      throw ledgerError(
        "FINALITY_CONFLICT",
        `block ${normalized.hash} would become a head incompatible with ` +
          `finalized block ${finalized.blockHash}`,
      );
    }

    const frozen = freezeBlock(normalized);
    this.blocks.set(frozen.hash, frozen);
    this.order.push(frozen);
    this.stateCache.set(frozen.hash, nextState);
    this.overlays.delete(frozen.chainId);
    this.revision += 1;

    let changedHead = false;
    if (willAdoptHead) {
      this.heads.set(frozen.chainId, frozen.hash);
      changedHead = current !== frozen.hash;
    }

    return {
      block: frozen,
      head: this.requireHead(frozen.chainId),
      changedHead,
    };
  }

  switchHead(blockHash: string): ChainHead;
  switchHead(chainId: string, blockHash: string): ChainHead;
  switchHead(target: { chainId?: string; blockHash?: string; hash?: string }): ChainHead;
  switchHead(
    chainOrTarget: string | { chainId?: string; blockHash?: string; hash?: string },
    maybeHash?: string,
  ): ChainHead {
    let chainId: string | undefined;
    let blockHash: string;
    if (typeof chainOrTarget === "string") {
      if (maybeHash !== undefined) {
        chainId = normalizeId(chainOrTarget, "chainId");
        blockHash = normalizeId(maybeHash, "blockHash");
      } else {
        blockHash = normalizeId(chainOrTarget, "blockHash");
      }
    } else {
      chainId = chainOrTarget.chainId
        ? normalizeId(chainOrTarget.chainId, "chainId")
        : undefined;
      blockHash = normalizeId(chainOrTarget.blockHash ?? chainOrTarget.hash ?? "", "blockHash");
    }

    const block = this.blocks.get(blockHash);
    if (!block) throw ledgerError("UNKNOWN_BLOCK", `unknown block ${blockHash}`);
    if (chainId !== undefined && chainId !== block.chainId) {
      throw ledgerError("CHAIN_MISMATCH", "block belongs to another chain");
    }

    const finalized = this.finality.get(block.chainId);
    if (finalized && !this.isAncestor(finalized.blockHash, block.hash)) {
      throw ledgerError(
        "FINALITY_CONFLICT",
        `head ${block.hash} is incompatible with finalized block ${finalized.blockHash}`,
      );
    }

    const state = this.stateAtBlock(block.hash);
    this.heads.set(block.chainId, block.hash);
    this.stateCache.set(block.hash, state);
    this.overlays.delete(block.chainId);
    this.revision += 1;
    return this.requireHead(block.chainId);
  }

  setHead(blockHash: string): ChainHead;
  setHead(chainId: string, blockHash: string): ChainHead;
  setHead(
    chainOrTarget: string | { chainId?: string; blockHash?: string; hash?: string },
    maybeHash?: string,
  ): ChainHead {
    if (typeof chainOrTarget === "string" && maybeHash !== undefined) {
      return this.switchHead(chainOrTarget, maybeHash);
    }
    if (typeof chainOrTarget === "string") return this.switchHead(chainOrTarget);
    return this.switchHead(chainOrTarget);
  }

  reorganize(blockHash: string): ChainHead;
  reorganize(chainId: string, blockHash: string): ChainHead;
  reorganize(chainOrTarget: string | { chainId?: string; blockHash?: string; hash?: string }, maybeHash?: string): ChainHead {
    if (typeof chainOrTarget === "string" && maybeHash !== undefined) {
      return this.switchHead(chainOrTarget, maybeHash);
    }
    if (typeof chainOrTarget === "string") return this.switchHead(chainOrTarget);
    return this.switchHead(chainOrTarget);
  }

  getHead(): Record<string, ChainHead>;
  getHead(chainId: string): ChainHead | undefined;
  getHead(chainId?: string): Record<string, ChainHead> | ChainHead | undefined {
    if (chainId === undefined) return recordEntries(this.headsView());
    return this.headsView().get(normalizeId(chainId, "chainId"));
  }

  head(chainId: string): ChainHead | undefined {
    return this.getHead(chainId);
  }

  getHeadHash(chainId: string): string | undefined {
    return this.heads.get(normalizeId(chainId, "chainId"));
  }

  private headsView(): Map<string, ChainHead> {
    const result = new Map<string, ChainHead>();
    for (const [chainId, hash] of this.heads) {
      const block = this.blocks.get(hash);
      if (block) {
        result.set(chainId, Object.freeze({ chainId, hash, number: block.number }));
      }
    }
    return result;
  }

  private requireHead(chainId: string): ChainHead {
    const view = this.headsView().get(chainId);
    if (!view) throw ledgerError("INVALID_BLOCK", `chain ${chainId} has no head`);
    return view;
  }

  getBlock(hash: string): LedgerBlock | undefined {
    return this.blocks.get(normalizeId(hash, "blockHash"));
  }

  getBlockByNumber(chainId: string, number: number | string | bigint): LedgerBlock | undefined {
    const normalizedChain = normalizeId(chainId, "chainId");
    const height = toBlockHeight(number, "block.number");
    return this.order.find(
      (block) => block.chainId === normalizedChain && block.number === height,
    );
  }

  getBlockAtHeight(chainId: string, number: number | string | bigint): LedgerBlock | undefined {
    return this.getBlockByNumber(chainId, number);
  }

  hasBlock(hash: string): boolean {
    return this.blocks.has(normalizeId(hash, "blockHash"));
  }

  getAllBlocks(chainId?: string): LedgerBlock[] {
    if (chainId === undefined) return [...this.order];
    const normalized = normalizeId(chainId, "chainId");
    return this.order.filter((block) => block.chainId === normalized);
  }

  getHistory(chainId?: string, endHash?: string): LedgerBlock[] {
    if (chainId === undefined) {
      if (this.heads.size !== 1) {
        throw ledgerError("INVALID_BLOCK", "getHistory requires chainId when multiple chains exist");
      }
      return this.getPath(this.heads.values().next().value as string);
    }
    if (endHash !== undefined) {
      const block = this.blocks.get(normalizeId(endHash, "endHash"));
      if (!block) throw ledgerError("UNKNOWN_BLOCK", `unknown block ${endHash}`);
      if (block.chainId !== normalizeId(chainId, "chainId")) {
        throw ledgerError("CHAIN_MISMATCH", "history end belongs to another chain");
      }
      return this.getPath(block.hash);
    }
    const normalized = normalizeId(chainId, "chainId");
    const byBlock = this.blocks.get(normalized);
    if (byBlock) return this.getPath(byBlock.hash);
    const head = this.heads.get(normalized);
    if (!head) throw ledgerError("UNKNOWN_BLOCK", `chain ${normalized} has no head`);
    return this.getPath(head);
  }

  history(chainIdOrHash?: string): LedgerBlock[] {
    return this.getHistory(chainIdOrHash);
  }

  getCanonicalHistory(chainId: string): LedgerBlock[] {
    return this.getHistory(chainId);
  }

  getKnownBranches(chainId: string): LedgerBlock[] {
    const normalized = normalizeId(chainId, "chainId");
    const chainBlocks = this.getAllBlocks(normalized);
    const parents = new Set(chainBlocks.map((block) => block.parentHash));
    return chainBlocks.filter((block) => !parents.has(block.hash));
  }

  private getPath(headHash: string): LedgerBlock[] {
    const path: LedgerBlock[] = [];
    const seen = new Set<string>();
    let cursor = headHash;
    while (true) {
      if (seen.has(cursor)) {
        throw ledgerError("INVALID_BLOCK", `block parent cycle at ${cursor}`);
      }
      seen.add(cursor);
      const block = this.blocks.get(cursor);
      if (!block) {
        throw ledgerError("UNKNOWN_BLOCK", `missing block ${cursor} in history`);
      }
      path.push(block);
      const parent = this.blocks.get(block.parentHash);
      if (!parent) {
        if (isRootParent(block.parentHash) || block.number === 0) break;
        throw ledgerError("UNKNOWN_BLOCK", `missing parent ${block.parentHash} of ${block.hash}`);
      }
      cursor = parent.hash;
    }
    return reverse(path);
  }

  isAncestor(ancestorHash: string, descendantHash: string): boolean {
    const ancestor = normalizeId(ancestorHash, "ancestorHash");
    let cursor: string | undefined = normalizeId(descendantHash, "descendantHash");
    const seen = new Set<string>();
    while (cursor !== undefined) {
      if (cursor === ancestor) return true;
      if (seen.has(cursor)) {
        throw ledgerError("INVALID_BLOCK", `block parent cycle at ${cursor}`);
      }
      seen.add(cursor);
      const block = this.blocks.get(cursor);
      if (!block) return false;
      cursor = this.blocks.has(block.parentHash) ? block.parentHash : undefined;
    }
    return false;
  }

  private stateAtBlock(blockHash?: string): InternalLedgerState {
    if (blockHash === undefined) return emptyState();
    const normalized = normalizeId(blockHash, "blockHash");
    const cached = this.stateCache.get(normalized);
    if (cached) return cloneState(cached);

    const path: LedgerBlock[] = [];
    let cursor: string | undefined = normalized;
    let base = emptyState();
    const seen = new Set<string>();
    while (cursor !== undefined) {
      if (seen.has(cursor)) {
        throw ledgerError("INVALID_BLOCK", `block parent cycle at ${cursor}`);
      }
      seen.add(cursor);
      const block = this.blocks.get(cursor);
      if (!block) throw ledgerError("UNKNOWN_BLOCK", `unknown block ${cursor}`);
      const cachedAncestor = this.stateCache.get(cursor);
      if (cachedAncestor) {
        base = cloneState(cachedAncestor);
        break;
      }
      path.push(block);
      if (!this.blocks.has(block.parentHash)) {
        if (!isRootParent(block.parentHash) && block.number !== 0) {
          throw ledgerError("UNKNOWN_BLOCK", `missing parent ${block.parentHash}`);
        }
        break;
      }
      cursor = block.parentHash;
    }

    const state = cloneState(base);
    for (const block of reverse(path)) {
      for (const event of block.events) applyEvent(state, event);
      this.stateCache.set(block.hash, cloneState(state));
    }
    return state;
  }

  getStateAt(blockHash: string): Record<string, AuthorizationStateView> {
    return recordEntries(this.views(this.stateAtBlock(blockHash)));
  }

  private views(state: InternalLedgerState): Map<string, AuthorizationStateView> {
    const result = new Map<string, AuthorizationStateView>();
    for (const [key, value] of state) result.set(key, stateToView(value));
    return result;
  }

  private stateForChain(chainId: string): InternalLedgerState {
    const normalized = normalizeId(chainId, "chainId");
    const head = this.heads.get(normalized);
    if (!head) return emptyState();
    const overlay = this.overlays.get(normalized);
    if (overlay && overlay.headHash === head) return cloneState(overlay.state);
    return this.stateAtBlock(head);
  }

  private findAuthorization(
    chainId: string,
    reference: AuthorizationReference,
    signature?: string,
  ): InternalAuthorizationState | undefined {
    const normalizedChain = normalizeId(chainId, "chainId");
    const state = this.stateForChain(normalizedChain);
    if (typeof reference === "string") {
      const direct = state.get(reference);
      if (direct) return direct;
      const matches: InternalAuthorizationState[] = [];
      for (const entry of state.values()) {
        if (entry.signature === reference) matches.push(entry);
        else if (entry.authorization.authorizationId === reference) matches.push(entry);
      }
      if (signature !== undefined) {
        const matchingSignature = matches.find((entry) => entry.signature === signature);
        if (matchingSignature) return matchingSignature;
      }
      if (matches.length === 1) return matches[0];
      if (matches.length > 1) {
        throw ledgerError(
          "UNKNOWN_AUTHORIZATION",
          "authorization reference is ambiguous; supply the full signature",
        );
      }
      return undefined;
    }

    if (!reference || typeof reference !== "object") return undefined;
    if ("key" in reference && typeof reference.key === "string") {
      return state.get(reference.key);
    }

    const authorization =
      "authorization" in reference && reference.authorization
        ? reference.authorization
        : (reference as AuthorizationInput);
    const signedSignature =
      signature ??
      ("signature" in reference && typeof reference.signature === "string"
        ? reference.signature
        : undefined);
    if (signedSignature && authorization) {
      const normalized = normalizeAuthorization(authorization);
      return state.get(authorizationKey(normalized, signedSignature));
    }
    if ("authorizationId" in reference && typeof reference.authorizationId === "string") {
      return this.findAuthorization(normalizedChain, reference.authorizationId);
    }
    return undefined;
  }

  getAuthorizationState(
    chainId: string,
    reference: AuthorizationReference,
    signature?: string,
  ): AuthorizationStateView | undefined {
    const value = this.findAuthorization(chainId, reference, signature);
    return value ? stateToView(value) : undefined;
  }

  getAuthorization(
    chainId: string,
    reference: AuthorizationReference,
    signature?: string,
  ): AuthorizationStateView | undefined {
    return this.getAuthorizationState(chainId, reference, signature);
  }

  listAuthorizations(chainId: string): AuthorizationStateView[] {
    return Array.from(this.stateForChain(chainId).values(), stateToView).sort((left, right) =>
      left.key.localeCompare(right.key),
    );
  }

  getRemaining(
    chainId: string,
    reference: AuthorizationReference,
    signature?: string,
  ): bigint | undefined {
    const value = this.findAuthorization(chainId, reference, signature);
    return value ? value.totalAmount - value.consumed : undefined;
  }

  getAvailable(
    chainId: string,
    reference: AuthorizationReference,
    signature?: string,
  ): bigint | undefined {
    const value = this.findAuthorization(chainId, reference, signature);
    if (!value) return undefined;
    return value.revoked || value.invalidated ? 0n : value.totalAmount - value.consumed;
  }

  remaining(
    chainId: string,
    reference: AuthorizationReference,
    signature?: string,
  ): bigint | undefined {
    return this.getRemaining(chainId, reference, signature);
  }

  getUsedRequests(
    chainId: string,
    reference: AuthorizationReference,
    signature?: string,
  ): string[] | undefined {
    const value = this.findAuthorization(chainId, reference, signature);
    return value
      ? Array.from(value.requests.keys()).sort((left, right) => left.localeCompare(right))
      : undefined;
  }

  hasUsedRequest(
    chainId: string,
    reference: AuthorizationReference,
    requestId: string,
    signature?: string,
  ): boolean {
    return this.findAuthorization(chainId, reference, signature)?.requests.has(requestId) ?? false;
  }

  isRequestUsed(
    chainId: string,
    reference: AuthorizationReference,
    requestId: string,
    signature?: string,
  ): boolean {
    return this.hasUsedRequest(chainId, reference, requestId, signature);
  }

  getRequestStatus(
    chainId: string,
    reference: AuthorizationReference,
    requestId: string,
    signature?: string,
  ): string | undefined {
    return this.findAuthorization(chainId, reference, signature)?.requests.get(requestId)?.status;
  }

  finalize(
    certificateOrBlockHash: string | FinalityCertificate,
    maybeBlockHash?: string,
  ): FinalityCertificate {
    let certificate: FinalityCertificate;
    if (typeof certificateOrBlockHash === "string") {
      const chainId =
        maybeBlockHash === undefined
          ? undefined
          : normalizeId(certificateOrBlockHash, "certificate.chainId");
      const blockHash = maybeBlockHash ?? certificateOrBlockHash;
      const block = this.blocks.get(normalizeId(blockHash, "blockHash"));
      if (!block) throw ledgerError("UNKNOWN_BLOCK", `unknown finalized block ${blockHash}`);
      if (chainId !== undefined && chainId !== block.chainId) {
        throw ledgerError("CHAIN_MISMATCH", "finalized block belongs to another chain");
      }
      certificate = { chainId: block.chainId, blockHash: block.hash };
    } else {
      const block = this.blocks.get(normalizeId(certificateOrBlockHash.blockHash, "blockHash"));
      if (!block) {
        throw ledgerError(
          "UNKNOWN_BLOCK",
          `unknown finalized block ${certificateOrBlockHash.blockHash}`,
        );
      }
      if (
        certificateOrBlockHash.chainId !== undefined &&
        normalizeId(certificateOrBlockHash.chainId, "certificate.chainId") !== block.chainId
      ) {
        throw ledgerError("CHAIN_MISMATCH", "finality certificate belongs to another chain");
      }
      certificate = {
        ...certificateOrBlockHash,
        chainId: block.chainId,
      };
    }
    return this.finalizeCertificate(certificate);
  }

  finalizeBlock(
    certificateOrBlockHash: string | FinalityCertificate,
    maybeBlockHash?: string,
  ): FinalityCertificate {
    return this.finalize(certificateOrBlockHash, maybeBlockHash);
  }

  setFinality(
    certificateOrBlockHash: string | FinalityCertificate,
    maybeBlockHash?: string,
  ): FinalityCertificate {
    return this.finalize(certificateOrBlockHash, maybeBlockHash);
  }

  submitFinality(
    certificateOrBlockHash: string | FinalityCertificate,
    maybeBlockHash?: string,
  ): FinalityCertificate {
    return this.finalize(certificateOrBlockHash, maybeBlockHash);
  }

  submitFinalityCertificate(
    certificateOrBlockHash: string | FinalityCertificate,
    maybeBlockHash?: string,
  ): FinalityCertificate {
    return this.finalize(certificateOrBlockHash, maybeBlockHash);
  }

  private finalizeCertificate(certificate: FinalityCertificate): FinalityCertificate {
    const block = this.blocks.get(normalizeId(certificate.blockHash, "certificate.blockHash"));
    if (!block) throw ledgerError("UNKNOWN_BLOCK", `unknown finalized block ${certificate.blockHash}`);
    const chainId = normalizeId(certificate.chainId, "certificate.chainId");
    if (block.chainId !== chainId) {
      throw ledgerError("CHAIN_MISMATCH", "finality certificate belongs to another chain");
    }
    if (this.overlays.has(chainId)) {
      throw ledgerError(
        "FINALITY_CONFLICT",
        "cannot finalize a chain with an uncommitted compressed-snapshot overlay",
      );
    }

    const expectedPath = this.getPath(block.hash);
    const expectedHashes = expectedPath.map((entry) => entry.hash);
    const supplied =
      certificate.ancestors ?? certificate.ancestorHashes ?? certificate.path;
    let ancestors = expectedHashes;
    if (supplied !== undefined) {
      const normalized = supplied.map((hash) => normalizeId(hash, "certificate ancestor"));
      if (normalized.length === 0) {
        throw ledgerError("FINALITY_CONFLICT", "finality certificate has no ancestors");
      }
      if (normalized.length === 1 && normalized[0] === block.hash) {
        ancestors = expectedHashes;
      } else if (
        normalized[0] === block.hash &&
        normalized.length === expectedHashes.length &&
        normalized
          .slice(1)
          .every(
            (hash, index) =>
              hash === expectedHashes[expectedHashes.length - 2 - index],
          )
      ) {
        // Also accept a target-to-root certificate, while storing the canonical
        // root-to-target path.
        ancestors = expectedHashes;
      } else if (normalized[normalized.length - 1] === block.hash) {
        if (
          normalized.length !== expectedHashes.length ||
          normalized.some((hash, index) => hash !== expectedHashes[index])
        ) {
          throw ledgerError(
            "FINALITY_CONFLICT",
            "finality certificate ancestor path does not match stored history",
          );
        }
      } else {
        if (
          normalized.length >= expectedHashes.length ||
          normalized.some((hash, index) => hash !== expectedHashes[index])
        ) {
          throw ledgerError(
            "FINALITY_CONFLICT",
            "finality certificate ancestors are not a prefix of stored history",
          );
        }
        ancestors = [...normalized, block.hash];
      }
    }

    const previous = this.finality.get(chainId);
    if (previous && previous.blockHash === block.hash) {
      return previous;
    }
    if (previous && previous.blockHash !== block.hash) {
      if (!this.isAncestor(previous.blockHash, block.hash)) {
        throw ledgerError(
          "FINALITY_CONFLICT",
          `finalized block ${block.hash} conflicts with ${previous.blockHash}`,
        );
      }
    }
    const head = this.heads.get(chainId);
    if (head && !this.isAncestor(block.hash, head)) {
      throw ledgerError(
        "FINALITY_CONFLICT",
        `cannot finalize ${block.hash}: current head ${head} is not its descendant`,
      );
    }

    const frozen = Object.freeze({
      chainId,
      blockHash: block.hash,
      ancestors: Object.freeze([...ancestors]),
    });
    this.finality.set(chainId, frozen);
    this.revision += 1;
    return frozen;
  }

  getFinality(chainId: string): FinalityCertificate | undefined {
    return this.finality.get(normalizeId(chainId, "chainId"));
  }

  getFinalized(chainId: string): FinalityCertificate | undefined {
    return this.getFinality(chainId);
  }

  getFinalizedBlock(chainId: string): LedgerBlock | undefined {
    const certificate = this.getFinality(chainId);
    return certificate ? this.blocks.get(certificate.blockHash) : undefined;
  }

  getFinalizedState(chainId: string): Record<string, AuthorizationStateView> {
    const certificate = this.getFinality(chainId);
    if (!certificate) return {};
    return this.getStateAt(certificate.blockHash);
  }

  getAuthorizationTree(chainId?: string, headHash?: string): AuthorizationTree {
    const state = this.stateForChainOrBlock(chainId, headHash);
    return buildAuthorizationTree(state.values());
  }

  getCommitmentRoot(chainId?: string, headHash?: string): string {
    return this.getAuthorizationTree(chainId, headHash).root;
  }

  getAuthorizationCommitment(
    chainId: string,
    reference: AuthorizationReference,
    signature?: string,
  ): string | undefined {
    const value = this.findAuthorization(chainId, reference, signature);
    return value ? stateLeafHash(value) : undefined;
  }

  nodeCommitment(
    chainId: string,
    reference: AuthorizationReference,
    signature?: string,
  ): string | undefined {
    return this.getAuthorizationCommitment(chainId, reference, signature);
  }

  getMerkleProof(chainId: string, key: string, headHash?: string) {
    const state = this.stateForChainOrBlock(chainId, headHash);
    const entries = Array.from(state.values(), (entry) => ({
      key: entry.key,
      leaf: stateLeafHash(entry),
    }));
    return createMerkleProof(entries, key);
  }

  private stateForChainOrBlock(chainId?: string, headHash?: string): InternalLedgerState {
    if (headHash !== undefined) {
      const block = this.blocks.get(normalizeId(headHash, "blockHash"));
      if (!block) throw ledgerError("UNKNOWN_BLOCK", `unknown block ${headHash}`);
      if (chainId !== undefined && normalizeId(chainId, "chainId") !== block.chainId) {
        throw ledgerError("CHAIN_MISMATCH", "block belongs to another chain");
      }
      if (this.heads.get(block.chainId) === block.hash) {
        return this.stateForChain(block.chainId);
      }
      return this.stateAtBlock(block.hash);
    }
    if (chainId === undefined) {
      if (this.heads.size !== 1) {
        throw ledgerError("INVALID_BLOCK", "chainId is required when multiple chains exist");
      }
      chainId = this.heads.keys().next().value as string;
    }
    return this.stateForChain(chainId);
  }

  createCompressedSnapshot(
    updates: ReadonlyArray<SnapshotChangeInput>,
    options: { chainId?: string; headHash?: string } = {},
  ): CompressedSnapshot {
    let chainId = options.chainId;
    if (chainId === undefined && updates.length > 0) {
      chainId = normalizeAuthorization(updates[0].state.authorization).chainId;
    }
    if (chainId === undefined) {
      if (this.heads.size !== 1) {
        throw ledgerError("CHAIN_MISMATCH", "snapshot chainId is required");
      }
      chainId = this.heads.keys().next().value as string;
    }
    const normalizedChain = normalizeId(chainId, "chainId");
    const state = this.stateForChainOrBlock(normalizedChain, options.headHash);
    const headHash = options.headHash ?? this.heads.get(normalizedChain);
    return createSnapshotFromStates(
      normalizedChain,
      Array.from(state.values()),
      updates,
      { headHash },
    );
  }

  verifyCompressedSnapshot(snapshot: CompressedSnapshot): SnapshotVerification {
    const snapshotChainId = normalizeId(snapshot.chainId, "snapshot.chainId");
    let expected: string | undefined;
    if (snapshot.headHash !== undefined) {
      const block = this.blocks.get(normalizeId(snapshot.headHash, "snapshot.headHash"));
      if (!block) throw ledgerError("UNKNOWN_BLOCK", `unknown snapshot head ${snapshot.headHash}`);
      if (block.chainId !== snapshotChainId) {
        throw ledgerError("CHAIN_MISMATCH", "snapshot head belongs to another chain");
      }
      const currentHead = this.heads.get(block.chainId);
      const state =
        currentHead === block.hash
          ? this.stateForChain(block.chainId)
          : this.stateAtBlock(block.hash);
      expected = buildAuthorizationTree(state.values()).root;
    } else {
      const head = this.heads.get(snapshotChainId);
      if (!head) {
        throw ledgerError("UNKNOWN_BLOCK", "snapshot chain has no current head");
      }
      expected = buildAuthorizationTree(this.stateForChain(snapshotChainId).values()).root;
    }
    return verifySnapshotProofs(snapshot, expected);
  }

  submitCompressedSnapshot(snapshot: CompressedSnapshot): SnapshotVerification {
    return this.applyCompressedSnapshot(snapshot);
  }

  acceptCompressedSnapshot(snapshot: CompressedSnapshot): SnapshotVerification {
    return this.submitCompressedSnapshot(snapshot);
  }

  applyCompressedSnapshot(snapshot: CompressedSnapshot): SnapshotVerification {
    const normalizedChain = normalizeId(snapshot.chainId, "snapshot.chainId");
    const head = this.heads.get(normalizedChain);
    if (!head) throw ledgerError("UNKNOWN_BLOCK", `chain ${normalizedChain} has no head`);
    if (
      snapshot.headHash !== undefined &&
      normalizeId(snapshot.headHash, "snapshot.headHash") !== head
    ) {
      throw ledgerError("SNAPSHOT_CONFLICT", "snapshot is not for the current chain head");
    }
    if (this.finality.has(normalizedChain)) {
      throw ledgerError(
        "FINALITY_CONFLICT",
        "a compressed state snapshot cannot rewrite a finalized head",
      );
    }
    const current = this.stateForChain(normalizedChain);
    const expectedRoot = buildAuthorizationTree(current.values()).root;
    if (snapshot.baseRoot !== expectedRoot) {
      throw ledgerError("SNAPSHOT_CONFLICT", "snapshot base root does not match the current head");
    }
    const verified = verifySnapshotProofs(snapshot, expectedRoot);
    if (snapshot.changes.length === 0) {
      this.acceptedSnapshots.set(verified.root, snapshot);
      return verified;
    }
    const next = cloneState(current);
    for (const change of snapshot.changes) {
      const signer = recoverAuthorizationSigner(
        normalizeAuthorization(change.state.authorization),
        change.state.signature,
        this.recoverSigner,
      );
      const replacement = snapshotStateToInternal(change.state, signer);
      const existing = next.get(replacement.key);
      if (existing && existing.totalAmount !== replacement.totalAmount) {
        throw ledgerError(
          "INVALID_AUTHORIZATION",
          "snapshot changed a signed authorization total allowance",
        );
      }
      next.set(replacement.key, replacement);
    }
    const materializedRoot = buildAuthorizationTree(next.values()).root;
    if (materializedRoot !== verified.root) {
      throw ledgerError(
        "SNAPSHOT_CONFLICT",
        "compressed snapshot does not materialize to its committed root",
      );
    }
    this.overlays.set(normalizedChain, {
      chainId: normalizedChain,
      headHash: head,
      root: verified.root,
      state: next,
    });
    this.acceptedSnapshots.set(verified.root, snapshot);
    this.revision += 1;
    return verified;
  }

  getAcceptedSnapshot(root: string): CompressedSnapshot | undefined {
    return this.acceptedSnapshots.get(root);
  }

  pausePersistence(): void {
    this.pauseDepth += 1;
  }

  pause(): void {
    this.pausePersistence();
  }

  async resumePersistence(): Promise<void> {
    if (this.pauseDepth === 0) {
      throw ledgerError("PERSISTENCE_ERROR", "persistence is not paused");
    }
    this.pauseDepth -= 1;
    if (this.pauseDepth === 0 && this.pendingSave) {
      this.pendingSave = false;
      await this.persist();
    }
  }

  resume(): Promise<void> {
    return this.resumePersistence();
  }

  async save(persistence?: AtomicPersistence): Promise<void> {
    const adapter = persistence ?? this.persistenceAdapter;
    if (!adapter) {
      throw ledgerError("PERSISTENCE_ERROR", "no persistence adapter is configured");
    }
    if (this.pauseDepth > 0) {
      this.pendingSave = true;
      return;
    }
    const bundle = this.exportState();
    try {
      await adapter.commit(bundle);
    } catch (error) {
      if (error instanceof Error && "code" in error) throw error;
      throw ledgerError(
        "PERSISTENCE_ERROR",
        `persistence commit failed: ${error instanceof Error ? error.message : String(error)}`,
      );
    }
  }

  persist(persistence?: AtomicPersistence): Promise<void> {
    return this.save(persistence);
  }

  flush(persistence?: AtomicPersistence): Promise<void> {
    return this.save(persistence);
  }

  exportState(): PersistedLedgerState {
    const blocks: PersistedBlock[] = this.order.map((block) =>
      Object.freeze({
        chainId: block.chainId,
        hash: block.hash,
        parentHash: block.parentHash,
        number: block.number,
        events: Object.freeze(block.events.map(persistedEvent)),
      }),
    );
    const finality: Record<string, FinalityCertificate> = {};
    for (const [chainId, certificate] of this.finality) {
      finality[chainId] = {
        ...certificate,
        ancestors: certificate.ancestors ? [...certificate.ancestors] : undefined,
      };
    }
    const snapshotOverlays: Record<string, PersistedSnapshotOverlay> = {};
    for (const [chainId, overlay] of this.overlays) {
      if (overlay.headHash !== this.heads.get(chainId)) continue;
      snapshotOverlays[chainId] = {
        chainId,
        headHash: overlay.headHash,
        root: overlay.root,
        leaves: Array.from(overlay.state.values(), (state) => {
          return {
            key: state.key,
            authorization: {
              chainId: state.authorization.chainId,
              contract: state.authorization.contract,
              issuer: state.authorization.issuer,
              beneficiary: state.authorization.beneficiary,
              totalAmount: state.authorization.totalAmount.toString(),
              validFrom: state.authorization.validFrom,
              validTo: state.authorization.validTo,
              authorizationId: state.authorization.authorizationId,
            },
            signature: state.signature,
            totalAmount: state.totalAmount.toString(),
            consumed: state.consumed.toString(),
            requests: Array.from(state.requests, ([requestId, record]) => ({
              requestId,
              status: record.status,
            })),
            revoked: state.revoked,
            invalidated: state.invalidated,
          };
        }),
      };
    }
    return Object.freeze({
      version: 1 as const,
      revision: this.revision,
      blocks: Object.freeze(blocks),
      heads: Object.freeze(recordEntries(new Map(this.heads))),
      finality: Object.freeze(finality),
      ...(Object.keys(snapshotOverlays).length > 0
        ? { snapshotOverlays: Object.freeze(snapshotOverlays) }
        : {}),
    });
  }

  toJSON(): PersistedLedgerState {
    return this.exportState();
  }

  static fromState(
    state: PersistedLedgerState,
    options: LedgerOptions | SignerRecovery,
  ): AuthorizationLedger {
    const ledger = new AuthorizationLedger(options);
    ledger.restoreState(state);
    return ledger;
  }

  static restore(
    state: PersistedLedgerState,
    options: LedgerOptions | SignerRecovery,
  ): AuthorizationLedger {
    return AuthorizationLedger.fromState(state, options);
  }

  static async load(
    options: LedgerOptions | SignerRecovery,
    persistence?: AtomicPersistence,
  ): Promise<AuthorizationLedger | null> {
    let resolved: LedgerOptions;
    let adapter: AtomicPersistence | undefined;
    if (typeof options === "function") {
      resolved = { recoverSigner: options };
      adapter = persistence;
    } else {
      resolved = options;
      adapter = persistence ?? options.persistence;
    }
    if (!adapter) {
      throw ledgerError("PERSISTENCE_ERROR", "load requires a persistence adapter");
    }

    const stored = await adapter.load();
    if (stored === null || stored === undefined) return null;
    const ledger = AuthorizationLedger.fromState(stored, resolved);
    ledger.setPersistence(adapter);
    return ledger;
  }

  private restoreState(state: PersistedLedgerState): void {
    if (!state || state.version !== 1 || !Array.isArray(state.blocks)) {
      throw ledgerError("PERSISTENCE_ERROR", "unsupported persisted ledger state");
    }
    const pending = [...state.blocks];
    const loaded = new Set<string>();

    while (pending.length > 0) {
      let progress = false;
      for (let index = 0; index < pending.length; ) {
        const block = pending[index];
        if (
          !loaded.has(block.parentHash) &&
          !isRootParent(block.parentHash) &&
          block.number !== 0
        ) {
          index += 1;
          continue;
        }
        const events: EventInput[] = block.events.map((event) => ({
          kind: event.kind,
          authorization: {
            ...event.authorization,
            totalAmount: event.authorization.totalAmount,
          },
          signature: event.signature,
          amount: event.amount === null ? undefined : event.amount,
          requestId: event.requestId ?? undefined,
          revoke: event.revokeAfter,
          invalidated: event.invalidated,
          blockHash: event.blockHash,
          parentHash: event.parentHash,
          blockNumber: event.blockNumber,
          sequence: event.sequence,
        }));
        const normalized = normalizeBlockInput(
          {
            chainId: block.chainId,
            hash: block.hash,
            parentHash: block.parentHash,
            number: block.number,
            events,
          },
          this.recoverSigner,
          { strictSequence: false },
        );
        this.insertNormalizedBlock(normalized, true);
        loaded.add(block.hash);
        pending.splice(index, 1);
        progress = true;
      }
      if (!progress) {
        throw ledgerError("PERSISTENCE_ERROR", "persisted blocks are not topologically complete");
      }
    }

    for (const [chainId, headHash] of Object.entries(state.heads)) {
      const normalizedChain = normalizeId(chainId, "persisted head chain");
      const block = this.blocks.get(normalizeId(headHash, "persisted head"));
      if (!block || block.chainId !== normalizedChain) {
        throw ledgerError("PERSISTENCE_ERROR", `persisted head for ${chainId} is missing`);
      }
      this.heads.set(normalizedChain, block.hash);
    }
    const persistedFinality: Readonly<Record<string, FinalityCertificate>> =
      state.finality ?? {};
    for (const certificate of Object.values(persistedFinality)) {
      this.finalizeCertificate(certificate);
    }
    const persistedOverlays: Readonly<Record<string, PersistedSnapshotOverlay>> =
      state.snapshotOverlays ?? {};
    for (const [chainId, overlay] of Object.entries(persistedOverlays)) {
      const normalizedChain = normalizeId(chainId, "snapshot overlay chain");
      if (this.finality.has(normalizedChain)) {
        throw ledgerError(
          "PERSISTENCE_ERROR",
          `snapshot overlay for ${chainId} conflicts with finality`,
        );
      }
      if (this.heads.get(normalizedChain) !== overlay.headHash) {
        throw ledgerError(
          "PERSISTENCE_ERROR",
          `snapshot overlay for ${chainId} is not attached to the persisted head`,
        );
      }
      const restored: InternalLedgerState = new Map();
      for (const leaf of overlay.leaves) {
        const authorization = normalizeAuthorization(leaf.authorization);
        if (authorization.chainId !== normalizedChain) {
          throw ledgerError("PERSISTENCE_ERROR", "snapshot overlay leaf belongs to another chain");
        }
        const signer = recoverAuthorizationSigner(
          authorization,
          leaf.signature,
          this.recoverSigner,
        );
        const internal = snapshotStateToInternal(
          {
            authorization,
            signature: leaf.signature,
            totalAmount: leaf.totalAmount,
            consumed: leaf.consumed,
            requests: leaf.requests,
            revoked: leaf.revoked,
            invalidated: leaf.invalidated,
          },
          signer,
        );
        if (internal.key !== leaf.key) {
          throw ledgerError("PERSISTENCE_ERROR", "snapshot overlay leaf key is not bound");
        }
        restored.set(internal.key, internal);
      }
      if (buildAuthorizationTree(restored.values()).root !== overlay.root) {
        throw ledgerError("PERSISTENCE_ERROR", "snapshot overlay root does not match its leaves");
      }
      this.overlays.set(normalizedChain, {
        chainId: normalizedChain,
        headHash: overlay.headHash,
        root: overlay.root,
        state: restored,
      });
    }
    this.revision = state.revision;
  }
}

export const Ledger = AuthorizationLedger;
export const AuthLedger = AuthorizationLedger;
export const WalletAuthorizationLedger = AuthorizationLedger;
export const RelayAuthorizationLedger = AuthorizationLedger;
export const WalletLedger = AuthorizationLedger;
export const RelayLedger = AuthorizationLedger;

export function createAuthorizationLedger(
  options?: LedgerOptions | SignerRecovery,
  extra?: LedgerOptions,
): AuthorizationLedger {
  return new AuthorizationLedger(options, extra);
}

export function createLedger(
  options?: LedgerOptions | SignerRecovery,
  extra?: LedgerOptions,
): AuthorizationLedger {
  return new AuthorizationLedger(options, extra);
}

export const createWalletLedger = createAuthorizationLedger;
export const createRelayLedger = createAuthorizationLedger;
