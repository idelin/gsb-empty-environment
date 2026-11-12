/**
 * Type definitions for OpenID Federation 1.0 trust chain validation.
 *
 * A trust chain is an ordered list of compact JWTs:
 *
 *   [leaf Entity Configuration,
 *    Subordinate Statement (issued by the leaf's superior),
 *    ... more Subordinate Statements ...,
 *    Trust Anchor Entity Configuration (self-signed)]
 */

/** A JSON Web Key as defined by RFC 7517. */
export interface Jwk {
  kty: string;
  kid?: string;
  alg?: string;
  use?: string;
  [key: string]: unknown;
}

/** A set of JSON Web Keys (RFC 7517, section 5). */
export interface Jwks {
  keys: Jwk[];
}

/**
 * The set of claims a relying party needs from a signed federation
 * statement. Extra claims are allowed and preserved.
 */
export interface FederationClaims {
  /** Issuer identifier (an entity identifier URL). */
  iss: string;
  /** Subject identifier (an entity identifier URL). */
  sub: string;
  /** Issued-at time, seconds since the Unix epoch. */
  iat?: number;
  /** Expiration time, seconds since the Unix epoch. */
  exp: number;
  /**
   * Present on Entity Configuration statements (`iss === sub`). Holds the
   * entity's own signing keys.
   */
  jwks?: Jwks;
  /**
   * Present on Subordinate Statements. Holds the public keys of the
   * subordinate entity named by `sub`.
   */
  source_endpoint?: string;
  /**
   * Maximum number of Subordinate Statements that may appear below the
   * issuer of this statement. Only meaningful on Subordinate Statements.
   */
  max_path_length?: number;
  /**
   * Metadata policy operators keyed by metadata type (e.g.
   * `openid_relying_party`) and claim name.
   */
  metadata_policy?: MetadataPolicy;
  [claim: string]: unknown;
}

/**
 * A metadata policy document:
 * `{ [metadataType]: { [claimName]: { [operator]: value } } }`.
 */
export type MetadataPolicy = Record<
  string,
  Record<string, PolicyOperators>
>;

/** The policy operators recognised by this package. */
export interface PolicyOperators {
  subset_of?: unknown[];
  one_of?: unknown[];
  value?: unknown;
  add?: unknown[];
  default?: unknown;
  [operator: string]: unknown;
}

/** A compact-serialized JWT (`header.payload.signature`). */
export type CompactJwt = string;

/** Context handed to an injected signature verifier. */
export interface SignatureContext {
  /** The compact JWT that must be verified. */
  jwt: CompactJwt;
  /** The index of the statement inside the trust chain (0 = leaf). */
  index: number;
  /** The parsed header of the JWT. */
  header: JwsHeader;
  /** The parsed payload of the JWT. */
  payload: FederationClaims;
  /**
   * The keys the statement must verify against. For a self-signed Entity
   * Configuration these come from its own `jwks` claim; for a Subordinate
   * Statement they come from the issuing superior's Entity Configuration.
   */
  jwks: Jwks;
}

/** The protected header of a compact JWS. */
export interface JwsHeader {
  alg: string;
  kid?: string;
  typ?: string;
  [key: string]: unknown;
}

/**
 * Signature verification is deliberately injected so that relying parties
 * can plug in their own crypto (HSM, KMS, a JOSE library, ...). The
 * implementation MUST resolve a key from `context.jwks`, verify the JWS,
 * and throw (or return a rejected promise) when the signature is invalid.
 */
export type SignatureVerifier = (
  context: SignatureContext,
) => void | Promise<void>;

/** Options controlling {@link verifyTrustChain}. */
export interface VerifyTrustChainOptions {
  /** Injected signature verifier. Required. */
  verifier: SignatureVerifier;
  /**
   * Trust anchor entity identifier. When supplied, the final statement's
   * `sub`/`iss` must equal it.
   */
  trustAnchor?: string;
  /**
   * Evaluation time in milliseconds since the Unix epoch. Defaults to
   * `Date.now()`. Injected so tests can freeze time.
   */
  now?: number;
  /**
   * Allowed clock skew in seconds when checking `iat`/`exp`. Defaults to 0.
   */
  clockSkewSeconds?: number;
  /**
   * Expected leaf entity identifier. When supplied, the first statement's
   * `sub` must equal it.
   */
  leafEntityId?: string;
}

/** The successfully resolved result of trust chain validation. */
export interface TrustChainResult {
  /** The verified leaf entity identifier (first statement's `sub`). */
  leaf: string;
  /** The verified trust anchor entity identifier. */
  trustAnchor: string;
  /**
   * The effective expiration of the whole chain: the minimum `exp` across
   * every statement, in seconds since the Unix epoch.
   */
  exp: number;
  /** The parsed statements in chain order (leaf first, anchor last). */
  statements: FederationClaims[];
  /**
   * The merged metadata policy after applying every Subordinate Statement,
   * from the trust anchor down toward the leaf.
   */
  combinedMetadataPolicy: MetadataPolicy;
  /** Number of Subordinate Statements in the chain. */
  pathLength: number;
}

/** Stable error codes returned by {@link TrustChainError}. */
export type TrustChainErrorCode =
  | 'EMPTY_CHAIN'
  | 'MALFORMED_JWT'
  | 'NOT_ENTITY_CONFIGURATION'
  | 'NOT_SUBORDINATE_STATEMENT'
  | 'SELF_SIGNED_MISMATCH'
  | 'BROKEN_CHAIN'
  | 'MISSING_JWKS'
  | 'EXPIRED'
  | 'NOT_YET_VALID'
  | 'PATH_TOO_LONG'
  | 'POLICY_LOOSENED'
  | 'TRUST_ANCHOR_MISMATCH'
  | 'LEAF_MISMATCH'
  | 'INVALID_SIGNATURE';
