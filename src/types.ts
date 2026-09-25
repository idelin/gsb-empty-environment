/** 导出种类：
 * - "decl"：纯声明，无求值副作用，允许参与环依赖；
 * - "value"：需要求值的绑定（有初始化副作用），禁止成环。
 */
export type ExportKind = "decl" | "value";

/** 求值上下文：工厂函数通过它按显式名称解析本地导出与导入绑定。 */
export interface ResolveContext {
  readonly module: string;
  readonly version: string;
  get(localName: string): unknown;
}

export interface ExportDecl {
  name: string;
  kind: ExportKind;
  /** 签名字符串，导入方可声明期望签名，链接期校验。 */
  signature: string;
  /** kind === "decl" 时的声明负载（不求值）。 */
  decl?: unknown;
  /** kind === "value" 时的求值工厂。 */
  make?: (ctx: ResolveContext) => unknown;
}

export interface ImportName {
  /** 导出模块中的名称。 */
  name: string;
  /** 本地别名（用于同名遮蔽场景）。 */
  as?: string;
  /** 期望签名；提供时必须与导出方一致。 */
  signature?: string;
}

export interface ImportDecl {
  module: string;
  /** 精确版本，必须完全匹配。 */
  version: string;
  names: ImportName[];
  /** 导入语句位置，用于缺失导出等错误的定位。 */
  loc: string;
}

export interface ModuleSource {
  name: string;
  version: string;
  imports: ImportDecl[];
  exports: ExportDecl[];
  /** 可选初始化钩子（求值副作用）；抛错则整批链接失败。 */
  init?: (ns: NamespaceView) => void;
}

/** 模块命名空间的只读视图。 */
export interface NamespaceView {
  has(localName: string): boolean;
  get(localName: string): unknown;
  names(): string[];
}
