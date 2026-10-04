export interface ModuleInput {
  path: string;
  /** 改名代价；缺省为 1。 */
  renameCost?: number;
}

export interface ReferenceInput {
  /** 引用所在模块路径（ importer ）。 */
  from: string;
  /** import/require 说明符，如 "./foo"、"@app/foo"。 */
  specifier: string;
}

export interface AnalyzerInput {
  modules: ModuleInput[];
  references?: ReferenceInput[];
  /** 别名映射，支持末尾 "/*" 通配，如 { "@app/*": "src/*" }。 */
  aliases?: Record<string, string>;
  /** 可省略的扩展名列表（含点），缺省见 DEFAULT_EXTENSIONS。 */
  extensions?: string[];
}

export interface ConflictGroup {
  /** 规范化键（大小写折叠 + 扩展名省略 + 别名展开后的路径）。 */
  key: string;
  /** 组内模块的规范化实际路径。 */
  modules: string[];
}

export interface RenameAction {
  from: string;
  to: string;
  cost: number;
}

export interface ReferenceUpdate {
  from: string;
  oldSpecifier: string;
  newSpecifier: string;
  resolvesTo: string;
}

export interface BadReference {
  from: string;
  specifier: string;
  reason: "unresolved" | "ambiguous";
  candidates?: string[];
}

export interface AnalysisResult {
  conflicts: ConflictGroup[];
  renames: RenameAction[];
  totalCost: number;
  updatedReferences: ReferenceUpdate[];
  badReferences: BadReference[];
}
