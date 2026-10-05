export { lexLine, lexedEquals, TAB_WIDTH } from './lexer.ts';
export type { LexedLine, LineKind } from './lexer.ts';

export { buildTree } from './parser.ts';
export type {
  BuiltTree,
  Continuation,
  DocumentError,
  ErrorKind,
  NodeCache,
  NodeError,
  Occurrence,
  SyntaxNode,
} from './parser.ts';

export { IncrementalDocument } from './document.ts';
export type { LineRange, UpdateReport } from './document.ts';

import { lexLine, type LexedLine } from './lexer.ts';
import { buildTree, type BuiltTree, type SyntaxNode } from './parser.ts';

/** Full from-scratch parse with a fresh cache — the reference for tests. */
export function parseFull(text: string): BuiltTree {
  const lexed: LexedLine[] = [];
  let depth = 0;
  for (const line of text.split('\n')) {
    const next = lexLine(line, depth);
    lexed.push(next);
    depth = next.depthOut;
  }
  return buildTree(lexed, new Map());
}

/** Structural fingerprint: equal iff two trees are structurally identical. */
export function serialize(node: SyntaxNode): string {
  return `${node.id}(${node.children.map(serialize).join(',')})`;
}
