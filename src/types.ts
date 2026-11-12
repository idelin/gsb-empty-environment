/**
 * Public types for the Ceramic-style stream conflict resolver.
 *
 * A branch is represented by its commit log in root-to-tip order.  The first
 * commit may be the stream's init event; this package only compares histories,
 * it does not validate state transitions.
 */

export type TimestampValue = number | bigint | Date | string;

/**
 * A CID can be supplied as a string or as an object whose `toString()` is the
 * canonical CID (useful when callers already use a CID library).
 */
export type CidLike = string | { toString(): string };

/**
 * Credential attached to an anchor commit.
 *
 * `blockTimestamp` (or one of its aliases) is the timestamp of the block in
 * which the anchor was included.  Extra fields are passed through untouched to
 * the injected verifier.
 */
export interface AnchorProof {
  blockTimestamp?: TimestampValue;
  timestamp?: TimestampValue;
  blockTime?: TimestampValue;
  time?: TimestampValue;
  ts?: TimestampValue;

  /**
   * Optional caller annotation.  It is only consulted when no verifier is
   * supplied, or when the annotation is explicitly `false` (which rejects the
   * anchor).  A verifier is always authoritative.
   */
  verified?: boolean;

  [key: string]: unknown;
}

/**
 * A commit in a stream log.
 *
 * Anchors can be attached to a commit through `anchor` or `anchorProof`, or
 * through the convenience fields `anchored`/`anchorTimestamp`.
 */
export interface Commit {
  cid: CidLike;
  anchor?: AnchorProof | null;
  anchorProof?: AnchorProof | null;
  anchorCommit?: AnchorProof | null;
  timestampProof?: AnchorProof | null;
  anchorVerified?: boolean;
  anchored?: boolean;
  anchorTimestamp?: TimestampValue;
  anchorBlockTimestamp?: TimestampValue;
  anchorTime?: TimestampValue;
  blockTimestamp?: TimestampValue;
  timestamp?: TimestampValue;
  type?: string;
  [key: string]: unknown;
}

/**
 * A stream state accepted by the resolver.
 *
 * The canonical history is `log` (or `commits`).  A compact summary form with
 * `tip`, `length`, `anchored` and `anchorTimestamp` is also accepted.  Summary
 * inputs must provide `commonPrefixLength`/`forkIndex` in the options if two
 * histories need to be compared at a non-root fork point.
 */
export interface StreamState {
  log?: readonly Commit[];
  commits?: readonly Commit[];
  tip?: CidLike;
  cid?: CidLike;
  length?: number;
  commitCount?: number;
  anchored?: boolean;
  anchor?: AnchorProof | null;
  anchorProof?: AnchorProof | null;
  anchorTimestamp?: TimestampValue;
  anchorBlockTimestamp?: TimestampValue;
  anchorTime?: TimestampValue;
  anchorIndex?: number;
  [key: string]: unknown;
}

export type StreamStateInput =
  | StreamState
  | readonly Commit[]
  | readonly string[];

export interface AnchorVerificationContext {
  /** Which side of the conflict is being verified. */
  side: 'current' | 'incoming';
  /** Zero-based index of the anchor commit in the branch log. */
  index: number;
  /** The commit carrying the anchor credential. */
  commit: Commit;
  /** The complete branch log, when available. */
  branch: readonly Commit[] | null;
}

/**
 * Caller-injected anchor verifier.
 *
 * Return `true` only when the credential is authentic and still authorised.
 * Returning `false`, throwing, or returning a promise from the synchronous
 * entry point makes the anchor count as unanchored.
 */
export type AnchorVerifier = (
  proof: AnchorProof,
  commit: Commit,
  context: AnchorVerificationContext,
) => boolean;

export type AsyncAnchorVerifier = (
  proof: AnchorProof,
  commit: Commit,
  context: AnchorVerificationContext,
) => boolean | Promise<boolean>;

export type AnchorVerifierInput = AnchorVerifier | AsyncAnchorVerifier;

export interface ResolveOptions {
  /**
   * Verifies anchor credentials.  This is the only authority for anchor
   * validity.  With no verifier, only credentials explicitly annotated with
   * `verified: true` are accepted (unless `trustUnverifiedAnchors` is enabled).
   */
  verifyAnchor?: AnchorVerifier;

  /** Async variant used by `resolveConflictAsync`. */
  verifyAnchorAsync?: AsyncAnchorVerifier;

  /** Alias for `verifyAnchor`. */
  verify?: AnchorVerifier;

  /** Alias for `verifyAnchor`. */
  anchorVerifier?: AnchorVerifier;

  /**
   * Number of commits shared by the two branches.  Useful for compact summary
   * inputs where a commit log cannot be inspected.  Overrides log inspection.
   */
  commonPrefixLength?: number;

  /**
   * Zero-based index of the last shared commit.  `-1` means the branches have
   * no common commit.  Ignored when `commonPrefixLength` is supplied.
   */
  forkIndex?: number;

  /**
   * Development escape hatch: treat structurally valid anchors as anchored even
   * without a verifier.  Do not use this for untrusted network input.
   */
  trustUnverifiedAnchors?: boolean;
}

export type ResolveOptionsInput = ResolveOptions | AnchorVerifierInput;

export interface AnchorInfo {
  /** CID of the commit carrying the anchor. */
  cid: string;
  /** Zero-based index in the branch log. */
  index: number;
  /** Block timestamp used for consensus, or `null` when unavailable. */
  timestamp: number | bigint | null;
  /** Original credential, exposed for diagnostics. */
  proof: AnchorProof;
}

export interface BranchInfo {
  side: 'current' | 'incoming';
  length: number;
  tip: string;
  anchored: boolean;
  /** Earliest anchor after the fork, falling back to the earliest anchor. */
  anchor: AnchorInfo | null;
  anchors: readonly AnchorInfo[];
}

export type ConflictReason =
  | 'same-tip'
  | 'incoming-descends-from-current'
  | 'current-descends-from-incoming'
  | 'current-anchored-over-unanchored'
  | 'incoming-anchored-over-unanchored'
  | 'earlier-current-anchor'
  | 'earlier-incoming-anchor'
  | 'fork-anchor-guard'
  | 'longer-current'
  | 'longer-incoming'
  | 'smaller-current-tip-cid'
  | 'smaller-incoming-tip-cid'
  | 'current-stable-tie';

export interface ConflictResult<T extends StreamStateInput = StreamStateInput> {
  /** The side that owns the canonical tip. */
  winner: 'current' | 'incoming';
  winnerSide: 'current' | 'incoming';
  /** The complete winner input, suitable for assigning to the stream state. */
  selected: T;
  /** Alias of `selected`. */
  state: T;
  winningState: T;
  losingState: T;
  /** Canonical tip selected by the resolver. */
  tip: string;
  winningTip: string;
  reason: ConflictReason;
  /** Number of commits shared before the first divergent commit. */
  commonPrefixLength: number;
  /** Index of the last shared commit; `-1` when there is no common commit. */
  forkIndex: number;
  current: BranchInfo;
  incoming: BranchInfo;
}
