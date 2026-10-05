export type {
  ChainStep,
  ConflictBranch,
  Diagnostic,
  DiagnosticKind,
  ImportSpec,
  ModuleSpec,
  ReexportSpec,
  Resolution,
  SymbolOrigin,
} from './types.ts';
export { computeResolution, formatChain, canonicalChain, type CacheEntry } from './resolver.ts';
export { Project, Snapshot, type SnapshotStats } from './project.ts';
export { resolveReference } from './reference.ts';
export { validateSnapshot, canonicalResolution, type Mismatch } from './validate.ts';
