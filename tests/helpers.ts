import { parse, walkAst } from '../src/index.ts';
import type { AnyNode, Program, Stmt } from '../src/index.ts';

const STMT_KINDS = new Set(['Let', 'If', 'While', 'FnDecl', 'Return', 'Block', 'ExprStmt']);

export function findStmt(source: string, textIncludes: string, occurrence = 1): Stmt {
  const program: Program = parse(source);
  let seen = 0;
  let found: Stmt | null = null;
  walkAst(program, (node: AnyNode) => {
    if (found || !STMT_KINDS.has(node.kind)) return;
    if (source.slice(node.start, node.end).trimStart().startsWith(textIncludes)) {
      seen++;
      if (seen === occurrence) found = node as Stmt;
    }
  });
  if (!found) throw new Error(`no statement containing '${textIncludes}' (occurrence ${occurrence})`);
  return found;
}

export function nodeIds(trace: { nodeId: number }[]): number[] {
  return trace.map((t) => t.nodeId);
}
