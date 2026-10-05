/**
 * Data model for the module system. Only three constructs are supported:
 * named exports (local definitions), explicit imports, and re-exports.
 *
 * Module ids are opaque strings; `from` fields must reference a module id
 * directly (specifier resolution / file loading is out of scope).
 */

export interface ImportSpec {
  /** Name as exported by the source module: `import { name } from 'from'`. */
  readonly name: string;
  /** Source module id. */
  readonly from: string;
}

export interface ReexportSpec {
  /** Name as exported by the source module. */
  readonly name: string;
  /** Source module id. */
  readonly from: string;
  /** Exported name of this module (`export { name as as } from 'from'`). Defaults to `name`. */
  readonly as?: string;
}

export interface ModuleSpec {
  /** Locally defined named exports: `export const x ...` etc. */
  readonly definitions?: readonly string[];
  /** Explicit imports (references that must resolve). */
  readonly imports?: readonly ImportSpec[];
  /** Re-exports: `export { a } from 'm'` / `export { a as b } from 'm'`. */
  readonly reexports?: readonly ReexportSpec[];
}

/** One step of a provenance chain: a (module, exported name) pair. */
export interface ChainStep {
  readonly module: string;
  readonly name: string;
}

/** The defining site of a symbol. */
export interface SymbolOrigin {
  readonly module: string;
  readonly name: string;
}

export interface ConflictBranch {
  readonly origin: SymbolOrigin;
  readonly chain: readonly ChainStep[];
}

export type Resolution =
  | { readonly status: 'resolved'; readonly origin: SymbolOrigin; readonly chain: readonly ChainStep[] }
  | { readonly status: 'unresolved' }
  | { readonly status: 'circular'; readonly cycle: readonly ChainStep[] }
  | { readonly status: 'conflict'; readonly definitions: readonly [ConflictBranch, ConflictBranch] };

export type DiagnosticKind =
  | 'unresolved-import'
  | 'unresolved-reexport'
  | 'circular-reexport'
  | 'conflicting-export';

export interface Diagnostic {
  readonly module: string;
  readonly kind: DiagnosticKind;
  /** Local name at the reference site (imported name / exported name). */
  readonly name: string;
  /** Target module for import / re-export diagnostics. */
  readonly from?: string;
  readonly message: string;
  readonly resolution: Resolution;
}
