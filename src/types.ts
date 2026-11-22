/**
 * Public domain types for the effective-policy resolver.
 *
 * All signatures cover canonical JSON (recursively sorted object keys).  The
 * resolver never treats a path alone as an asset identity: a waiver can only
 * match a resource when the signed issuer scope contains the exact
 * (tenant root, immutable resource id, path version) tuple.
 */

export type Instant = number | string | Date;
/** Finite constraint values are compared by set membership, not coercion. */
export type ConstraintValue = string | number | boolean;

export interface TimeInterval {
  /** Inclusive by default. */
  start?: Instant;
  /** Exclusive by default. */
  end?: Instant;
  startInclusive?: boolean;
  endInclusive?: boolean;
}

export type PathSegment = string;

/**
 * A deterministic finite automaton over path segments.
 *
 * Missing transitions reject.  A transition keyed by "*" is a catch-all for
 * segments that have no exact transition.  `alphabet` lists the concrete
 * segment classes used for automata products and finite enumeration.
 */
export interface PathDFA {
  states: readonly string[];
  start: string;
  accepting: readonly string[];
  transitions: Readonly<Record<string, Readonly<Record<string, string>>>>;
  alphabet?: readonly string[];
}

export interface TenantRootClaims {
  kind: "tenant-root";
  tenantId: string;
  rootId: string;
  publicKey: string;
  issuedAt: number;
  expiresAt?: number;
}

export interface SignedTenantRoot extends TenantRootClaims {
  signature: string;
}

export interface TenantRootIssuer {
  root: SignedTenantRoot;
  privateKey: string;
}

export interface SourceClaims {
  kind: "policy-source";
  id: string;
  signerId: string;
  publicKey: string;
  issuedAt: number;
  expiresAt?: number;
  description?: string;
}

/** A source identity that a waiver can name as its cancellation target. */
export interface SignedPolicySource extends SourceClaims {
  signature: string;
}

export interface ResourceIdentity {
  tenantRoot: SignedTenantRoot;
  /** Immutable for the life of the resource. */
  resourceId: string;
  /** Path at this identity version. */
  path: readonly PathSegment[];
  /** Changes whenever the path (or another identity-bearing property) changes. */
  pathVersion: number | string;
}

export interface AssetRootClaims {
  kind: "asset-set-root";
  tenantId: string;
  tenantRootId: string;
  setId: string;
  membersRoot: string;
  issuedAt: number;
  expiresAt?: number;
}

/**
 * A signed commitment to a finite asset set.  Membership is proved against
 * `membersRoot`; the commitment itself is signed by the tenant root key.
 */
export interface SignedAssetRoot extends AssetRootClaims {
  signature: string;
}

export interface MerkleSibling {
  hash: string;
  side: "left" | "right";
}

export interface MembershipProof {
  setId: string;
  /** Hash of the exact resource identity. */
  assetHash: string;
  leaf: string;
  siblings: readonly MerkleSibling[];
  root: string;
}

export interface AssetSetScope {
  /** Extra path-language restriction evaluated before membership. */
  path: PathDFA;
  assets: SignedAssetRoot;
  /** Optional issuer-supplied proofs. Request-level proofs are also accepted. */
  proofs?: readonly MembershipProof[];
}

/**
 * A source scope.  A path-only scope is useful for baselines that intentionally
 * apply uniformly; an asset-set scope additionally requires an inclusion proof.
 */
export interface ResourceScope {
  path: PathDFA;
  assets?: SignedAssetRoot;
  proofs?: readonly MembershipProof[];
}

export interface ConstraintDeclaration {
  /** Stable within its signed source. */
  id: string;
  key: string;
  /** Finite allowed set for this constraint.  Intersection is set intersection. */
  allowed: readonly ConstraintValue[];
  /** Human-readable source text.  It is evidence, not identity. */
  literal?: string;
  source: SignedPolicySource;
  valid?: TimeInterval;
  scope?: ResourceScope;
}

export interface PolicyNode {
  id: string;
  declarations?: readonly ConstraintDeclaration[];
  /** Parent nodes inherited by this node. */
  parents?: readonly string[];
  scope?: ResourceScope;
  kind?: string;
}

export interface InheritanceEdge {
  /** The parent that supplies constraints. */
  from: string;
  /** The child that inherits them. */
  to: string;
}

export interface InheritanceGraph {
  nodes: readonly PolicyNode[];
  edges?: readonly InheritanceEdge[];
}

export interface WaiverTarget {
  /** Preferred signed-source identifier. */
  sourceId?: string;
  /** Alias accepted by the resolver. */
  originId?: string;
  /** Preferred constraint identifier. */
  constraintId?: string;
  /** Alias accepted by the resolver. */
  declarationId?: string;
  /**
   * Optional key selector.  It is only accepted when it identifies exactly one
   * declaration from the named source.
   */
  key?: string;
}

export interface WaiverClaims {
  kind: "waiver";
  id: string;
  issuer: SignedPolicySource;
  target: WaiverTarget;
  /** The asset set and path language promised at signing time. */
  scope: AssetSetScope;
  issuedAt: number;
  expiresAt: number;
}

export interface SignedWaiver extends WaiverClaims {
  signature: string;
}

export interface PolicyState {
  graph: InheritanceGraph;
  waivers?: readonly SignedWaiver[];
  at: Instant;
}

export interface TrustOptions {
  /** If present, a tenant root public key must have one of these fingerprints. */
  trustedRootFingerprints?: readonly string[];
  /** If present, a policy source public key must have one of these fingerprints. */
  trustedSourceFingerprints?: readonly string[];
  /** Require the root to be present in trustedRootFingerprints. */
  requireTrustedRoots?: boolean;
  /** Require policy sources to be present in trustedSourceFingerprints. */
  requireTrustedSources?: boolean;
}

export interface ResolutionRequest {
  state: PolicyState;
  /** Graph node representing the target resource, baseline, or policy unit. */
  target: string;
  identity: ResourceIdentity;
  membershipProofs?: readonly MembershipProof[];
  trust?: TrustOptions;
  /** Bound for independently verifiable simple provenance paths. */
  maxProvenanceChains?: number;
}

export type UnknownReasonCode =
  | "missing-membership-proof"
  | "invalid-membership-proof"
  | "invalid-tenant-root"
  | "untrusted-tenant-root"
  | "expired-tenant-root"
  | "invalid-source-signature"
  | "untrusted-source"
  | "expired-source"
  | "invalid-waiver-signature"
  | "invalid-asset-root"
  | "expired-asset-root"
  | "ambiguous-waiver-target"
  | "conflicting-constraint-origin";

export interface UnknownReason {
  code: UnknownReasonCode;
  message: string;
  sourceId?: string;
  waiverId?: string;
  nodeId?: string;
  setId?: string;
}

export interface ProvenanceChain {
  sourceId: string;
  /** Binds the provenance chain to the signed key, not just a display id. */
  sourceKeyFingerprint: string;
  declarationId: string;
  /** Starts at the declaring node and ends at the target node. */
  nodeIds: readonly string[];
  edges: readonly InheritanceEdge[];
}

export interface ConstraintOrigin {
  sourceId: string;
  sourceKeyFingerprint: string;
  declarationId: string;
  nodeId: string;
  literal?: string;
  allowed: readonly ConstraintValue[];
  /** Every simple inheritance path that supplies this origin. */
  chains: readonly ProvenanceChain[];
}

export interface WaiverApplication {
  waiverId: string;
  sourceId: string;
  declarationId: string;
  issuerId: string;
}

export interface EffectiveConstraint {
  key: string;
  status: "active" | "conflict";
  allowed: readonly ConstraintValue[];
  origins: readonly ConstraintOrigin[];
  waived: readonly WaiverApplication[];
}

export type ResolutionStatus =
  | "resolved"
  | "conflict"
  | "unknown";

export interface EffectivePolicy {
  status: ResolutionStatus;
  target: string;
  identityKey: string;
  constraints: Readonly<Record<string, EffectiveConstraint>>;
  unknown: readonly UnknownReason[];
  waived: readonly WaiverApplication[];
  /** Diagnostics that do not make a policy unknown. */
  diagnostics: readonly string[];
}

export type ChangeClassification =
  | "tightened"
  | "loosened"
  | "unchanged"
  | "mixed"
  | "unknown"
  | "conflict";

export interface ConstraintDelta {
  key: string;
  before: EffectiveConstraint | null;
  after: EffectiveConstraint | null;
  classification: ChangeClassification;
  /**
   * strict = after ⊂ before, permissive = after ⊃ before, same, incomparable,
   * added, removed, unknown, or conflict.
   */
  relation:
    | "strict"
    | "permissive"
    | "same"
    | "incomparable"
    | "added"
    | "removed"
    | "unknown"
    | "conflict";
}

export interface ChangeAssetContext {
  identity: ResourceIdentity;
  target?: string;
  membershipProofs?: readonly MembershipProof[];
}

export interface ChangeAsset {
  id: string;
  before: ChangeAssetContext;
  after: ChangeAssetContext;
}

export interface ChangePlan {
  before: PolicyState;
  after: PolicyState;
  /** Used when a context does not name its own target. */
  target?: string;
  assets: readonly ChangeAsset[];
  trust?: TrustOptions;
  maxProvenanceChains?: number;
}

export interface ValidatedProvenanceChain extends ProvenanceChain {
  side: "before" | "after";
  assetId: string;
  valid: true;
}

export interface ChangeRegion {
  id: string;
  classification: ChangeClassification;
  /** Disjoint symbolic region for this classification. */
  path: PathDFA;
  /** True when every unknown reason in this region is a missing proof. */
  unknownOnlyForProof: boolean;
  assetIds: readonly string[];
  /** Concrete paths in this region, useful for exhaustive small-graph checks. */
  paths: readonly (readonly PathSegment[])[];
  deltas: readonly ConstraintDelta[];
  unknown: readonly UnknownReason[];
}

export interface ChangeAnalysis {
  regions: readonly ChangeRegion[];
  disjoint: boolean;
  chains: readonly ValidatedProvenanceChain[];
  before: readonly EffectivePolicy[];
  after: readonly EffectivePolicy[];
}
