/**
 * Boolean set expressions over named sets: and(...) / or(...) / set references.
 * Canonicalization sorts children so the hash is stable under operand reordering.
 */
import { createHash } from 'crypto';

export type Expr =
  | { kind: 'set'; set: string }
  | { kind: 'and'; children: Expr[] }
  | { kind: 'or'; children: Expr[] };

export class ExprParseError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ExprParseError';
  }
}

export function setRef(set: string): Expr {
  return { kind: 'set', set };
}

export function and(...children: Expr[]): Expr {
  return { kind: 'and', children };
}

export function or(...children: Expr[]): Expr {
  return { kind: 'or', children };
}

/** Collect the sorted unique set ids referenced by the expression. */
export function exprSets(expr: Expr): string[] {
  const out = new Set<string>();
  const walk = (e: Expr): void => {
    if (e.kind === 'set') out.add(e.set);
    else e.children.forEach(walk);
  };
  walk(expr);
  return [...out].sort();
}

/** Canonical JSON: children of and/or sorted recursively so hash is order-insensitive. */
export function canonical(expr: Expr): unknown {
  if (expr.kind === 'set') return ['set', expr.set];
  const kids = expr.children.map(canonical);
  kids.sort((a, b) => {
    const sa = JSON.stringify(a);
    const sb = JSON.stringify(b);
    return sa < sb ? -1 : sa > sb ? 1 : 0;
  });
  return [expr.kind, kids];
}

export function exprHash(expr: Expr): string {
  return createHash('sha256').update(JSON.stringify(canonical(expr)), 'utf8').digest('hex');
}

/** Parse e.g. "or(A, and(B, C))" (case-insensitive operators, whitespace tolerant). */
export function parseExpr(source: string): Expr {
  let pos = 0;
  const skipWs = (): void => {
    while (pos < source.length && /\s/.test(source[pos])) pos++;
  };
  const peek = (): string => (pos < source.length ? source[pos] : '');
  const parseIdent = (): string => {
    skipWs();
    const m = /^[A-Za-z_][A-Za-z0-9_.-]*/.exec(source.slice(pos));
    if (!m) throw new ExprParseError('expected identifier at offset ' + pos);
    pos += m[0].length;
    return m[0];
  };
  const parseExprInner = (): Expr => {
    skipWs();
    const ident = parseIdent();
    const lower = ident.toLowerCase();
    if (lower !== 'and' && lower !== 'or') return setRef(ident);
    skipWs();
    if (peek() !== '(') throw new ExprParseError('expected "(" after ' + ident + ' at offset ' + pos);
    pos++;
    const children: Expr[] = [];
    for (;;) {
      children.push(parseExprInner());
      skipWs();
      if (peek() === ',') {
        pos++;
        continue;
      }
      if (peek() === ')') {
        pos++;
        break;
      }
      throw new ExprParseError('expected "," or ")" at offset ' + pos);
    }
    if (children.length === 0) throw new ExprParseError(ident + ' requires at least one operand');
    return { kind: lower as 'and' | 'or', children };
  };
  const result = parseExprInner();
  skipWs();
  if (pos !== source.length) throw new ExprParseError('trailing input at offset ' + pos);
  return result;
}
