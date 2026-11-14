import type { DuplicatePolicy, JsonValue } from "./canonical.ts";
import type {
  NormalizedJsonDocument,
  NormalizedRequest,
  NormalizeOptions,
} from "./normalize.ts";

export type AdmissionHeaders =
  | Record<string, string | string[] | undefined>
  | { get(name: string): string | null };

export interface SignaturePolicy {
  /** Header name. Defaults to `x-json-signature`. */
  header?: string;
  /** HMAC secret. Without a secret the signature is plain SHA-256. */
  secret?: string | Uint8Array;
  /** Deny when the header is absent. */
  required?: boolean;
  /** Compare against this value instead of reading a header. */
  expected?: string;
}

export interface GateOptions extends NormalizeOptions {
  /** Defaults to `first`/`last` folding; `reject` denies every duplicate. */
  duplicatePolicy?: DuplicatePolicy;
  signature?: SignaturePolicy | false;
  allowedRoles?: readonly string[];
  requiredAuthKeys?: readonly string[];
}

export interface DuplicateRange {
  index: number;
  keyRange: { start: number; end: number };
  valueRange: { start: number; end: number };
}

export interface AdmissionReason {
  code: string;
  message: string;
  key?: string;
  path?: string;
  offset?: number;
  occurrences?: DuplicateRange[];
}

export interface LayerDecision {
  layer: "waf" | "router";
  decision: "allow" | "deny";
  allow: boolean;
  reasons: readonly AdmissionReason[];
  normalized: NormalizedRequest | null;
  document: NormalizedJsonDocument | null;
  auth: Readonly<Record<string, JsonValue>>;
  digest: string | null;
}

export interface AdmissionDecision {
  ok: boolean;
  allow: boolean;
  decision: "allow" | "deny";
  reasons: readonly AdmissionReason[];
  normalized: NormalizedRequest | null;
  digest: string | null;
  waf: LayerDecision;
  router: LayerDecision;
  consistent: boolean;
}
