/** 节点种类：document 为根，line 为代码行（可携带子块），其余为叶子。 */
export type NodeKind = 'document' | 'line' | 'comment' | 'blank' | 'continuation';

export type DiagnosticCode =
  | 'tab-in-indent'
  | 'unmatched-close'
  | 'unclosed-bracket'
  | 'inconsistent-dedent';

/** 挂在节点上的诊断：与绝对位置无关，保证节点 ID 的位置无关性。 */
export interface LocalDiagnostic {
  readonly code: DiagnosticCode;
  readonly column: number;
  readonly message: string;
}

/** 对外输出的诊断：带绝对行号（每个版本重新计算）。 */
export interface Diagnostic extends LocalDiagnostic {
  readonly line: number;
}

/**
 * 语法节点。不可变、不携带绝对位置：
 * id 为内容寻址哈希（kind/indent/text/诊断/子节点 id），
 * 内容不变的子树在任意次编辑后 id 不变，且（同一 IncrementalParser 内）为同一对象。
 */
export interface SyntaxNode {
  readonly id: string;
  readonly kind: NodeKind;
  readonly indent: number;
  readonly text: string;
  readonly children: readonly SyntaxNode[];
  readonly diagnostics: readonly LocalDiagnostic[];
}

/** 一次行级编辑：把 [startLine, startLine+oldLineCount) 替换为 newLines。 */
export interface Edit {
  readonly startLine: number;
  readonly oldLineCount: number;
  readonly newLines: readonly string[];
}

export interface EditStats {
  /** 本次重新分词（re-tokenize）的行数。 */
  readonly retokenizedLines: number;
  /** 新树节点总数（按出现次数计）。 */
  readonly totalNodes: number;
  /** id 在上一版本中已存在的节点数（即可复用的未变子树）。 */
  readonly reusedNodes: number;
  /** 本次新建节点数。 */
  readonly createdNodes: number;
}

export interface EditResult {
  readonly root: SyntaxNode;
  /** 受影响行范围 [startLine, endLine)，供编辑器重绘。 */
  readonly affectedRange: { readonly startLine: number; readonly endLine: number };
  /** 当前版本全部诊断（绝对行号）。 */
  readonly diagnostics: readonly Diagnostic[];
  readonly stats: EditStats;
}
