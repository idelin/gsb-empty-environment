// AST for the expression language.
// `shared` nodes are only produced by the optimizer: they evaluate `expr`
// lazily on first dynamic use and memoize the result for the rest of the run.

export type Expr = Lit | Call | If | Shared;

export interface Lit {
  kind: 'lit';
  value: number;
}

export interface Call {
  kind: 'call';
  fn: string;
  args: Expr[];
}

export interface If {
  kind: 'if';
  cond: Expr;
  then: Expr;
  else: Expr;
}

export interface Shared {
  kind: 'shared';
  id: number;
  expr: Expr;
}

export function cloneExpr(e: Expr): Expr {
  switch (e.kind) {
    case 'lit':
      return { kind: 'lit', value: e.value };
    case 'call':
      return { kind: 'call', fn: e.fn, args: e.args.map(cloneExpr) };
    case 'if':
      return { kind: 'if', cond: cloneExpr(e.cond), then: cloneExpr(e.then), else: cloneExpr(e.else) };
    case 'shared':
      return { kind: 'shared', id: e.id, expr: cloneExpr(e.expr) };
  }
}

export function countNodes(e: Expr): number {
  switch (e.kind) {
    case 'lit':
      return 1;
    case 'call':
      return 1 + e.args.reduce((n, a) => n + countNodes(a), 0);
    case 'if':
      return 1 + countNodes(e.cond) + countNodes(e.then) + countNodes(e.else);
    case 'shared':
      return 1 + countNodes(e.expr);
  }
}

export function countShared(e: Expr): number {
  switch (e.kind) {
    case 'lit':
      return 0;
    case 'call':
      return e.args.reduce((n, a) => n + countShared(a), 0);
    case 'if':
      return countShared(e.cond) + countShared(e.then) + countShared(e.else);
    case 'shared':
      return 1 + countShared(e.expr);
  }
}

export function show(e: Expr): string {
  switch (e.kind) {
    case 'lit':
      return String(e.value);
    case 'call':
      return e.args.length === 0 ? `${e.fn}()` : `${e.fn}(${e.args.map(show).join(', ')})`;
    case 'if':
      return `if(${show(e.cond)}, ${show(e.then)}, ${show(e.else)})`;
    case 'shared':
      return `@${e.id}(${show(e.expr)})`;
  }
}
