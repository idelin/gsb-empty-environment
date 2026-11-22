import type { MerkleProof } from "./merkle-types.ts";

export type AmountInput = bigint | number | string;
export type BlockHeightInput = bigint | number | string;

export interface AuthorizationInput {
  chainId: string;
  contract: string;
  issuer: string;
  beneficiary: string;
  totalAmount: AmountInput;
  validFrom: BlockHeightInput;
  validTo: BlockHeightInput;
  authorizationId: string;
}

export interface Authorization {
  readonly chainId: string;
  readonly contract: string;
  readonly issuer: string;
  readonly beneficiary: string;
  readonly totalAmount: bigint;
  readonly validFrom: number;
  readonly validTo: number;
  readonly authorizationId: string;
}

export interface SignedAuthorizationInput {
  authorization: AuthorizationInput;
  signature: string;
}

export interface SignedAuthorization {
  readonly authorization: Authorization;
  readonly signature: string;
}

export type EventKindInput =
  | "spend"
  | "consume"
  | "consumption"
  | "charge"
  | "invalidate"
  | "invalidation"
  | "fail"
  | "revoke"
  | "revocation"
  | "cancel";

export type EventKind = "spend" | "invalidate" | "revoke";

export interface EventInput {
  kind?: EventKindInput;
  type?: EventKindInput;
  blockHash?: string;
  parentHash?: string;
  parentBlockHash?: string;
  blockNumber?: BlockHeightInput;
  sequence?: number | string | bigint;
  index?: number | string | bigint;
  logIndex?: number | string | bigint;
  eventIndex?: number | string | bigint;
  txIndex?: number | string | bigint;
  authorization?: AuthorizationInput;
  signedAuthorization?: SignedAuthorizationInput;
  signature?: string;
  amount?: AmountInput;
  requestId?: string;
  businessRequestId?: string;
  requestKey?: string;
  revoke?: boolean;
  revoked?: boolean;
  revocation?: boolean;
  invalidated?: boolean;
  invalidate?: boolean;
}

export interface LedgerEvent {
  readonly kind: EventKind;
  readonly authorization: Authorization;
  readonly signature: string;
  readonly recoveredSigner: string;
  readonly amount: bigint | null;
  readonly requestId: string | null;
  readonly revokeAfter: boolean;
  readonly invalidated: boolean;
  readonly blockHash: string;
  readonly parentHash: string;
  readonly blockNumber: number;
  readonly sequence: number;
}

export interface BlockInput {
  chainId?: string;
  hash?: string;
  blockHash?: string;
  parentHash?: string;
  number?: BlockHeightInput;
  blockNumber?: BlockHeightInput;
  height?: BlockHeightInput;
  parentBlockHash?: string;
  events?: ReadonlyArray<EventInput>;
}

export interface LedgerBlock {
  readonly chainId: string;
  readonly hash: string;
  readonly parentHash: string;
  readonly number: number;
  readonly events: ReadonlyArray<LedgerEvent>;
}

export interface SignerRecovery {
  (authorization: Authorization, signature: string): string;
}

export type RecoverSigner = SignerRecovery;

export type RequestStatus = "active" | "invalidated" | "revoked";

export interface RequestRecord {
  readonly requestId: string;
  readonly status: RequestStatus;
}

export interface AuthorizationStateView {
  readonly key: string;
  readonly authorization: Authorization;
  readonly signature: string;
  readonly recoveredSigner: string;
  readonly totalAmount: bigint;
  readonly consumed: bigint;
  /** totalAmount - consumed; revocation does not erase consumed value. */
  readonly remaining: bigint;
  /** Amount that can still be spent right now. */
  readonly available: bigint;
  readonly spendable: bigint;
  readonly revoked: boolean;
  readonly invalidated: boolean;
  readonly requests: ReadonlyArray<RequestRecord>;
}

export interface ChainHead {
  readonly chainId: string;
  readonly hash: string;
  readonly number: number;
}

export interface FinalityCertificate {
  readonly chainId: string;
  readonly blockHash: string;
  /** Root-to-final path, inclusive. If omitted, the ledger derives it. */
  readonly ancestors?: ReadonlyArray<string>;
  readonly ancestorHashes?: ReadonlyArray<string>;
  readonly path?: ReadonlyArray<string>;
}

export interface AppendBlockResult {
  readonly block: LedgerBlock;
  readonly head: ChainHead;
  readonly changedHead: boolean;
}

export interface AuthorizationTree {
  readonly root: string;
  readonly leaves: ReadonlyMap<string, string>;
}

export interface SnapshotLeafState {
  readonly authorization: AuthorizationInput;
  readonly signature: string;
  readonly totalAmount?: AmountInput;
  readonly consumed: AmountInput;
  readonly requests?: ReadonlyArray<string | RequestRecord>;
  readonly revoked?: boolean;
  readonly invalidated?: boolean;
}

export interface SnapshotChangeInput {
  readonly key?: string;
  readonly state: SnapshotLeafState;
}

export interface SnapshotChange {
  readonly key: string;
  readonly state: SnapshotLeafState;
  readonly oldLeaf: string;
  readonly newLeaf: string;
  readonly proof: MerkleProof;
}

export interface CompressedSnapshot {
  readonly version: 1;
  readonly chainId: string;
  readonly headHash?: string;
  readonly baseRoot: string;
  readonly root: string;
  readonly changes: ReadonlyArray<SnapshotChange>;
}

export interface SnapshotVerification {
  readonly root: string;
  readonly baseRoot: string;
  readonly changes: ReadonlyArray<{
    readonly key: string;
    readonly oldLeaf: string;
    readonly newLeaf: string;
  }>;
}

export interface PersistedEvent {
  readonly kind: EventKind;
  readonly authorization: {
    readonly chainId: string;
    readonly contract: string;
    readonly issuer: string;
    readonly beneficiary: string;
    readonly totalAmount: string;
    readonly validFrom: number;
    readonly validTo: number;
    readonly authorizationId: string;
  };
  readonly signature: string;
  readonly amount: string | null;
  readonly requestId: string | null;
  readonly revokeAfter: boolean;
  readonly invalidated: boolean;
  readonly blockHash: string;
  readonly parentHash: string;
  readonly blockNumber: number;
  readonly sequence: number;
}

export interface PersistedBlock {
  readonly chainId: string;
  readonly hash: string;
  readonly parentHash: string;
  readonly number: number;
  readonly events: ReadonlyArray<PersistedEvent>;
}

export interface PersistedSnapshotLeaf {
  readonly key: string;
  readonly authorization: {
    readonly chainId: string;
    readonly contract: string;
    readonly issuer: string;
    readonly beneficiary: string;
    readonly totalAmount: string;
    readonly validFrom: number;
    readonly validTo: number;
    readonly authorizationId: string;
  };
  readonly signature: string;
  readonly totalAmount: string;
  readonly consumed: string;
  readonly requests: ReadonlyArray<RequestRecord>;
  readonly revoked: boolean;
  readonly invalidated: boolean;
}

export interface PersistedSnapshotOverlay {
  readonly chainId: string;
  readonly headHash: string;
  readonly root: string;
  readonly leaves: ReadonlyArray<PersistedSnapshotLeaf>;
}

export interface PersistedLedgerState {
  readonly version: 1;
  readonly revision: number;
  readonly blocks: ReadonlyArray<PersistedBlock>;
  readonly heads: Readonly<Record<string, string>>;
  readonly finality: Readonly<Record<string, FinalityCertificate>>;
  readonly snapshotOverlays?: Readonly<Record<string, PersistedSnapshotOverlay>>;
}

export interface LedgerOptions {
  recoverSigner?: SignerRecovery;
  recover?: SignerRecovery;
  signatureRecovery?: SignerRecovery;
  persistence?: AtomicPersistence;
  /** Optional chain used when a block and all of its events omit chainId. */
  defaultChainId?: string;
  /** Verify caller-provided sequence is strictly unique but do not require 0..n-1. */
  strictSequence?: boolean;
}

export interface AtomicPersistence {
  load(): Promise<PersistedLedgerState | null | undefined>;
  commit(state: PersistedLedgerState): Promise<void>;
}

export interface LoadLedgerOptions extends LedgerOptions {
  persistence: AtomicPersistence;
}

export interface ReplayBlockInput {
  readonly chainId?: string;
  readonly hash?: string;
  readonly blockHash?: string;
  readonly parentHash?: string;
  readonly parentBlockHash?: string;
  readonly number?: BlockHeightInput;
  readonly blockNumber?: BlockHeightInput;
  readonly height?: BlockHeightInput;
  readonly events: ReadonlyArray<EventInput | LedgerEvent>;
}

export interface ReplayReport {
  readonly chainId: string;
  readonly headHash: string;
  readonly headNumber: number;
  readonly blocks: ReadonlyArray<LedgerBlock>;
  readonly authorizations: Readonly<Record<string, AuthorizationStateView>>;
  readonly remainingByKey: Readonly<Record<string, bigint>>;
  readonly usedRequestsByKey: Readonly<Record<string, ReadonlyArray<string>>>;
}
