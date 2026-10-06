// Scope resolution + capture analysis.
//
// Every binding gets a unique Decl. A Decl is marked `captured` when a
// lexically nested function references it; each function records the set of
// free variables it (transitively) needs from enclosing scopes, ordered by
// declaration id so capture layouts are deterministic.

import { Program, Stmt, Expr, FnExpr, Decl } from '../ast';

export interface FnInfo {
  node: FnExpr | null; // null = top-level pseudo-function
  parent: FnInfo | null;
  decls: Decl[];
  freeSet: Set<Decl>;
  freeVars: Decl[]; // filled after the walk, sorted by decl id
}

export interface Resolved {
  top: FnInfo;
  fnInfos: Map<FnExpr, FnInfo>;
}

interface Scope {
  vars: Map<string, Decl>;
}

export function resolve(program: Program): Resolved {
  let nextDeclId = 0;
  const fnInfos = new Map<FnExpr, FnInfo>();
  const top: FnInfo = { node: null, parent: null, decls: [], freeSet: new Set(), freeVars: [] };
  const fnStack: FnInfo[] = [top];
  const scopes: Scope[] = [{ vars: new Map() }];

  resolveStmts(program.body);

  const all: FnInfo[] = [top, ...fnInfos.values()];
  for (const info of all) {
    info.freeVars = [...info.freeSet].sort((a, b) => a.id - b.id);
  }
  return { top, fnInfos };

  function currentFn(): FnInfo {
    return fnStack[fnStack.length - 1];
  }

  function declare(name: string, mutable: boolean, isParam: boolean, isSelf: boolean): Decl {
    const scope = scopes[scopes.length - 1];
    if (scope.vars.has(name)) {
      throw new Error(`duplicate declaration of "${name}"`);
    }
    const decl: Decl = {
      id: nextDeclId++,
      name,
      mutable,
      isParam,
      isSelf,
      owner: currentFn().node,
      captured: false,
    };
    scope.vars.set(name, decl);
    currentFn().decls.push(decl);
    return decl;
  }

  function lookup(name: string): Decl {
    for (let i = scopes.length - 1; i >= 0; i--) {
      const decl = scopes[i].vars.get(name);
      if (decl) return decl;
    }
    throw new Error(`unbound variable "${name}"`);
  }

  // Mark `decl` as captured and record it as a free variable of every
  // function between the reference site and the owning function (exclusive),
  // so intermediate functions thread it through as an upvalue.
  function markCaptured(decl: Decl): void {
    if (decl.owner === currentFn().node) return;
    decl.captured = true;
    for (let i = fnStack.length - 1; i >= 0; i--) {
      const info = fnStack[i];
      if (info.node === decl.owner) break;
      info.freeSet.add(decl);
    }
  }

  function resolveStmts(stmts: Stmt[]): void {
    for (const s of stmts) resolveStmt(s);
  }

  function resolveBlock(body: Stmt[]): void {
    scopes.push({ vars: new Map() });
    resolveStmts(body);
    scopes.pop();
  }

  function resolveStmt(s: Stmt): void {
    switch (s.kind) {
      case 'let':
        resolveExpr(s.init);
        s.decl = declare(s.name, s.mutable, false, false);
        return;
      case 'set': {
        const decl = lookup(s.name);
        if (!decl.mutable) {
          throw new Error(`cannot assign to const "${s.name}"`);
        }
        s.decl = decl;
        markCaptured(decl);
        resolveExpr(s.value);
        return;
      }
      case 'expr':
        resolveExpr(s.expr);
        return;
      case 'block':
        resolveBlock(s.body);
        return;
      case 'if':
        resolveExpr(s.cond);
        resolveBlock(s.then);
        resolveBlock(s.else);
        return;
      case 'while':
        resolveExpr(s.cond);
        resolveBlock(s.body);
        return;
      case 'return':
        if (s.expr) resolveExpr(s.expr);
        return;
      case 'print':
        resolveExpr(s.expr);
        return;
    }
  }

  function resolveExpr(e: Expr): void {
    switch (e.kind) {
      case 'num':
      case 'bool':
        return;
      case 'var': {
        const decl = lookup(e.name);
        e.decl = decl;
        markCaptured(decl);
        return;
      }
      case 'bin':
        resolveExpr(e.left);
        resolveExpr(e.right);
        return;
      case 'not':
        resolveExpr(e.expr);
        return;
      case 'call':
        resolveExpr(e.fn);
        e.args.forEach(resolveExpr);
        return;
      case 'fn': {
        const info: FnInfo = {
          node: e,
          parent: currentFn(),
          decls: [],
          freeSet: new Set(),
          freeVars: [],
        };
        fnInfos.set(e, info);
        fnStack.push(info);
        scopes.push({ vars: new Map() });
        if (e.selfName) declare(e.selfName, false, false, true);
        for (const p of e.params) declare(p, true, true, false);
        resolveStmts(e.body);
        scopes.pop();
        fnStack.pop();
        return;
      }
      case 'arr':
        e.items.forEach(resolveExpr);
        return;
      case 'index':
        resolveExpr(e.arr);
        resolveExpr(e.index);
        return;
      case 'push':
        resolveExpr(e.arr);
        resolveExpr(e.value);
        return;
      case 'len':
        resolveExpr(e.arr);
        return;
    }
  }
}
