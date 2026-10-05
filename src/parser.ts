import { createHash } from 'node:crypto';
import type { Diagnostic, LocalDiagnostic, NodeKind, SyntaxNode } from './types.ts';
import type { LineAnalysis } from './lexer.ts';

function hashNode(
  kind: NodeKind,
  indent: number,
  text: string,
  children: readonly SyntaxNode[],
  diagnostics: readonly LocalDiagnostic[],
): string {
  const hash = createHash('sha1');
  hash.update(kind);
  hash.update('\0');
  hash.update(String(indent));
  hash.update('\0');
  hash.update(text);
  hash.update('\0');
  for (const diagnostic of diagnostics) {
    hash.update(diagnostic.code);
    hash.update('\0');
    hash.update(String(diagnostic.column));
    hash.update('\0');
  }
  for (const child of children) {
    hash.update(child.id);
    hash.update('\0');
  }
  return hash.digest('hex').slice(0, 16);
}

/**
 * 哈希一致化（hash-consing）节点工厂：结构相同的子树永远得到同一个对象。
 * IncrementalParser 跨编辑复用同一个工厂，因此未变化的子树在新版本中
 * 保持对象身份（===）与 id 双重稳定，可直接被编辑器/渲染层复用。
 */
export class NodeFactory {
  private cache = new Map<string, SyntaxNode>();

  make(
    kind: NodeKind,
    indent: number,
    text: string,
    children: readonly SyntaxNode[],
    diagnostics: readonly LocalDiagnostic[],
  ): SyntaxNode {
    const id = hashNode(kind, indent, text, children, diagnostics);
    const cached = this.cache.get(id);
    if (cached) return cached;
    const node: SyntaxNode = {
      id,
      kind,
      indent,
      text,
      children: [...children],
      diagnostics: [...diagnostics],
    };
    this.cache.set(id, node);
    return node;
  }
}

export interface ParseOutput {
  readonly root: SyntaxNode;
  /** 每一行对应的叶子/行节点（代码行节点在其块闭合时回填）。 */
  readonly lineNodes: readonly SyntaxNode[];
  /**
   * 每一行的“自身内容指纹”：只含 kind/indent/text/本行诊断，
   * 不含子孙。用于编辑后精确找出内容真正变化的行，
   * 避免子孙变化连坐块头行。
   */
  readonly lineKeys: readonly string[];
  /** 全量诊断（绝对行号，按行排序）。 */
  readonly diagnostics: readonly Diagnostic[];
}

function lineKey(kind: string, indent: number, text: string, diagnostics: readonly LocalDiagnostic[]): string {
  const diagPart = diagnostics.map((d) => `${d.code}@${d.column}`).join(',');
  return `${kind}|${indent}|${text}|${diagPart}`;
}

interface Frame {
  indent: number;
  kind: NodeKind;
  text: string;
  lineIndex: number;
  children: SyntaxNode[];
  diagnostics: LocalDiagnostic[];
}

/**
 * 由行分析结果构建块树。缩进规则：
 * - 代码行缩进大于上一有效行 -> 开启子块；
 * - 回退到栈上不存在的缩进层级 -> inconsistent-dedent 诊断，
 *   并恢复为最近一个更浅块的子级，保证后续内容不丢失；
 * - 括号未闭合期间的行（depthBefore > 0）是续行，不参与缩进结构；
 * - 空行/注释是叶子，不影响缩进栈（注释按自身缩进归位）。
 */
export function buildTree(lines: readonly LineAnalysis[], factory: NodeFactory): ParseOutput {
  const diagnostics: Diagnostic[] = [];
  const lineNodes: (SyntaxNode | null)[] = new Array(lines.length).fill(null);
  const lineKeys: string[] = new Array(lines.length).fill('');

  const rootFrame: Frame = { indent: -1, kind: 'document', text: '', lineIndex: -1, children: [], diagnostics: [] };
  const stack: Frame[] = [rootFrame];
  const top = (): Frame => stack[stack.length - 1];

  const closeFrame = (): void => {
    const frame = stack.pop() as Frame;
    const node = factory.make(frame.kind, frame.indent, frame.text, frame.children, frame.diagnostics);
    top().children.push(node);
    if (frame.lineIndex >= 0) lineNodes[frame.lineIndex] = node;
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    for (const diagnostic of line.diagnostics) diagnostics.push(diagnostic);
    const localDiags: LocalDiagnostic[] = line.diagnostics.map(({ code, column, message }) => ({
      code,
      column,
      message,
    }));

    if (line.kind === 'blank') {
      const node = factory.make('blank', 0, '', [], []);
      top().children.push(node);
      lineNodes[i] = node;
      lineKeys[i] = lineKey('blank', 0, '', []);
      continue;
    }

    if (line.depthBefore > 0) {
      // 括号续行：无论代码还是注释，都不影响缩进栈。
      const node = factory.make('continuation', line.indent, line.text, [], localDiags);
      top().children.push(node);
      lineNodes[i] = node;
      lineKeys[i] = lineKey('continuation', line.indent, line.text, localDiags);
      continue;
    }

    if (line.kind === 'comment') {
      while (top().indent >= line.indent && stack.length > 1) closeFrame();
      const node = factory.make('comment', line.indent, line.text, [], localDiags);
      top().children.push(node);
      lineNodes[i] = node;
      lineKeys[i] = lineKey('comment', line.indent, line.text, localDiags);
      continue;
    }

    // 代码行：先闭合更深/同级块，再决定是子块还是错误恢复。
    let popped = false;
    while (top().indent > line.indent) {
      closeFrame();
      popped = true;
    }
    if (top().indent === line.indent) {
      closeFrame();
    } else if (popped) {
      const diagnostic: Diagnostic = {
        code: 'inconsistent-dedent',
        line: i,
        column: 0,
        message: `Indent ${line.indent} does not match any outer level; recovered as child of indent ${top().indent}`,
      };
      diagnostics.push(diagnostic);
      localDiags.push({ code: diagnostic.code, column: 0, message: diagnostic.message });
    }
    stack.push({ indent: line.indent, kind: 'line', text: line.text, lineIndex: i, children: [], diagnostics: localDiags });
    lineKeys[i] = lineKey('line', line.indent, line.text, localDiags);
  }

  while (stack.length > 1) closeFrame();
  const root = factory.make('document', -1, '', rootFrame.children, []);

  diagnostics.sort((a, b) => a.line - b.line || a.column - b.column);
  return { root, lineNodes: lineNodes as SyntaxNode[], lineKeys, diagnostics };
}

export function walkTree(node: SyntaxNode, visit: (node: SyntaxNode) => void): void {
  visit(node);
  for (const child of node.children) walkTree(child, visit);
}

export function collectIds(root: SyntaxNode): Set<string> {
  const ids = new Set<string>();
  walkTree(root, (node) => ids.add(node.id));
  return ids;
}
