import type { Expr, Program, Stmt } from './ast.ts';

export interface Declaration {
  id: number;
  name: string;
  nameSpan: { start: number; end: number };
}

export interface RefRecord {
  id: number;
  name: string;
  span: { start: number; end: number };
  declId: number | null;
}

export interface BindingGraph {
  decls: Declaration[];
  refs: RefRecord[];
}

interface Scope {
  names: Map<string, Declaration>;
}

class Binder {
  readonly decls: Declaration[] = [];
  readonly refs: RefRecord[] = [];
  private readonly scopes: Scope[] = [{ names: new Map() }];

  private lookup(name: string): Declaration | null {
    for (let i = this.scopes.length - 1; i >= 0; i--) {
      const decl = this.scopes[i].names.get(name);
      if (decl) return decl;
    }
    return null;
  }

  private bindExpr(expr: Expr): void {
    switch (expr.kind) {
      case 'ident': {
        const decl = this.lookup(expr.name);
        this.refs.push({
          id: this.refs.length,
          name: expr.name,
          span: expr.span,
          declId: decl ? decl.id : null,
        });
        return;
      }
      case 'num':
        return;
      case 'binary':
        this.bindExpr(expr.left);
        this.bindExpr(expr.right);
        return;
    }
  }

  private bindStmt(stmt: Stmt): void {
    switch (stmt.kind) {
      case 'let': {
        // The initializer is bound before the declaration enters scope, so
        // `let x = x;` resolves the right-hand `x` to an outer binding.
        this.bindExpr(stmt.init);
        const decl: Declaration = {
          id: this.decls.length,
          name: stmt.name,
          nameSpan: stmt.nameSpan,
        };
        this.decls.push(decl);
        // A later `let` with the same name in the same block shadows the
        // earlier one for all subsequent statements in that block.
        this.scopes[this.scopes.length - 1].names.set(stmt.name, decl);
        return;
      }
      case 'block':
        this.scopes.push({ names: new Map() });
        for (const inner of stmt.body) this.bindStmt(inner);
        this.scopes.pop();
        return;
      case 'expr':
        this.bindExpr(stmt.expr);
        return;
    }
  }

  bindProgram(program: Program): void {
    for (const stmt of program.body) this.bindStmt(stmt);
  }
}

// Statements are bound in source order. A `let` becomes visible only after
// its own statement, so a reference that textually precedes the declaration
// in the same block does not see it and resolves to an outer binding (or is
// unbound). Declaration and reference ids are ordinals in source order, which
// makes two graphs from structurally identical sources directly comparable.
export function bind(program: Program): BindingGraph {
  const binder = new Binder();
  binder.bindProgram(program);
  return { decls: binder.decls, refs: binder.refs };
}
