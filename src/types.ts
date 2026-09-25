export type Resource = string;
export type Action = string;
export type Principal = string;

/**
 * Only explicitly supplied attributes are visible to policies.
 * Values are restricted to finite primitives and arrays of them.
 */
export type AttributeValue = string | number | boolean | ReadonlyArray<string | number | boolean>;
export type Attributes = Readonly<Record<string, AttributeValue>>;

export type IntegerOp = "==" | "!=" | "<" | "<=" | ">" | ">=";

export type Policy =
  | { kind: "allowAll" }
  | { kind: "denyAll" }
  | { kind: "attrIn"; attr: string; values: ReadonlyArray<string | number | boolean> }
  | { kind: "intCmp"; attr: string; op: IntegerOp; value: number }
  | { kind: "not"; operand: Policy }
  | { kind: "and"; operands: ReadonlyArray<Policy> }
  | { kind: "or"; operands: ReadonlyArray<Policy> };

export interface Grant {
  id: string;
  /** null marks a self-signed root grant, the only permitted power source. */
  parentId: string | null;
  issuer: Principal;
  subject: Principal;
  resources: ReadonlyArray<Resource>;
  actions: ReadonlyArray<Action>;
  /** Seconds of additional delegation depth below this grant. */
  maxDepth: number;
  /** When true the subject may use the grant but must not redelegate. */
  sealed: boolean;
  notBefore: number;
  expiresAt: number;
  policy: Policy;
  parentSig: string | null;
}

export interface SignedGrant {
  grant: Grant;
  sig: string;
}

export interface AuthzRequest {
  grantId: string;
  subject: Principal;
  resource: Resource;
  action: Action;
  attrs: Attributes;
  at: number;
}

export interface ProofLink {
  grant: Grant;
  sig: string;
}

export interface Proof {
  leafId: string;
  subject: Principal;
  resource: Resource;
  action: Action;
  attrs: Attributes;
  at: number;
  /** Root grant first, leaf grant last. */
  chain: ReadonlyArray<ProofLink>;
}

export type CheckCode =
  | "malformed"
  | "subject"
  | "anchor"
  | "cycle"
  | "linkage"
  | "unknownKey"
  | "sig"
  | "time"
  | "revoked"
  | "nesting"
  | "depth"
  | "sealed"
  | "requestScope"
  | "policy";

export interface CheckFailure {
  code: CheckCode;
  grantId: string | null;
  detail: string;
}

export interface VerifyResult {
  allowed: boolean;
  reasons: ReadonlyArray<CheckFailure>;
  proof: Proof;
}
