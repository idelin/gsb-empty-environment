/**
 * Domain model for signed email-consent authorization.
 *
 * A consent grant binds five dimensions together:
 *   - a permanent recipient subject
 *   - a node of the purpose tree
 *   - a set of senders allowed to use the grant
 *   - a geographic scope
 *   - a half-open validity interval [validFrom, validUntil)
 *
 * Every dimension is carried inside a cryptographically signed envelope so an
 * authorization decision can cite the exact statements it rests on.
 */

/** Epoch milliseconds. Half-open intervals include `from` and exclude `to`. */
export type Timestamp = number;

/** Stable, permanent identifier of a recipient (never reused, unlike aliases). */
export type SubjectId = string;

/** An address/alias that may map to a {@link SubjectId}. */
export type Alias = string;

/** Node id inside the purpose tree, e.g. "marketing.newsletter.weekly". */
export type PurposeNodeId = string;

/** Registered sender/processor id. */
export type SenderId = string;

/** Geographic region code. "*" is the catalog wildcard. */
export type RegionCode = string;

/** Identifier of a consent chain (a lineage of statements from one issuer). */
export type ChainId = string;

/** Identifier of an individual signed statement, unique per issuer. */
export type StatementId = string;

/** Identifier of the signing key, resolvable via the key directory. */
export type KeyId = string;

/** Half-open time interval [from, to). `to === Infinity` means "open ended". */
export interface HalfOpenInterval {
  readonly from: Timestamp;
  readonly to: Timestamp;
}

/**
 * Catalog describing the immutable purpose tree. A grant on a node covers the
 * node itself and every descendant; a selective revocation may target only a
 * descendant, leaving the rest of the grant intact.
 */
export interface PurposeCatalog {
  /** Maps a node id to its direct parent id (root maps to null). */
 readonly parents: Readonly<Record<PurposeNodeId, PurposeNodeId | null>>;
}

/**
 * Catalog of geographic regions. The optional wildcard "*" matches every
 * region. Region codes are otherwise flat: there is no implicit hierarchy, so
 * scoping stays explicit and auditable.
 */
export interface RegionCatalog {
  readonly regions: ReadonlySet<RegionCode>;
  readonly wildcard?: RegionCode;
}

/** A grant/revocation scope restricted to a subset of a grant's dimensions. */
export interface ScopeFilter {
  /** Restrict to these purpose nodes (and their subtrees when on a grant). */
  readonly purposes?: readonly PurposeNodeId[];
  /** Restrict to these senders. */
  readonly senders?: readonly SenderId[];
  /** Restrict to these regions. */
  readonly regions?: readonly RegionCode[];
  /** Restrict to a time window intersected with the grant interval. */
  readonly interval?: HalfOpenInterval;
}

export type StatementKind = "grant" | "revocation" | "correction" | "recovery";

interface StatementBase {
  readonly id: StatementId;
  readonly chainId: ChainId;
  readonly issuer: KeyId;
  /** Monotonic per-chain sequence used to order and detect forks. */
  readonly seq: number;
  readonly issuedAt: Timestamp;
}

/**
 * A positive consent grant. It covers `purpose` together with the whole
 * subtree, each sender in `senders`, and each region in `regions`.
 */
export interface GrantStatement extends StatementBase {
  readonly kind: "grant";
  readonly subject: SubjectId;
  readonly purpose: PurposeNodeId;
  readonly senders: readonly SenderId[];
  readonly regions: readonly RegionCode[];
  readonly validFrom: Timestamp;
  readonly validUntil: Timestamp;
}

/**
 * A recovery statement is a grant whose validity interval is guaranteed not to
 * overlap an earlier (now lapsed) grant on the same chain; it re-establishes
 * consent for a future window without resurrecting the revoked one.
 */
export interface RecoveryStatement extends StatementBase {
  readonly kind: "recovery";
  readonly subject: SubjectId;
  readonly purpose: PurposeNodeId;
  readonly senders: readonly SenderId[];
  readonly regions: readonly RegionCode[];
  readonly validFrom: Timestamp;
  readonly validUntil: Timestamp;
  /** The lapsed statement this recovery follows (must end before validFrom). */
  readonly supersedes: StatementId;
}

/**
 * Selective revocation. Omitted dimensions mean "everything on that
 * dimension". Revoking one sub-purpose, region or sender never revokes the
 * rest of the matched grant(s).
 */
export interface RevocationStatement extends StatementBase {
  readonly kind: "revocation";
  readonly subject: SubjectId;
  /** Statements targeted by id; omitted means every grant on this chain. */
  readonly targetGrantIds?: readonly StatementId[];
  readonly filter: ScopeFilter;
}

/**
 * Explicit correction of a previously issued statement. The referenced
 * statement is replaced wholesale by `replacement` (which keeps the same id).
 * A correction may arrive before the original in transport order.
 */
export interface CorrectionStatement extends StatementBase {
  readonly kind: "correction";
  readonly correctsId: StatementId;
  readonly replacement: GrantStatement | RecoveryStatement | RevocationStatement;
}

export type ConsentStatement =
  | GrantStatement
  | RecoveryStatement
  | RevocationStatement
  | CorrectionStatement;

/**
 * A signed alias assertion maps an email address/alias onto the permanent
 * subject it belongs to. Two aliases resolving to the same subject share that
 * subject's consent; conflicting mappings make the recipient ambiguous.
 */
export interface AliasAssertion {
  readonly id: StatementId;
  readonly issuer: KeyId;
  readonly issuedAt: Timestamp;
  readonly alias: Alias;
  readonly subject: SubjectId;
}

/** Envelope pairing a statement with its detached Ed25519 signature. */
export interface SignedEnvelope<T> {
  readonly statement: T;
  /** Base64 Ed25519 signature over the canonical JSON of `statement`. */
  readonly signature: string;
}

export type SignedConsentStatement = SignedEnvelope<ConsentStatement>;
export type SignedAliasAssertion = SignedEnvelope<AliasAssertion>;

/** Resolves key ids to public verification keys. */
export interface KeyDirectory {
  getPublicKey(keyId: KeyId): import("node:crypto").KeyObject | undefined;
}

/** A single item in a publishing batch. */
export interface BatchItem {
  /** Correlation id echoed back in the result. */
  readonly itemId: string;
  readonly recipient: Alias | SubjectId;
  readonly purpose: PurposeNodeId;
  readonly sender: SenderId;
  readonly region: RegionCode;
  /**
   * Optional chain pin: when set, the item may only be authorized by this
   * chain. A pin that no chain can satisfy forces "pending".
   */
  readonly chainHint?: ChainId;
}

export type DecisionStatus = "deliverable" | "pending" | "denied";

/** Machine-readable reason codes for non-deliverable decisions. */
export type PendingReason =
  | "unknown-recipient"
  | "ambiguous-recipient"
  | "unverified-signature"
  | "malformed-statement"
  | "forked-chain"
  | "pinned-chain-mismatch"
  | "no-chain-covers-all-dimensions"
  | "unknown-purpose"
  | "unknown-region";

export type DeniedReason =
  | "no-consent"
  | "fully-revoked"
  | "outside-validity-window";

/** A signed statement cited as evidence for an authorization decision. */
export interface Evidence {
  readonly statementId: StatementId;
  readonly chainId: ChainId;
  readonly kind: StatementKind;
  readonly seq: number;
  readonly signature: string;
}

/** The single consent chain that authorizes an item end to end. */
export interface AuthorizationBasis {
  readonly chainId: ChainId;
  /** Grant/recovery statements that contribute the surviving coverage. */
  readonly grants: readonly Evidence[];
  /** Revocations that were applied while deriving the coverage. */
  readonly revocations: readonly Evidence[];
}

export interface BatchDecision {
  readonly itemId: string;
  readonly status: DecisionStatus;
  /** Present exactly when status === "deliverable". */
  readonly basis?: AuthorizationBasis;
  readonly reason?: PendingReason | DeniedReason;
}

/** Per-sender receipt: only results for items the sender itself handles. */
export interface SenderReceipt {
  readonly sender: SenderId;
  readonly results: readonly BatchDecision[];
}

export interface BatchEvaluation {
  /** The verifiable maximal deliverable subset (in input order). */
  readonly deliverable: readonly BatchDecision[];
  readonly pending: readonly BatchDecision[];
  readonly denied: readonly BatchDecision[];
  /** Every decision, keyed by input index. */
  readonly all: readonly BatchDecision[];
}

/** Point-in-time coverage query for a fully resolved subject. */
export interface CoverageQuery {
  readonly subject: SubjectId;
  readonly purpose: PurposeNodeId;
  readonly sender: SenderId;
  readonly region: RegionCode;
  readonly at: Timestamp;
}

export interface CoveringChain {
  readonly chainId: ChainId;
  readonly grants: readonly Evidence[];
  readonly revocations: readonly Evidence[];
}

/** Implemented by both the reference and the incremental interpreters. */
export interface AuthorizationReader {
  /** All chains that cover the query at the given instant, each on its own. */
  coveringChains(query: CoverageQuery): CoveringChain[];
  evaluateBatch(items: readonly BatchItem[], at: Timestamp): BatchEvaluation;
}
