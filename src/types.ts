/**
 * Merge acceptance package — public domain types.
 *
 * The model is built around immutable node ids. A signature is anchored to a
 * node in an *original* parsed tree and covers that node together with its
 * original descendants only. Renames, moves, alias folding and template
 * expansion never enlarge that scope and never mint a location grant.
 */

export type NodeId = string;
export type IssuerId = string;
export type SigId = string;
export type DocId = string;
export type TemplateId = string;
export type BranchId = string;

/** Scalar / array leaf values. Plain objects are represented as object nodes. */
export type LeafValue = string | number | boolean | readonly (string | number | boolean)[];

export type Facet = 'name' | 'value' | 'parent' | 'existence';
export const FACETS: readonly Facet[] = ['name', 'value', 'parent', 'existence'] as const;

/** The three distinct terminal states a key can have during merge. */
export type KeyStateKind = 'value' | 'null' | 'tombstone' | 'missing';

export interface Attribution {
  issuer: IssuerId;
  sigId?: SigId;
}

export interface Edge {
  key: string;
  child: NodeId;
  attribution?: Attribution;
}

interface BaseNode {
  id: NodeId;
  attribution?: Attribution;
}

export interface ObjectNode extends BaseNode {
  kind: 'object';
  edges: Edge[];
}

export interface ValueNode extends BaseNode {
  kind: 'value';
  value: LeafValue;
}

/** Explicit JSON null: the key exists and its value is null. */
export interface NullNode extends BaseNode {
  kind: 'null';
}

/** Durable deletion marker: blocks lower-priority inheritance. */
export interface TombstoneNode extends BaseNode {
  kind: 'tombstone';
  reason?: string;
}

/** Extension / template reference. Expansion never grants authority. */
export interface RefNode extends BaseNode {
  kind: 'ref';
  template: TemplateId;
  role?: string;
  tenant?: string;
  version?: string;
}

export type Node = ObjectNode | ValueNode | NullNode | TombstoneNode | RefNode;
export type NodeKind = Node['kind'];

/** A parsed finite tree with immutable node ids. */
export interface TreeDoc {
  docId: DocId;
  root: NodeId;
  nodes: Record<NodeId, Node>;
}

export interface Signature {
  sigId: SigId;
  issuer: IssuerId;
  /** Original parsed document the signed node belongs to. */
  docId: DocId;
  nodeId: NodeId;
  /** Inline revocation convenience; a revocation list is also accepted. */
  revoked?: boolean;
}

/**
 * A location/purpose grant. `path` is a pointer pattern:
 *  - `/a/b`      exact pointer
 *  - `/a/ *`     one segment wildcard (written without the space)
 *  - `/a/**`     any descendant prefix
 *  - `/**`       everything
 */
export interface Grant {
  issuer: IssuerId;
  path: string;
  facets?: Facet[] | '*';
  tenant?: string;
  role?: string | string[];
  note?: string;
}

/** Protect a pointer from cross-scope tombstones. */
export interface ProtectedKeyRule {
  path: string;
  /** Security scope / owner allowed to delete within scope. */
  owner: IssuerId;
  /** Explicit cross-scope deleters; `'*'` allows every authorised issuer. */
  allowDeleteFrom?: IssuerId[] | '*';
}

export interface AliasRule {
  /** Alias / legacy key name this rule folds. */
  alias: string;
  /** Canonical / output key name. */
  canonical: string;
  /** Pointer pattern of the parent object where this alias applies. */
  at?: string;
  attribution: Attribution;
}

export interface Condition {
  /** All label values must match the evaluation / site labels. */
  labels?: Record<string, string>;
}

export interface Branch {
  branchId: BranchId;
  /** Higher number = higher precedence. */
  priority: number;
  doc: TreeDoc;
  /** Fallback attribution for nodes/edges without their own. */
  attribution?: Attribution;
  when?: Condition;
  labels?: Record<string, string>;
}

/**
 * A contribution retraction. Unlike a tombstone, this removes a named signed
 * contribution so that recomputation can expose another branch value
 * ("delete then re-inherit"). The retraction itself is an existence decision.
 */
export interface DeleteDirective {
  id?: string;
  issuer: IssuerId;
  sigId?: SigId;
  /** Pointer of the key whose contribution is retracted. */
  path: string;
  /** Restrict retraction to one branch; otherwise retract the incumbent source. */
  branchId?: BranchId;
  /** Retract only a source node id. */
  nodeId?: NodeId;
}

export interface Revocation {
  sigId: SigId;
  at?: string;
  reason?: string;
}

export interface AcceptOptions {
  /** Default true: content facets must present a scope-covering signature. */
  requireSignatureEvidence?: boolean;
  /** Include inactive/skipped branches in the trace. Default true. */
  recordTrace?: boolean;
  /** Publication id seed; otherwise deterministic from canonical output. */
  publicationSeed?: string;
}

export interface MergeInput {
  branches: Branch[];
  /**
   * Original/source parsed trees referenced by signatures. These anchor scopes
   * and original locations but are NOT themselves merged or published.
   */
  documents?: TreeDoc[];
  /** Shared template documents keyed by template id. */
  templates?: Record<TemplateId, TreeDoc>;
  signatures?: Signature[];
  revocations?: Revocation[] | SigId[];
  grants?: Grant[];
  aliases?: AliasRule[];
  protectedKeys?: ProtectedKeyRule[];
  deletions?: DeleteDirective[];
  /** Global evaluation labels (tenant/role/env...). */
  labels?: Record<string, string>;
  options?: AcceptOptions;
}

export type ErrorCode =
  | 'INVALID_INPUT'
  | 'DUPLICATE_NODE_ID'
  | 'NODE_ID_CONTENT_CONFLICT'
  | 'UNKNOWN_TEMPLATE'
  | 'TEMPLATE_CYCLE'
  | 'ALIAS_COLLISION'
  | 'SIGNATURE_NOT_FOUND'
  | 'SIGNATURE_REVOKED'
  | 'SIGNATURE_ISSUER_MISMATCH'
  | 'SIGNATURE_SCOPE'
  | 'UNAUTHORIZED_FACET'
  | 'CROSS_SCOPE_TOMBSTONE'
  | 'PROTECTED_KEY'
  | 'UNKNOWN_NODE'
  | 'INVALID_CONDITION'
  | 'INTERNAL';

export interface ErrorLocation {
  /** JSON Pointer from the published root, e.g. `/db/host`. Root is `""`. */
  pointer: string;
  nodeId?: NodeId;
  facet?: Facet;
}

export interface AcceptanceError {
  code: ErrorCode;
  message: string;
  location: ErrorLocation;
  /** Stable machine details used by the truth interpreter / assertions. */
  detail?: Record<string, string | number | boolean | string[]>;
}

export interface Evidence {
  sigId: SigId;
  originNodeId: NodeId;
  valid: boolean;
  violations: string[];
}

export interface FacetDecision {
  facet: Facet;
  /** Issuer whose location/purpose grant is evaluated. */
  issuer: IssuerId;
  sigId?: SigId;
  originNodeId?: NodeId;
  branchId?: BranchId;
  via: string[];
  evidence: Evidence[];
  authorized: boolean;
  violations: AcceptanceError[];
}

export interface ExposureRecord {
  pointer: string;
  deletedBranchId?: BranchId;
  deletedIssuer?: IssuerId;
  deletedSigId?: SigId;
  deletedSignatureRevoked: boolean;
  exposedBranchId?: BranchId;
  exposedIssuer?: IssuerId;
  exposedSigId?: SigId;
  exposedSignatureRevoked: boolean;
}

export interface LeafProvenance {
  pointer: string;
  key: string;
  value: unknown;
  kind: 'value' | 'null';
  name: FacetDecision;
  valueDecision: FacetDecision;
  parent: FacetDecision;
  existence: FacetDecision;
  exposures: ExposureRecord[];
  authorized: boolean;
}

export interface PublishedLeaf {
  pointer: string;
  key: string;
  value: LeafValue | null;
  provenance: LeafProvenance;
}

export interface BranchTraceEntry {
  branchId: BranchId;
  priority: number;
  active: boolean;
  reason?: string;
}

export interface MergeTrace {
  branches: BranchTraceEntry[];
  expansions: { pointer: string; template: TemplateId; instanceRoot: NodeId }[];
  aliasApplications: { pointer: string; from: string; to: string; ruleIssuer: IssuerId }[];
  exposures: ExposureRecord[];
}

export interface Publication {
  ok: true;
  publicationId: string;
  /** Plain, JSON-serialisable output tree (sorted keys). */
  tree: Record<string, unknown>;
  leaves: PublishedLeaf[];
  provenance: LeafProvenance[];
  trace: MergeTrace;
}

export interface Rejection {
  ok: false;
  errors: AcceptanceError[];
}

export type AcceptanceResult = Publication | Rejection;

/** Host sink that only receives a publication after full acceptance. */
export interface PublishHost {
  publish(publication: Publication): void | Promise<void>;
}
