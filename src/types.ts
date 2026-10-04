/** Shared types for the config-merge library. */

export type SourceKind = 'defaults' | 'file' | 'env' | 'cli';

/** Identifies where a value came from. */
export interface SourceRef {
  kind: SourceKind;
  /** File path, env var name, CLI flag, or a logical name like "defaults". */
  name: string;
  /** Optional human-readable location, e.g. "config.json:12:5" or "argv[3]". */
  location?: string;
}

export type IssueCode =
  | 'duplicate-key'
  | 'null-value'
  | 'type-conversion'
  | 'type-mismatch'
  | 'invalid-list-op';

export interface MergeIssue {
  severity: 'error' | 'warning';
  code: IssueCode;
  /** Dotted config path, e.g. "server.port" or "tags[2]". */
  path: string;
  message: string;
  /** Where the offending value originated, when known. */
  source?: SourceRef;
}

export type FieldType =
  | 'string'
  | 'number'
  | 'boolean'
  | 'string[]'
  | 'number[]'
  | 'object'
  | 'any';

export interface FieldSchema {
  type: FieldType;
  /** Secret fields are masked on display but still comparable. */
  secret?: boolean;
  /** Allow explicit null for this field (otherwise null is reported). */
  nullable?: boolean;
}

/** Dotted-path -> field schema. */
export type ConfigSchema = Record<string, FieldSchema>;

export function formatSource(ref: SourceRef): string {
  return ref.location ? `${ref.name} (${ref.location})` : ref.name;
}
