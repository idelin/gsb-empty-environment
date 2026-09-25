/** Explicit attribute values a policy may read: strings (from finite sets) or integers. */
export type AttributeValue = string | number;
export type Attributes = Readonly<Record<string, AttributeValue>>;

/**
 * Policy condition tree. Only three kinds of atoms exist:
 *  - membership of an attribute in a finite set
 *  - integer comparisons on an attribute
 * combined with boolean and/or/not. Nothing here can call out to an
 * external identity (or any other) service: evaluation is pure.
 */
export type Condition =
  | { readonly op: 'in'; readonly attr: string; readonly set: readonly AttributeValue[] }
  | { readonly op: 'eq' | 'lt' | 'lte' | 'gt' | 'gte'; readonly attr: string; readonly value: number }
  | { readonly op: 'and' | 'or'; readonly conditions: readonly Condition[] }
  | { readonly op: 'not'; readonly condition: Condition };

/** A signed delegation token. `signature` covers every other field. */
export interface Delegation {
  readonly id: string;
  readonly issuer: string;
  readonly subject: string;
  readonly resources: readonly string[];
  readonly actions: readonly string[];
  readonly policy: Condition | null;
  /** Remaining delegation depth. 0 means "cannot be delegated further". */
  readonly maxDepth: number;
  /** Explicit non-delegatable marker, independent of depth. */
  readonly delegatable: boolean;
  readonly notBefore: number;
  readonly expiresAt: number;
  readonly parentId: string | null;
  readonly signature: string;
}

export interface LinkCheck {
  readonly name: string;
  readonly ok: boolean;
  readonly detail: string;
}

export interface ProofLink {
  readonly tokenId: string;
  readonly issuer: string;
  readonly subject: string;
  readonly resources: readonly string[];
  readonly actions: readonly string[];
  readonly notBefore: number;
  readonly expiresAt: number;
  readonly checks: readonly LinkCheck[];
}

export interface PolicyTraceEntry {
  readonly tokenId: string;
  readonly ok: boolean;
  /** Names of the explicit attributes the policy actually read. */
  readonly attributesRead: readonly string[];
}

/** Full chain proof, root first, leaf last. JSON-serializable. */
export interface ChainProof {
  readonly tokenId: string;
  readonly verifiedAt: number;
  readonly valid: boolean;
  readonly failure: string | null;
  readonly chain: readonly ProofLink[];
  readonly policyTrace: readonly PolicyTraceEntry[];
}
