export interface AliasMap {
  [alias: string]: string;
}

export interface BundlerOptions {
  aliases?: AliasMap;
  extensions?: string[];
  caseSensitive?: boolean;
  resolveIndexFiles?: boolean;
}

export interface ModuleEntry {
  path: string;
  renameCost?: number;
}

export interface ReferenceEntry {
  from: string;
  specifier: string;
}

export interface BundlerInput {
  modules: Array<string | ModuleEntry>;
  references?: ReferenceEntry[];
  options?: BundlerOptions;
}

export interface ConflictGroup {
  key: string;
  modules: string[];
}

export interface RenameAction {
  from: string;
  to: string;
  cost: number;
}

export type ReferenceStatus = "ok" | "updated" | "ambiguous" | "unresolved" | "external";

export interface ReferenceReport {
  from: string;
  newFrom: string;
  specifier: string;
  newSpecifier: string | null;
  resolvesTo: string | null;
  candidates: string[];
  status: ReferenceStatus;
}

export interface CanonicalKeyEntry {
  module: string;
  key: string;
}

export interface AnalysisReport {
  ok: boolean;
  canonicalKeys: CanonicalKeyEntry[];
  duplicates: string[][];
  conflicts: ConflictGroup[];
  renames: RenameAction[];
  totalCost: number;
  references: ReferenceReport[];
}
