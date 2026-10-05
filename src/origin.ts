export type SourceKind = 'defaults' | 'file' | 'env' | 'argv';

export interface SourceLocation {
  line: number;
  column: number;
}

export interface SourceRef {
  kind: SourceKind;
  /** File path, env var name, argv token, or "defaults". */
  name: string;
  /** Position inside the source, when known (JSON files). */
  location?: SourceLocation;
}

/** Priority order: later sources override earlier ones. */
export const SOURCE_PRIORITY: Record<SourceKind, number> = {
  defaults: 0,
  file: 1,
  env: 2,
  argv: 3,
};

export type ListOp = 'set' | 'append' | 'remove';

/** One flattened key/value produced by a source loader. */
export interface SourceEntry {
  path: string[];
  /** Raw value as produced by the source (strings for env/argv). */
  value: unknown;
  op: ListOp;
  source: SourceRef;
}
