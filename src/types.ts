export type JsonPrimitive = string | number | boolean | null;
export type JsonValue = JsonPrimitive | JsonObject | JsonArray;
export interface JsonObject {
  [key: string]: JsonValue;
}
export type JsonArray = JsonValue[];

export type PathSegment = string | number;

export type FieldAction = "pseudonymize" | "redact" | "drop";

export interface FieldRule {
  path: string;
  domain: string;
  action?: FieldAction;
}

export type UnknownFieldPolicy = "error" | "redact";

export interface AnonymizeOptions {
  rules: FieldRule[];
  key: string | Uint8Array;
  pseudonymLength?: number;
  onUnknownField?: UnknownFieldPolicy;
}

export type ErrorCode =
  | "UNAUTHORIZED_FIELD"
  | "COLLISION"
  | "INVALID_RULE";

export interface AnonymizeError {
  code: ErrorCode;
  path: string;
  message: string;
}

export interface MappingEntry {
  pseudonym: string;
  valueDigest: string;
  occurrences: string[];
}

export interface DomainMapping {
  domain: string;
  entries: MappingEntry[];
}

export interface MappingExport {
  version: 1;
  domains: DomainMapping[];
  collisions: AnonymizeError[];
}

export interface AnonymizeResult {
  data: JsonValue;
  mapping: MappingExport;
  errors: AnonymizeError[];
  stats: {
    pseudonymized: number;
    redacted: number;
    dropped: number;
    collisions: number;
  };
}
