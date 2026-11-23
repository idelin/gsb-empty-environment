/**
 * Core data model for the deserialization admission package.
 *
 * A message carries a type descriptor graph (shared, possibly cyclic nodes).
 * A tenant policy is a finite regular tree grammar over the same node shape.
 * Everything is plain JSON-serializable data: no class instances ever flow
 * through the admission boundary, and the runtime never invokes constructors.
 */

/** Strong name identity of an assembly. Public key is mandatory. */
export interface AssemblyIdentity {
  /** Four-part exact .NET-style version, e.g. "1.2.3.4". */
  version: string;
  /** Hex/base64 public key token; two assemblies with equal simple name but
   *  different keys are different assemblies. */
  publicKey: string;
  /** Optional simple assembly name; when present it must also match. */
  name?: string;
}

/** Predicate over assembly identities used inside a normalized grammar. */
export interface AssemblyRef {
  name?: string;
  publicKey: string;
  /** Accepted versions; a single exact version is the common baseline case. */
  versions: VersionRange[];
}

/** Inclusive four-part version interval. Missing bounds are open. */
export interface VersionRange {
  min?: string;
  max?: string;
}

export type Variance = 'covariant' | 'contravariant' | 'invariant';

/**
 * A node of a type descriptor graph. Nodes are referenced by string ids so
 * the same node can be shared across several generic positions, possibly
 * under different constraints.
 */
export interface TypeNode {
  id: string;
  assembly: AssemblyIdentity;
  /** Metadata/type name, e.g. "System.Collections.Generic.List`1". */
  typeName: string;
  /** Generic arguments, in declaration order (pre-permutation). */
  args?: TypeRef[];
  /** Array rank > 0 means this node is an array of `element`. */
  arrayRank?: number;
  element?: TypeRef;
}

/** Reference to a node, or an inline node (desugared before checking). */
export type TypeRef = string | TypeNode;

/** A complete message payload: shared nodes plus the entry reference. */
export interface TypeGraph {
  root: TypeRef;
  nodes: Record<string, TypeNode>;
}

/* ---------- Regular tree grammar (the host's admitted language) ---------- */

export interface GrammarAlt {
  /** Constructor identity: assembly predicate + type name (array has a
   *  dedicated pseudo-constructor via `arrayRank`). */
  assembly: AssemblyRef;
  typeName: string;
  arrayRank?: number;
  /** One entry per generic parameter; each points at a nonterminal. */
  args?: GrammarArg[];
  /** For array alternatives, the element nonterminal. */
  element?: string;
}

export interface GrammarArg {
  /** Nonterminal defining the language accepted at this position. */
  nt: string;
  variance: Variance;
}

export interface GrammarProduction {
  nt: string;
  alts: GrammarAlt[];
}

/** A normalized, guarded finite regular tree grammar. */
export interface Grammar {
  /** Rule format / semantics version this grammar was compiled under. */
  ruleVersion: string;
  start: string;
  productions: GrammarProduction[];
}

/* ------------------------ Deployment policy tables ----------------------- */

/** Rename/remap rule for a single type of an old deployment. */
export interface TypeRename {
  oldAssembly: AssemblyIdentity;
  oldTypeName: string;
  newAssembly: AssemblyIdentity;
  newTypeName: string;
  /**
   * Position permutation: new generic position i maps to old position
   * paramPermutation[i]. Length must equal the arity. Identity by default.
   */
  paramPermutation?: number[];
  /**
   * Variance markers as declared by the new deployment. Must equal the
   * markers the old grammar relied on: silently widening (e.g. invariant
   * -> covariant across versions) is rejected as illegal.
   */
  variance?: Variance[];
  /** Versions the new deployment may emit. Must stay inside the old table's
   *  accepted versions; it can only narrow, never widen. */
  versionRange?: VersionRange;
}

export interface PolicyTable {
  ruleVersion: string;
  start: string;
  /** Baseline grammar productions (as admitted by past deployments). */
  productions: GrammarProduction[];
  /** Remapping configuration for the new deployment. */
  renames?: TypeRename[];
}

/* --------------------------- Inclusion verdict --------------------------- */

export type InclusionVerdict = 'subset' | 'widened' | 'unknown';

/** A concrete finite tree accepted by the new table but not by the old one. */
export interface WideningEvidence {
  /** Constructor path from the root, zero-based child positions. */
  path: number[];
  expected: string;
  found: string;
}

/**
 * Self-describing, independently checkable certificate. The `proof` contains
 * the full set of explored nonterminal pairs so a verifier never has to trust
 * the issuer's claim — it replays the same coinductive containment rules.
 */
export interface InclusionCertificate {
  format: 'type-admission/inclusion-cert/v1';
  ruleVersion: string;
  verdict: InclusionVerdict;
  budget: number;
  pairsUsed: number;
  /** Canonical JSON hashes of both normalized grammars. */
  oldGrammarHash: string;
  newGrammarHash: string;
  /** Every pair (newNt, oldNt) explored, with its per-alt witnesses. */
  proof: CertifiedPair[];
  counterexample?: WideningEvidence;
  issuedAt: string;
}

export interface CertifiedPair {
  newNt: string;
  oldNt: string;
  /** For each alternative of the new nonterminal, the alternative index of
   *  the old nonterminal that matches it, plus per-argument pair links. */
  matches: AltWitness[];
}

export interface AltWitness {
  newAltIndex: number;
  oldAltIndex: number;
  /** Pairs (newArgNt, oldArgNt) at each argument position, already present
   *  in the certificate's proof set. */
  argPairs: { newNt: string; oldNt: string }[];
}

/* ------------------------------ Runtime gate ----------------------------- */

export interface Rejection {
  /** First rejected location as a path of argument/element positions. */
  path: number[];
  nodeId: string;
  reason: string;
}

export interface AdmissionResult {
  admitted: boolean;
  rejection?: Rejection;
  /** Rule version actually used; always the certified one. */
  ruleVersion: string;
}
