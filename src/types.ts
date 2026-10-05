export type ModuleId = string;

export interface SymbolRef {
  module: ModuleId;
  name: string;
}

/** import { imported as local } from 'from' */
export interface ImportEdge {
  local: string;
  from: ModuleId;
  imported: string;
}

/** export { imported as exported } from 'from' */
export interface ReexportEdge {
  exported: string;
  from: ModuleId;
  imported: string;
}

/**
 * A module: named local definitions, explicit named imports, named re-exports.
 * Specs are treated as immutable values; replace them wholesale on update.
 */
export interface ModuleSpec {
  definitions: readonly string[];
  imports: readonly ImportEdge[];
  reexports: readonly ReexportEdge[];
}

export interface ChainEdge {
  from: SymbolRef;
  via: 'import' | 'reexport';
  to: SymbolRef;
}

/** A source chain: hops from the query site to a definition site. */
export interface SourceChain {
  edges: readonly ChainEdge[];
  target: SymbolRef;
}

export type Resolution =
  | { kind: 'defined'; target: SymbolRef; chain: SourceChain }
  | { kind: 'undefined' }
  | { kind: 'conflict'; chains: readonly [SourceChain, SourceChain] }
  | { kind: 'cyclic-reexport'; cycle: readonly ChainEdge[] };
