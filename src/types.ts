/**
 * Core type model for the offline credential-host context resolver.
 *
 * Design invariants:
 *  - Every context object is referenced exclusively by a content identifier
 *    (`cid`) derived from its canonical bytes. URLs never appear inside
 *    `@import`; they may only exist as informational manifest metadata.
 *  - Every diagnostic object is fully populated: optional fields use explicit
 *    empty-string / empty-array values rather than `undefined`.
 */

/** Content identifier, e.g. "sha256:<base16>". */
export type ContentId = string;

export const MAX_IMPORT_DEPTH = 4;

export const CONTENT_ID_ALGORITHM = "sha256";

/** JSON-LD keywords handled by this resolver. */
export type JsonLdKeyword =
  | "@base"
  | "@vocab"
  | "@import"
  | "@propagate"
  | "@protected"
  | "@type"
  | "@id"
  | "@reverse"
  | "@nest"
  | "@container";

/** A context entry: either an IRI string or an expanded term definition. */
export type TermDefinition = string | ExpandedTermDefinition;

export interface ExpandedTermDefinition {
  "@id"?: string;
  "@type"?: string;
  "@reverse"?: string;
  "@container"?: string | string[];
  "@nest"?: string;
  "@protected"?: boolean;
  "@propagate"?: boolean;
  [term: string]: unknown;
}

/**
 * A context object as stored in the offline content store. Index signature is
 * intentionally permissive; the engine validates every unknown member.
 */
export interface ContextObject {
  "@version"?: 1.1;
  "@base"?: string | null;
  "@vocab"?: string | null;
  "@import"?: ContentId | readonly ContentId[];
  "@propagate"?: boolean;
  "@protected"?: boolean;
  [term: string]: unknown;
}

/** The two coercions this package is required to enforce. */
export type ForcedType = "@id" | "@vocab";

/** A term binding produced by resolution, fully materialised and validated. */
export interface ResolvedTerm {
  readonly term: string;
  readonly iri: string;
  readonly reverse: boolean;
  readonly forcedType: ForcedType | null;
  readonly container: readonly string[];
  readonly protectedFlag: boolean;
  readonly nestedUnder: readonly string[];
  /** Content ids that contributed this binding, innermost first. */
  readonly provenance: readonly ContentId[];
}

/** Snapshot of the active contextual state, used for `@propagate: false`. */
export interface ActiveStateSnapshot {
  readonly vocab: string | null;
  readonly base: string | null;
  readonly termBindings: ReadonlyMap<string, ResolvedTerm>;
  readonly protectedTerms: ReadonlyMap<string, ProtectedRegistration>;
  readonly nestScopes: ReadonlyMap<string, ResolvedTerm[]>;
}

/** Registration record kept for every protected binding. */
export interface ProtectedRegistration {
  readonly term: string;
  readonly iri: string;
  readonly reverse: boolean;
  readonly forcedType: ForcedType | null;
  readonly container: readonly string[];
  readonly definingCid: ContentId;
  /** Canonical fingerprint of the definition; identical cids repeat safely. */
  readonly definitionFingerprint: string;
}

/** One entry in the resolved context stack (root first). */
export interface ContextStackFrame {
  readonly cid: ContentId;
  readonly depth: number;
  readonly propagate: boolean;
}

/** Result of resolving a root context against a verified store. */
export interface ResolutionResult {
  readonly rootCid: ContentId;
  readonly stack: readonly ContextStackFrame[];
  readonly terms: ReadonlyMap<string, ResolvedTerm>;
  readonly protectedTerms: ReadonlyMap<string, ProtectedRegistration>;
  readonly vocab: string | null;
  readonly base: string | null;
  /** Minimal sufficient source closure: every cid actually consulted. */
  readonly closure: readonly ContentId[];
}

/** Stable, machine-readable error codes. */
export type ErrorCode =
  | "TRUST_MANIFEST_SIGNATURE_INVALID"
  | "TRUST_MANIFEST_MALFORMED"
  | "TRUST_MANIFEST_ROOT_MISMATCH"
  | "CONTENT_NOT_IN_TRUST_LIST"
  | "CONTENT_DIGEST_MISMATCH"
  | "CONTENT_MALFORMED"
  | "IMPORT_DEPTH_EXCEEDED"
  | "IMPORT_CYCLE_DETECTED"
  | "IMPORT_TARGET_MALFORMED"
  | "PROTECTED_TERM_REDEFINED"
  | "PROTECTED_ALIAS_COLLISION"
  | "PROTECTED_REVERSE_COLLISION"
  | "UNKNOWN_KEYWORD_REWRITE"
  | "UNSUPPORTED_FORCED_TYPE"
  | "NEST_ALIAS_CONFLICT"
  | "NEST_SCOPE_CONFLICT"
  | "PROPAGATE_SCOPE_VIOLATION"
  | "CACHE_KEY_UNSOUND"
  | "CACHE_REUSE_DENIED"
  | "CACHE_CLOSURE_INSUFFICIENT";

/**
 * Complete diagnostic contract. Every field is always present: callers can
 * serialise the object without ever observing `undefined`.
 */
export interface ContextDiagnostic {
  readonly code: ErrorCode;
  readonly message: string;
  readonly offendingTerm: string;
  readonly offendingCid: string;
  readonly importPath: readonly ContentId[];
  readonly depth: number;
  readonly detail: string;
}

/** Error thrown for every hard rejection; carries a complete diagnostic. */
export class ContextResolutionError extends Error {
  readonly diagnostic: ContextDiagnostic;

  constructor(diagnostic: ContextDiagnostic) {
    super(diagnostic.message);
    this.name = "ContextResolutionError";
    this.diagnostic = diagnostic;
  }
}

/** Signed trust manifest root. */
export interface TrustManifest {
  readonly manifestVersion: 1;
  readonly rootKeyId: string;
  /** Base64url Ed25519 SPKI public key (the trust anchor). */
  readonly rootPublicKeySpki: string;
  /** Allowed context objects, mapped by their content id. */
  readonly allowedContexts: readonly AllowedContextEntry[];
  /** Algorithm used to derive content ids. */
  readonly contentAlgorithm: typeof CONTENT_ID_ALGORITHM;
  /** ISO-8601 creation timestamp; informational only. */
  readonly issuedAt: string;
}

export interface AllowedContextEntry {
  readonly cid: ContentId;
  /** Hex digest kept in the manifest for cross-checking store content. */
  readonly digestHex: string;
  /** Optional non-authoritative label; never used for trust decisions. */
  readonly label: string;
}

/** Envelope pairing the canonical manifest bytes with an Ed25519 signature. */
export interface SignedManifestEnvelope {
  /** Deterministically canonical JSON of the TrustManifest. */
  readonly manifestCanonical: string;
  /** Base64url Ed25519 signature over the UTF-8 canonical bytes. */
  readonly signature: string;
}

/** Offline, read-only map of content id -> raw context object. */
export interface ContentStore {
  has(cid: ContentId): boolean;
  get(cid: ContentId): ContextObject | undefined;
  /** Canonical bytes used when deriving the content id. */
  canonicalBytes(cid: ContentId): Uint8Array | undefined;
  ids(): readonly ContentId[];
}
