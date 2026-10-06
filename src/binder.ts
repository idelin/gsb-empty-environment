import type { Block, Expr, Ident, LetDecl, Program, Span, Stmt } from "./ast.ts";

export interface Binding {
  id: number;
  name: string;
  nameSpan: Span;
  decl: LetDecl;
  scope: Scope;
  /**
   * Position from which the binding becomes visible: the end of the `let`
   * statement. References earlier than this position (including references
   * inside the declaration's own initializer) never resolve to this binding.
   */
  visibleFrom: number;
}

export interface Scope {
  block: Program | Block;
  parent: Scope | null;
  bindings: Binding[];
}

export interface BindResult {
  bindings: Binding[];
  resolutions: Map<Ident, Binding | null>;
  identScopes: Map<Ident, Scope>;
  rootScope: Scope;
}

/**
 * Resolves `name` as seen from `scope` at source position `pos`, honouring
 * declare-before-use: inside each enclosing block only bindings whose
 * `visibleFrom` is not after the entry position are candidates. When
 * `renamed`/`renamedTo` are given, that binding is looked up under its new
 * name (used to simulate a rename without editing the tree).
 */
export function resolveName(
  scope: Scope,
  pos: number,
  name: string,
  renamed: Binding | null = null,
  renamedTo: string = "",
): Binding | null {
  let current: Scope | null = scope;
  let entry = pos;
  while (current) {
    let best: Binding | null = null;
    for (const b of current.bindings) {
      const bName = b === renamed ? renamedTo : b.name;
      if (bName !== name || b.visibleFrom > entry) continue;
      if (!best || b.visibleFrom > best.visibleFrom) best = b;
    }
    if (best) return best;
    entry = current.block.span.start;
    current = current.parent;
  }
  return null;
}

export function bind(program: Program): BindResult {
  const bindings: Binding[] = [];
  const resolutions = new Map<Ident, Binding | null>();
  const identScopes = new Map<Ident, Scope>();
  const rootScope: Scope = { block: program, parent: null, bindings: [] };
  let nextId = 0;

  function resolveExpr(expr: Expr, scope: Scope): void {
    switch (expr.kind) {
      case "Ident": {
        identScopes.set(expr, scope);
        resolutions.set(expr, resolveName(scope, expr.span.start, expr.text));
        break;
      }
      case "Binary":
        resolveExpr(expr.left, scope);
        resolveExpr(expr.right, scope);
        break;
      case "NumLit":
        break;
    }
  }

  function walkStmt(stmt: Stmt, scope: Scope): void {
    switch (stmt.kind) {
      case "LetDecl": {
        resolveExpr(stmt.init, scope);
        const binding: Binding = {
          id: nextId++,
          name: stmt.name,
          nameSpan: stmt.nameSpan,
          decl: stmt,
          scope,
          visibleFrom: stmt.span.end,
        };
        scope.bindings.push(binding);
        bindings.push(binding);
        break;
      }
      case "ExprStmt":
        resolveExpr(stmt.expr, scope);
        break;
      case "Block":
        walkBlock(stmt, { block: stmt, parent: scope, bindings: [] });
        break;
    }
  }

  function walkBlock(block: Program | Block, scope: Scope): void {
    for (const stmt of block.body) walkStmt(stmt, scope);
  }

  walkBlock(program, rootScope);
  return { bindings, resolutions, identScopes, rootScope };
}
