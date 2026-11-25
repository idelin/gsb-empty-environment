/**
 * Public type surface of the coverage-acceptance verifier.
 *
 * A bundle is a byte container with a public manifest and several replaceable
 * regions. Each signature record attests one or more declared attributes over
 * a half-open byte range, bound to a payload schema, canonicalization
 * version, package identity and public-key material.
 */

/** Half-open byte interval [start, end) over the raw package bytes. */
export interface ByteRange {
  start: number;
  end: number;
}

/**
 * Host-supplied canonicalization description.
 *
 * `foldNewlines` collapses CRLF (and lone CR) bytes to LF; `stripPadding`
 * removes bytes declared as removable padding. The host owns both transforms
 * and supplies the ordered reverse mapping, so the verifier never guesses how
 * canonical coordinates return to raw byte intervals.
 */
export interface CanonicalSpec {
  version: string;
  foldNewlines: boolean;
  stripPadding: boolean;
  /** Original raw bytes covered by this canonicalization scope. */
  rawRange: ByteRange;
  /** Ordered, canonical-position-sorted reverse mapping. */
  reverseMap: CanonicalMapEntry[];
}

/**
 * Ordered reverse mapping entry: a canonical coordinate range maps back to
 * one original byte interval. Entries for one mapping are ordered by
 * canonical position and must not overlap there.
 */
export interface CanonicalMapEntry extends ByteRange {
  /** Inclusive start in canonical coordinates. */
  canonicalStart: number;
  /** Exclusive end in canonical coordinates. */
  canonicalEnd: number;
}

/**
 * A replaceable region of the bundle. `aliasOf` lets a second region path
 * reference the same raw bytes as another region (duplicate path alias); the
 * `scope` keeps commitments over aliased bytes distinct.
 */
export interface Region {
  path: string;
  range: ByteRange;
  scope: string;
  aliasOf?: string;
}

/** Length field whose bytes influence the interpretation of some region. */
export interface LengthField {
  id: string;
  range: ByteRange;
  /** Region whose interpretation depends on this length value. */
  regionPath: string;
}

/** One required/mandatory attribute that must be witnessed. */
export interface AttributeRequirement {
  attribute: string;
  /** Raw encoding ranges that must all be covered by one signature. */
  encodingRanges: ByteRange[];
  /** Length fields influencing interpretation; same-signature coverage. */
  lengthFieldIds: string[];
  /** Distinct actual public-key materials required (0 = no threshold). */
  distinctKeyMaterials: number;
}

/** Management-domain mutual-exclusion policy. */
export interface ExclusionRule {
  domainA: string;
  domainB: string;
}

/** Public manifest of the distribution bundle. */
export interface Manifest {
  packageId: string;
  canonicalizations: Record<string, CanonicalSpec>;
  regions: Region[];
  lengthFields: LengthField[];
  requirements: AttributeRequirement[];
  /** Payload schema id -> canonicalization id it is bound to. */
  schemaBindings: Record<string, string>;
  /** Management domains that may never co-occur in one witness. */
  exclusionRules: ExclusionRule[];
}

/**
 * A single signature record. The record binds payload schema, canonical
 * version, package identity, half-open byte coverage and declared attributes,
 * plus the key material / certificate and management domain.
 */
export interface SignatureRecord {
  index: number;
  algorithm: string;
  /** Public-key material identity: certificates sharing material are one key. */
  keyMaterialId: string;
  /** Optional certificate identity; two certificates may share one key. */
  certificateId?: string;
  domain: string;
  packageId: string;
  payloadSchema: string;
  canonicalVersion: string;
  scope: string;
  coveredRange: ByteRange;
  declaredAttributes: string[];
  /** Opaque signature blob interpreted by the registered crypto backend. */
  signatureBytes: Uint8Array;
}

export interface DistributionBundle {
  manifest: Manifest;
  records: SignatureRecord[];
  rawBytes: Uint8Array;
}

/** Structured diagnostic. The three required defect classes stay distinct. */
export type DiagnosticCode =
  | "UNKNOWN_ALGORITHM"
  | "UNBOUND_SCHEMA"
  | "AMBIGUOUS_REVERSE_MAP"
  | "PACKAGE_ID_MISMATCH"
  | "CANONICAL_VERSION_MISMATCH"
  | "CANONICAL_SCOPE_MISMATCH"
  | "INVALID_RANGE"
  | "CRYPTO_FAILURE";

export interface Diagnostic {
  code: DiagnosticCode;
  message: string;
  recordIndex?: number;
  canonicalId?: string;
}

/** A signature that survived structural checks and passed verification. */
export interface VerifiedSignature {
  index: number;
  keyMaterialId: string;
  domain: string;
  scope: string;
  coveredRange: ByteRange;
  declaredAttributes: string[];
}

export interface AttributeWitness {
  attribute: string;
  /** Minimum-size global witness; record indices ascending. */
  recordIndices: number[];
  verificationCalls: number;
  distinctKeyMaterials: number;
  satisfied: boolean;
  /** Why no witness exists, when satisfied is false. */
  reason?: string;
}

export interface EvaluationReport {
  diagnostics: Diagnostic[];
  verified: VerifiedSignature[];
  witnesses: AttributeWitness[];
  /** Distinct records actually sent to the crypto backend. */
  totalVerificationCalls: number;
}

/** Backend the host plugs in for the actual signature math. */
export interface CryptoBackend {
  knownAlgorithms: ReadonlySet<string>;
  verify(record: SignatureRecord, bundle: DistributionBundle): boolean;
}
