/**
 * Public types for the address-bar display decision package.
 *
 * All scalar values are Unicode scalar values (0..0x10FFFF excluding the
 * surrogate range).  Labels are already IDNA-validated by the host.
 */

export type Scalar = number;
export type SkeletonSymbol = number;

export interface ConfusableRule {
  /** One to three input scalars consumed by this rule. */
  readonly consumes: readonly Scalar[];
  /** Zero to three skeleton symbols emitted by this rule. */
  readonly skeleton: readonly SkeletonSymbol[];
}

export interface ConfusableTable {
  readonly version: string;
  /**
   * Declaration order is significant: for equal consume lengths the first
   * declared rule wins.
   */
  readonly rules: readonly ConfusableRule[];
  readonly meta?: Readonly<Record<string, unknown>>;
}

export interface ScriptRange {
  readonly start: Scalar;
  readonly end: Scalar;
  readonly script: string;
  /**
   * Unicode Script_Extensions.  When present this is the authoritative set of
   * scripts for the scalar; otherwise the primary `script` is used.
   */
  readonly extensions?: readonly string[];
}

export interface ScriptTable {
  readonly version: string;
  readonly ranges: readonly ScriptRange[];
  /** Scripts that are neutral for policy purposes. */
  readonly commonScripts?: readonly string[];
  /** Script assumed for scalars not covered by any range. */
  readonly defaultScript?: string;
  readonly meta?: Readonly<Record<string, unknown>>;
}

export interface ScriptPolicy {
  /** Scripts permitted by the current suffix's policy. */
  readonly allowed: readonly string[];
  /**
   * When true (the default) all non-neutral scalars must admit a common
   * script choice.  Script_Extensions make each scalar's choice set explicit.
   */
  readonly singleScript?: boolean;
  readonly disallowed?: readonly string[];
  readonly minScripts?: number;
  readonly maxScripts?: number;
  /** Policy result for a label containing only neutral scalars. */
  readonly allowNeutralOnly?: boolean;
}

export type SuffixRuleKind = "normal" | "wildcard" | "exception";

export interface SuffixRule {
  /**
   * Canonical suffix text.  The `*` and `!` markers accepted by the public
   * suffix list may also be embedded in this value (for example `*.ck` or
   * `!www.ck`); `kind` is then inferred when omitted.
   */
  readonly suffix: string;
  readonly kind?: SuffixRuleKind;
  readonly policy?: ScriptPolicy;
}

export interface SuffixTable {
  readonly version: string;
  readonly rules: readonly SuffixRule[];
  readonly defaultPolicy?: ScriptPolicy;
  readonly meta?: Readonly<Record<string, unknown>>;
}

export interface DfaEdge {
  readonly from: number;
  readonly to: number;
  /** Inclusive Unicode scalar range accepted by this edge. */
  readonly start: Scalar;
  readonly end: Scalar;
}

/**
 * A deterministic finite automaton over Unicode scalars.  The automaton is
 * the host's finite description of candidate alternative domains; the decision
 * code searches it symbolically instead of enumerating a name table.
 */
export interface ScalarAutomaton {
  readonly initial: number;
  readonly accepting: readonly number[];
  readonly edges: readonly DfaEdge[];
  readonly stateCount?: number;
}

export interface LabelInputObject {
  readonly text: string;
  /** UTF-16 offset in `sourceText`, when supplied. */
  readonly start?: number;
  /** UTF-16 end offset; derived from `text` when omitted. */
  readonly end?: number;
}

export type LabelInput = string | LabelInputObject;

export type DomainInput =
  | string
  | { readonly labels: readonly string[] };

export interface ScalarSpan {
  readonly codePoint: Scalar;
  readonly utf16Start: number;
  readonly utf16End: number;
}

export interface ResolvedLabel {
  readonly index: number;
  readonly text: string;
  readonly start: number;
  readonly end: number;
  readonly scalars: readonly Scalar[];
}

export type SignatureEncoding = "hex" | "base64";

export interface SignedBrandAlias {
  /** Domain in the same textual form as the automaton's accepted words. */
  readonly domain: string;
  readonly brand: string;
  /** Public suffix this alias is scoped to, or `*`. */
  readonly suffix: string;
  readonly keyId: string;
  readonly signature: string;
  readonly alg?: string;
  readonly signatureEncoding?: SignatureEncoding;
  /** Restrict this alias proof to one confusable table version. */
  readonly tableVersion?: string;
  /** Restrict this alias proof to one suffix table version. */
  readonly suffixVersion?: string;
  readonly notBefore?: number;
  readonly notAfter?: number;
  /**
   * Set by a trusted host that has already verified the signature.  This is an
   * escape hatch for asynchronous platform crypto; it must never be taken from
   * untrusted input.
   */
  readonly verified?: boolean;
  readonly meta?: Readonly<Record<string, unknown>>;
}

export type AliasVerifier = (alias: SignedBrandAlias) => boolean;

export type AliasKeyRing = Readonly<
  Record<string, string | Uint8Array>
>;

export interface VersionedTables {
  /** Keyed by confusable/script table version. */
  readonly confusables: Readonly<Record<string, ConfusableTable>>;
  readonly scripts: Readonly<Record<string, ScriptTable>>;
  /** Keyed by suffix table version (updated independently). */
  readonly suffixes: Readonly<Record<string, SuffixTable>>;
}

export interface DisplayRequest {
  /** Labels of the address currently shown by the host. */
  readonly labels: readonly LabelInput[];
  /** Original host text, used to resolve and verify UTF-16 spans. */
  readonly sourceText?: string;
  readonly baseOffset?: number;
  /** Brand's protected registered domain; defaults to `labels`. */
  readonly protectedDomain?: DomainInput;
  readonly brand: string;
  /**
   * Confusable/script versions to try, newest first.  Defaults to the keys of
   * `tables.confusables`.
   */
  readonly versionOrder?: readonly string[];
  /**
   * When true, a failed proof in one version may fall through to the next
   * version in `versionOrder`.  The default is false: the selected version
   * fails closed to ASCII, keeping old bookmarks and newly seen domains
   * distinct.
   */
  readonly versionFallback?: boolean;
  readonly suffixVersion: string;
  /** Optional per-table-version suffix snapshot selection. */
  readonly suffixVersionByTableVersion?: Readonly<Record<string, string>>;
  readonly tables: VersionedTables;
  readonly aliases?: readonly SignedBrandAlias[];
  /** Candidate alternative domains.  Omitted means no alternatives. */
  readonly automaton?: ScalarAutomaton;
  /**
   * Treat only candidates whose labels satisfy the selected suffix's script
   * policy as "allowed domains" for the alias-proof condition.  Default true;
   * set false when the automaton is already the complete allowed set and its
   * labels intentionally bypass suffix script policy.
   */
  readonly filterCandidatesByScriptPolicy?: boolean;
  /** Hard cap from the address-bar policy; values above 64 are clamped. */
  readonly maxCandidateScalars?: number;
  readonly maxCandidateStates?: number;
  readonly now?: number;
  readonly verifier?: AliasVerifier;
  readonly keyring?: AliasKeyRing;
  readonly aliasKeyEncoding?: "utf8" | "hex" | "base64";
  /** Policy used only when neither the suffix rule nor table supplies one. */
  readonly defaultScriptPolicy?: ScriptPolicy;
}

export interface CandidateWitness {
  readonly domain: string;
  readonly scalars: readonly Scalar[];
  readonly skeleton: readonly SkeletonSymbol[];
  readonly tableVersion: string;
}

export interface CandidateProof {
  readonly ok: boolean;
  readonly truncated: boolean;
  readonly targetSkeleton: readonly SkeletonSymbol[];
  readonly provenDomains: readonly string[];
  readonly acceptedSameSkeleton: number;
  readonly provenSameSkeleton: number;
  readonly witness: CandidateWitness | null;
  readonly exploredStates: number;
  readonly reason: string;
}

export type DisplayMode = "unicode" | "ascii";

export type DecisionReason =
  | "proved"
  | "script-policy"
  | "no-proof"
  | "search-truncated"
  | "ascii-input";

export interface LabelDecision {
  readonly index: number;
  readonly label: string;
  readonly mode: DisplayMode;
  readonly displayText: string;
  readonly asciiText: string;
  readonly version: string;
  readonly span: { readonly start: number; readonly end: number };
  readonly scalars: readonly ScalarSpan[];
  readonly scriptPolicyHolds: boolean;
  readonly reason: DecisionReason;
}

export interface AllowedRange {
  readonly unicodeLabels: readonly number[];
  readonly asciiLabels: readonly number[];
  /** Digest of the complete allowed/fallback range. */
  readonly digest: string;
}

export interface VersionAttempt {
  readonly version: string;
  readonly suffixVersion: string;
  readonly proof: CandidateProof;
  readonly scriptPolicyHolds: readonly boolean[];
  readonly ok: boolean;
  readonly reason: string;
  readonly tableDigests: Readonly<Record<string, string>>;
}

export interface DisplayDecision {
  readonly labels: readonly LabelDecision[];
  readonly text: string;
  /** Version whose proof was selected (or the first attempted version). */
  readonly version: string;
  readonly chosenAttempt: number;
  /** The single timestamp used for alias time-window checks. */
  readonly now: number;
  readonly attempts: readonly VersionAttempt[];
  readonly suffix: {
    readonly value: string;
    readonly version: string;
    readonly labels: readonly string[];
    readonly policy: ScriptPolicy | undefined;
  };
  readonly tableDigests: Readonly<Record<string, string>>;
  readonly allowedRange: AllowedRange;
  readonly proof: CandidateProof;
}

export interface TableSummary {
  readonly confusables: Readonly<
    Record<string, { version: string; digest: string; ruleCount: number }>
  >;
  readonly scripts: Readonly<
    Record<string, { version: string; digest: string; rangeCount: number }>
  >;
  readonly suffixes: Readonly<
    Record<string, { version: string; digest: string; ruleCount: number }>
  >;
  readonly combinedDigest: string;
}

export interface VerificationReport {
  readonly ok: boolean;
  readonly errors: readonly string[];
  readonly checkedLabels: number;
  readonly checkedTables: number;
}

export interface ExhaustiveCheckOptions {
  readonly alphabet?: readonly Scalar[];
  readonly maxWordLength?: number;
  readonly maxChunkings?: number;
  readonly maxWords?: number;
}

export interface CheckResult {
  readonly name: string;
  readonly ok: boolean;
  readonly checked: number;
  readonly failures: readonly string[];
}

export interface ExhaustiveReport {
  readonly ok: boolean;
  readonly checks: readonly CheckResult[];
}
