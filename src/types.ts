export type SensitivityLevel =
  | "public"
  | "internal"
  | "sensitive"
  | "restricted";

export type LevelInput =
  | SensitivityLevel
  | "none"
  | "low"
  | "medium"
  | "high"
  | "confidential"
  | "private"
  | "protected"
  | "pii"
  | "secret"
  | "top-secret"
  | "topsecret"
  | "critical"
  | 0
  | 1
  | 2
  | 3;

export type MaskingStrategy =
  | "tokenize"
  | "partial"
  | "hash"
  | "mask"
  | "redact";

export type NestedMode = "leaves" | "subtree";

export interface FieldRuleObject {
  level: LevelInput;
  strategy?: MaskingStrategy;
  nestedMode?: NestedMode;
}

export type FieldRule = LevelInput | FieldRuleObject;

export interface FieldRuleEntry {
  pattern: string | RegExp;
  rule: FieldRule;
}

export interface DetectionOptions {
  /** Percent/URI encoding and form encoding. Default: true. */
  percent?: boolean;
  /** Base64 and base64url, padded or unpadded. Default: true. */
  base64?: boolean;
  /** Hexadecimal encoding. Default: true. */
  hex?: boolean;
  /** \uXXXX and \xNN escapes. Default: true. */
  unicodeEscapes?: boolean;
  /** HTML numeric and named entities. Default: true. */
  htmlEntities?: boolean;
  /** Unicode NFKC compatibility variants, e.g. full-width digits. Default: true. */
  normalized?: boolean;
  /** Reversed text. Default: false. */
  reversed?: boolean;
  /** ASCII case variants. Default: false. */
  caseInsensitive?: boolean;
  /** Values split by separators such as 138-0013-8000. Default: true. */
  separated?: boolean;
  /** Values split across object fields or array elements. Default: true. */
  splitAcrossFields?: boolean;
  /** Values shorter than this are only matched when they equal a whole string. Default: 4. */
  minLength?: number;
  /** Recursive decoding depth for encoded values. Default: 2. */
  maxDepth?: number;
}

export type AuditReason =
  | "field"
  | "subtree"
  | "content"
  | "split"
  | "key"
  | "known-value";

export interface AuditEntry {
  /** Opaque replacement token. */
  token: string;
  /** JSON Pointer into the original input. */
  path: string;
  /** Sensitivity level that caused the replacement. */
  level: SensitivityLevel;
  /** Why the value was replaced. */
  reason: AuditReason;
  /**
   * Exact value that was replaced. `restore()` uses this value, so an encoded
   * fragment is restored as the encoded fragment rather than re-encoded.
   */
  original: unknown;
  /** Raw sensitive value detected inside an encoded or concatenated fragment. */
  sensitiveValue?: string;
  /** Encoding path used to discover the leak, e.g. "base64" or "base64>url". */
  encoding?: string;
  /** Replacement strategy used for the token. */
  strategy: MaskingStrategy;
  /** Field name that triggered a field-level or inherited replacement. */
  field?: string;
  /** Milliseconds since epoch. */
  createdAt: number;
}

export interface DesensitizerOptions {
  /** Field rules by name, dotted path, glob, or regular expression. */
  fields?:
    | Record<string, FieldRule>
    | readonly FieldRuleEntry[]
    | ReadonlyMap<string, FieldRule>;
  /** Alias for `fields`, useful when wiring directly from policy config. */
  sensitiveFields?:
    | Record<string, FieldRule>
    | readonly FieldRuleEntry[]
    | ReadonlyMap<string, FieldRule>;
  /** Alias for `fields`. */
  fieldLevels?:
    | Record<string, FieldRule>
    | readonly FieldRuleEntry[]
    | ReadonlyMap<string, FieldRule>;
  /** Level for fields with no explicit rule or inferred sensitive name. Default: "public". */
  defaultLevel?: LevelInput;
  /** Infer levels for common names such as password, token, phone, email. Default: true. */
  inferSensitiveFields?: boolean;
  /** Replacement strategy per level. Default: tokenize for every non-public level. */
  levelStrategies?: Partial<Record<SensitivityLevel, MaskingStrategy>>;
  /** Default handling for a sensitive field whose value is an object or array. Default: "leaves". */
  nestedMode?: NestedMode;
  /** Variant detection configuration. */
  detection?: DetectionOptions;
  /** Extra values that must never appear verbatim or in an encoded form. */
  knownSensitiveValues?: Iterable<unknown>;
  /** Verify the serialized output and throw rather than returning a leak. Default: true. */
  verifyOutput?: boolean;
  /** Store original values in the audit vault so reveal/restore work. Default: true. */
  storeOriginal?: boolean;
  /** Override the random source, mainly useful for deterministic tests. */
  randomBytes?: (size: number) => Uint8Array;
}

export interface SerializeOptions {
  /** Indentation passed to JSON.stringify. */
  space?: number | string;
}

export interface Leak {
  value: string;
  encoding: string;
  index: number;
  location: "text" | "key";
  path?: string;
}
