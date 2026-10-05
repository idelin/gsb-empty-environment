import type { Expr, Ident, Program, Stmt } from './ast.ts';

export type SymbolKind = 'let' | 'fn' | 'param' | 'import';

export interface SymbolInfo {
  id: string;
  file: string;
  name: string;
  kind: SymbolKind;
  nameIdent: Ident;
  declPos: number;
  exported: boolean;
  target?: string;
  importSource?: string;
  importedName?: string;
}

export type DiagnosticCode =
  | 'unbound'
  | 'duplicate'
  | 'import-unresolved'
  | 'import-missing-export';

export interface Diagnostic {
  file: string;
  code: DiagnosticCode;
  message: string;
  start: number;
  end: number;
  nodeId: number;
}

export interface ImportSpecInfo {
  importedIdent: Ident;
  localIdent: Ident | null;
  aliasSymbolId: string;
  source: string;
  sourceStart: number;
  sourceEnd: number;
}

export interface FileAnalysis {
  file: string;
  program: Program;
  symbols: SymbolInfo[];
  bindings: Map<number, string>;
  diagnostics: Diagnostic[];
  importSpecs: ImportSpecInfo[];
  exports: Map<string, string>;
}

interface Scope {
  parent: Scope | null;
  decls: SymbolInfo[];
  fnCutoff: number | null;
}

export function resolveFile(file: string, program: Program): FileAnalysis {
  const symbols: SymbolInfo[] = [];
  const bindings = new Map<number, string>();
  const diagnostics: Diagnostic[] = [];
  const importSpecs: ImportSpecInfo[] = [];
  const exportsMap = new Map<string, string>();

  function declare(
    scope: Scope,
    nameIdent: Ident,
    kind: SymbolKind,
    exported: boolean,
    extra?: Partial<SymbolInfo>,
  ): SymbolInfo {
    const sym: SymbolInfo = {
      id: `${file}#${symbols.length}`,
      file,
      name: nameIdent.name,
      kind,
      nameIdent,
      declPos: nameIdent.start,
      exported,
      ...extra,
    };
    if (scope.decls.some((d) => d.name === sym.name)) {
      diagnostics.push({
        file,
        code: 'duplicate',
        message: `duplicate declaration of '${sym.name}' in the same scope`,
        start: nameIdent.start,
        end: nameIdent.end,
        nodeId: nameIdent.id,
      });
    }
    scope.decls.push(sym);
    symbols.push(sym);
    if (exported && scope.parent === null) exportsMap.set(sym.name, sym.id);
    return sym;
  }

  function lookup(name: string, pos: number, scope: Scope): SymbolInfo | null {
    let cutoff = pos;
    let s: Scope | null = scope;
    while (s !== null) {
      for (let i = s.decls.length - 1; i >= 0; i--) {
        const d = s.decls[i];
        if (d.name === name && d.declPos < cutoff) return d;
      }
      if (s.fnCutoff !== null && s.fnCutoff < cutoff) cutoff = s.fnCutoff;
      s = s.parent;
    }
    return null;
  }

  function resolveIdent(ident: Ident, scope: Scope): void {
    const sym = lookup(ident.name, ident.start, scope);
    if (sym) {
      bindings.set(ident.id, sym.id);
    } else {
      diagnostics.push({
        file,
        code: 'unbound',
        message: `unbound reference '${ident.name}'`,
        start: ident.start,
        end: ident.end,
        nodeId: ident.id,
      });
    }
  }

  function resolveExpr(expr: Expr, scope: Scope): void {
    switch (expr.type) {
      case 'Ident':
        resolveIdent(expr, scope);
        break;
      case 'BinExpr':
        resolveExpr(expr.left, scope);
        resolveExpr(expr.right, scope);
        break;
      case 'CallExpr':
        resolveExpr(expr.callee, scope);
        for (const arg of expr.args) resolveExpr(arg, scope);
        break;
      default:
        break;
    }
  }

  function walkStmts(stmts: Stmt[], scope: Scope): void {
    for (const stmt of stmts) {
      switch (stmt.type) {
        case 'LetDecl':
          resolveExpr(stmt.init, scope);
          declare(scope, stmt.name, 'let', stmt.exported);
          break;
        case 'FnDecl': {
          declare(scope, stmt.name, 'fn', stmt.exported);
          const fnScope: Scope = { parent: scope, decls: [], fnCutoff: stmt.name.end };
          for (const param of stmt.params) declare(fnScope, param, 'param', false);
          walkStmts(stmt.body.body, fnScope);
          break;
        }
        case 'ImportDecl':
          for (const spec of stmt.specs) {
            const localIdent = spec.local ?? spec.imported;
            const sym = declare(scope, localIdent, 'import', false, {
              importSource: stmt.source,
              importedName: spec.imported.name,
            });
            importSpecs.push({
              importedIdent: spec.imported,
              localIdent: spec.local,
              aliasSymbolId: sym.id,
              source: stmt.source,
              sourceStart: stmt.sourceStart,
              sourceEnd: stmt.sourceEnd,
            });
          }
          break;
        case 'Block':
          walkStmts(stmt.body, { parent: scope, decls: [], fnCutoff: null });
          break;
        case 'ReturnStmt':
          if (stmt.arg) resolveExpr(stmt.arg, scope);
          break;
        case 'ExprStmt':
          resolveExpr(stmt.expr, scope);
          break;
      }
    }
  }

  const root: Scope = { parent: null, decls: [], fnCutoff: null };
  walkStmts(program.body, root);

  return { file, program, symbols, bindings, diagnostics, importSpecs, exports: exportsMap };
}
