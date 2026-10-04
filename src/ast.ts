// Expression AST for fixed-width bit-vector arithmetic.
// Every operation truncates its result to the checker's bit width.

export type BinOp = 'and' | 'or' | 'xor' | 'add' | 'sub' | 'shl' | 'shr';

export type Expr =
  | { kind: 'const'; value: bigint }
  | { kind: 'var'; name: string }
  | { kind: 'not'; a: Expr } // bitwise ~
  | { kind: 'neg'; a: Expr } // arithmetic unary -
  | { kind: 'bin'; op: BinOp; a: Expr; b: Expr }
  | { kind: 'ite'; c: Expr; t: Expr; e: Expr }; // c != 0 ? t : e

export const kConst = (value: bigint): Expr => ({ kind: 'const', value });
export const kVar = (name: string): Expr => ({ kind: 'var', name });
export const kNot = (a: Expr): Expr => ({ kind: 'not', a });
export const kNeg = (a: Expr): Expr => ({ kind: 'neg', a });
export const kBin = (op: BinOp, a: Expr, b: Expr): Expr => ({ kind: 'bin', op, a, b });
export const kIte = (c: Expr, t: Expr, e: Expr): Expr => ({ kind: 'ite', c, t, e });

// Variable names in deterministic (sorted) order. The sorted order defines
// the tuple order used for "lexicographically smallest counterexample".
export function collectVars(...exprs: Expr[]): string[] {
  const set = new Set<string>();
  const walk = (e: Expr): void => {
    switch (e.kind) {
      case 'const':
        return;
      case 'var':
        set.add(e.name);
        return;
      case 'not':
      case 'neg':
        walk(e.a);
        return;
      case 'bin':
        walk(e.a);
        walk(e.b);
        return;
      case 'ite':
        walk(e.c);
        walk(e.t);
        walk(e.e);
        return;
    }
  };
  for (const e of exprs) walk(e);
  return [...set].sort();
}

export function exprToString(e: Expr): string {
  switch (e.kind) {
    case 'const':
      return e.value.toString();
    case 'var':
      return e.name;
    case 'not':
      return `(~${exprToString(e.a)})`;
    case 'neg':
      return `(-${exprToString(e.a)})`;
    case 'bin': {
      const sym: Record<BinOp, string> = {
        and: '&', or: '|', xor: '^', add: '+', sub: '-', shl: '<<', shr: '>>',
      };
      return `(${exprToString(e.a)} ${sym[e.op]} ${exprToString(e.b)})`;
    }
    case 'ite':
      return `(${exprToString(e.c)} ? ${exprToString(e.t)} : ${exprToString(e.e)})`;
  }
}
