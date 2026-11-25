/**
 * Public type model for the deserialization admission package.
 *
 * Everything crossing the gateway boundary is plain JSON-serializable data.
 * No value of any type described here is ever instantiated by this package:
 * the runtime admission path performs structural matching only.
 */

export const RULE_VERSION = "admission-rules/1.0.0" as const;

export type Variance = "in" | "out" | "invariant";

/** A type-description graph node carried by a queued message. */
export type TypeNode =
  | {
      kind: "named";
      /** Stable node id so the same node can be shared by several positions. */
      id: string;
      /** Public key token identifying the assembly (not a cryptographic key object). */
      assemblyPublicKey: string;
      /** Exact, fully qualified assembly version, e.g. "1.2.3". */
      assemblyVersion: string;
      typeName: string;
      /** Generic argument nodes in declared order. */
      args: TypeNode[];
    }
  | {
      kind: "array";
      id: string;
      /** Array rank: 1 == vector, 2 == matrix, ... */
      rank: number;
      element: TypeNode;
    }
  | {
      kind: "ref";
      /** Target node id. A back-edge only counts as guarded when the cycle
       *  traverses at least one structural constructor (named/array). */
      target: string;
    };

export interface TypeGraph {
  root: string;
  nodes: Record<string, TypeNode>;
}

/**
 * Descriptor of an allowed constructor as known to a binding-table release.
 * Arrays are modeled as synthetic constructors named `array:<rank>`.
 */
export interface ConstructorDescriptor {
  assemblyPublicKey: string;
  assemblyVersion: string;
  typeName: string;
  /** Generic arity; arrays are modeled with arity 1. */
  arity: number;
  /** Slot variance in declared parameter order. */
  variances: Variance[];
  /** True for the synthetic array constructors. */
  array?: boolean;
  arrayRank?: number;
}

/** One binding-table release. Two releases coexist for the delayed queue. */
export interface BindingTable {
  tableId: string;
  rulesVersion: string;
  constructors: ConstructorDescriptor[];
}

export interface GrammarDocument {
  tenant: string;
  start: string;
  nonterminals: Record<
    string,
    Array<{
      constructor: ConstructorDescriptor;
      children: string[];
    }>
  >;
}

/** A production after canonicalization, referencing canonical nonterminal ids. */
export interface CanonicalProduction {
  ctorId: string;
  children: string[];
}

export interface CanonicalGrammar {
  tenant: string;
  start: string;
  /** Canonical nonterminal id -> productions in deterministic order. */
  nonterminals: Map<string, CanonicalProduction[]>;
  /** ctorId -> descriptor, shared across nonterminals. */
  catalog: Map<string, ConstructorDescriptor>;
  digest: string;
}

export interface VersionRange {
  /** Exact allowed versions; "*" means any version, still checked against
   *  the old table so old permits can never be widened. */
  exact?: string[];
  any?: boolean;
  /** Inclusive bounds on numeric version tuples; absent bound = open. */
  min?: number[];
  max?: number[];
}

export interface RenameRule {
  from: { assemblyPublicKey: string; typeName: string };
  to: { assemblyPublicKey: string; typeName: string };
}

export interface ParamPermutation {
  ctor: { assemblyPublicKey: string; typeName: string };
  /** newPosition -> oldPosition, must be a permutation of [0..arity). */
  mapping: number[];
}

export interface VarianceMark {
  ctor: { assemblyPublicKey: string; typeName: string };
  slot: number;
  variance: Variance;
}

export interface VersionRangeRule {
  ctor: { assemblyPublicKey: string; typeName: string };
  range: VersionRange;
}

export interface DeploymentConfig {
  rulesVersion: string;
  renames: RenameRule[];
  permutations: ParamPermutation[];
  varianceMarks: VarianceMark[];
  versionRanges: VersionRangeRule[];
  budget: number;
}

export type Verdict = "subset" | "widened" | "insufficient";

export interface InclusionObligation {
  left: string;
  right: string;
  direction: "forward" | "backward";
}

export interface WideningWitness {
  kind:
    | "unmatched-production"
    | "version-uncovered"
    | "variance-relaxed"
    | "empty-language";
  ctorId?: string;
  nonterminal?: string;
  detail: string;
}

export interface InclusionCertificate {
  rulesVersion: string;
  verdict: Verdict;
  tenant: string;
  leftTableId: string;
  rightTableId: string;
  leftGrammarDigest: string;
  rightGrammarDigest: string;
  startPair: InclusionObligation;
  proof: InclusionObligation[];
  witnesses: WideningWitness[];
  budget: number;
  spent: number;
  issuedAt: string;
  digest: string;
}

export type RejectionKind =
  | "unknown-node"
  | "dangling-ref"
  | "bare-alias-cycle"
  | "unknown-constructor"
  | "version-not-permitted"
  | "arity-mismatch"
  | "no-matching-production";

export interface RejectionLocation {
  /** Edge path from the graph root, e.g. ["args[0]", "element"]. */
  path: string[];
  nodeId: string;
  kind: RejectionKind;
  detail: string;
}

export interface AdmissionResult {
  admitted: boolean;
  /** First rejected type position; undefined when admitted. */
  rejection?: RejectionLocation;
  rulesVersion: string;
  grammarDigest: string;
  tableId: string;
}
