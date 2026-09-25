/** Kind of an export: 'decl' is a side-effect-free declaration reference,
 *  'value' is produced by module evaluation (init). */
export type ExportKind = 'decl' | 'value';

export interface ExportDecl {
  readonly kind: ExportKind;
  /** Signature string, e.g. "(number) => string". Checked against importer expectations. */
  readonly signature: string;
}

/** Explicit named import: each imported name maps to the signature the importer expects. */
export interface ImportDecl {
  readonly from: string;
  readonly version: string;
  readonly names: Readonly<Record<string, string>>;
}

/** Namespaces handed to init(): module name -> (imported name -> value). */
export type ImportNamespaces = Record<string, Record<string, unknown>>;

export interface ModuleSpec {
  readonly name: string;
  readonly version: string;
  readonly exports: Readonly<Record<string, ExportDecl>>;
  readonly imports: readonly ImportDecl[];
  /** Values for 'decl'-kind exports. Available immediately, no evaluation side effects. */
  readonly declare?: Readonly<Record<string, unknown>>;
  /** Produces values for 'value'-kind exports. Runs at link time in dependency order. */
  readonly init?: (imports: ImportNamespaces) => Record<string, unknown>;
}

/** Full dependency version vector: module name -> resolved version. */
export type VersionVector = Record<string, string>;

/** Canonical cache key for a version vector. */
export function vectorKey(vector: VersionVector): string {
  return Object.keys(vector)
    .sort()
    .map((n) => `${n}@${vector[n]}`)
    .join('|');
}
