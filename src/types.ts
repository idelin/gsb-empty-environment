/** JSON-compatible value tree. Object key order is insignificant; array order is significant. */
export type JsonValue = string | number | boolean | null | JsonValue[] | { [key: string]: JsonValue };

/** What to do when a rule matches a node. */
export type RuleAction = "pseudonymize" | "allow" | "redact";

export interface FieldRule {
  /**
   * Dot-separated path pattern. Segments:
   *  - `name`   object key
   *  - `*`      any single object key
   *  - `[]`     any array index (order preserved in concrete paths)
   * Example: "users[].contact.email"
   */
  pattern: string;
  action: RuleAction;
  /** Required when action === "pseudonymize". Isolates namespaces: same value in different domains gets different pseudonyms. */
  domain?: string;
}

/** Policy for leaf values matched by no rule. They are never passed through unchanged. */
export type UnauthorizedPolicy = "redact" | "error";

export type ErrorCode =
  | "COLLISION"
  | "UNAUTHORIZED_FIELD"
  | "INVALID_RULE"
  | "NON_JSON_VALUE";

export interface Issue {
  code: ErrorCode;
  /** Concrete path, e.g. "users[0].email". Never contains original values. */
  path: string;
  message: string;
}

export interface LogEntry {
  level: "info" | "warn" | "error";
  code: string;
  path?: string;
  domain?: string;
  pseudonym?: string;
  message: string;
}

export type Logger = (entry: LogEntry) => void;

export interface MappingEntry {
  domain: string;
  pseudonym: string;
  /** HMAC of the canonical original — lets you verify a candidate value without storing it. */
  valueHash: string;
  /** Original value, present only in the exported mapping (kept separate from redacted data). */
  value: JsonValue;
  /** Concrete paths where this pseudonym was applied. */
  paths: string[];
}

export interface MappingTable {
  version: 1;
  entries: MappingEntry[];
}

export interface Stats {
  pseudonymized: number;
  redacted: number;
  allowed: number;
}

export interface PseudonymizeResult {
  /** Redacted/pseudonymized tree. Contains no original values of matched or unauthorized fields. */
  data: JsonValue;
  issues: Issue[];
  stats: Stats;
}
