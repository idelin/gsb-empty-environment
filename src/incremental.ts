import type { Diagnostic, Edit, EditResult, SyntaxNode } from './types.ts';
import { LineStore } from './lexer.ts';
import { NodeFactory, buildTree, collectIds, walkTree, type ParseOutput } from './parser.ts';

/**
 * 增量缩进解析器。
 * - 词法：只有被编辑的行会重新分词；括号状态扫描走缓存，不碰原文。
 * - 语法：每版本重建树骨架，但节点经哈希一致化工厂构造，
 *   未变化的子树直接复用旧对象（对象身份与 id 双稳定）。
 * - 错误恢复：Tab / 括号 / 缩进错误都降级为局部诊断，
 *   不会让后续内容从树中消失。
 */
export class IncrementalParser {
  private readonly store: LineStore;
  private readonly factory = new NodeFactory();
  private output: ParseOutput;
  private previousIds: Set<string>;
  private previousLineKeys: Set<string>;

  constructor(text: string) {
    this.store = new LineStore(text);
    this.output = buildTree(this.store.all(), this.factory);
    this.previousIds = collectIds(this.output.root);
    this.previousLineKeys = new Set(this.output.lineKeys);
  }

  get root(): SyntaxNode {
    return this.output.root;
  }

  get diagnostics(): readonly Diagnostic[] {
    return this.output.diagnostics;
  }

  get lineCount(): number {
    return this.store.length;
  }

  applyEdit(edit: Edit): EditResult {
    const oldIds = this.previousIds;
    const oldLineKeys = this.previousLineKeys;
    const { retokenized } = this.store.splice(edit.startLine, edit.oldLineCount, edit.newLines);
    this.output = buildTree(this.store.all(), this.factory);

    let totalNodes = 0;
    let reusedNodes = 0;
    walkTree(this.output.root, (node) => {
      totalNodes++;
      if (oldIds.has(node.id)) reusedNodes++;
    });

    const affectedRange = this.computeAffectedRange(edit, oldLineKeys);
    this.previousIds = collectIds(this.output.root);
    this.previousLineKeys = new Set(this.output.lineKeys);

    return {
      root: this.output.root,
      affectedRange,
      diagnostics: this.output.diagnostics,
      stats: {
        retokenizedLines: retokenized,
        totalNodes,
        reusedNodes,
        createdNodes: totalNodes - reusedNodes,
      },
    };
  }

  /**
   * 受影响范围 = 编辑区域 ∪ 行自身内容指纹发生变化的行。
   * 指纹只含本行 kind/indent/text/诊断：子孙变化不会连坐块头行，
   * 内容未变的行即使行号移动也不在范围内。
   */
  private computeAffectedRange(edit: Edit, oldLineKeys: Set<string>): { startLine: number; endLine: number } {
    let start = edit.startLine;
    let end = edit.startLine + edit.newLines.length;
    if (edit.oldLineCount > 0 && end === start) end = start + 1;
    const lineKeys = this.output.lineKeys;
    for (let i = 0; i < lineKeys.length; i++) {
      if (!oldLineKeys.has(lineKeys[i])) {
        if (i < start) start = i;
        if (i + 1 > end) end = i + 1;
      }
    }
    return { startLine: start, endLine: end };
  }
}

/** 全量解析：全新工厂 + 全新行缓存，用作增量结果的对照基准。 */
export function fullParse(text: string): ParseOutput {
  const store = new LineStore(text);
  return buildTree(store.all(), new NodeFactory());
}
