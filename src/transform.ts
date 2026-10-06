// Boxing transform.
//
// Analysis: for every function scope, find locals that are
//   (a) captured  — referenced from a *different* (descendant) function, and
//   (b) mutated   — assigned anywhere in the program.
// Only captured AND mutated locals are heap-boxed. Captured-but-immutable
// variables are captured by value at closure-creation time; everything else
// stays in plain stack-frame slots.
//
// Capture lists are transitive: if an inner closure needs a grandparent's
// variable, every function in between also lists it, so the machine can
// resolve it hop by hop.

import type { Capture, Expr, Program, Stmt, TProgram, TStmt } from './ast.ts';

type FuncStmt = Stmt & { tag: 'func' };

class FuncInfo {
  declared = new Set<string>();
  mutated = new Set<string>();
  captured = new Set<string>();
  directFree = new Set<string>();
  free: Set<string> = new Set();
  boxed: Set<string> = new Set();
  children: FuncInfo[] = [];
  parent: FuncInfo | null;
  node: FuncStmt | null;

  constructor(parent: FuncInfo | null, node: FuncStmt | null) {
    this.parent = parent;
    this.node = node;
  }
}

export function transform(prog: Program): TProgram {
  const root = new FuncInfo(null, null);
  const funcInfos = new Map<FuncStmt, FuncInfo>();
  analyzeBlock(prog, root, funcInfos);
  computeFree(root);
  computeBoxed(root);
  return rewriteBlock(prog, root, funcInfos);
}

function declare(fn: FuncInfo, name: string): void {
  if (fn.declared.has(name)) {
    throw new Error(`duplicate declaration in the same function scope: ${name}`);
  }
  fn.declared.add(name);
}

function reference(fn: FuncInfo, name: string, isWrite: boolean): void {
  let owner: FuncInfo | null = fn;
  while (owner !== null && !owner.declared.has(name)) owner = owner.parent;
  if (owner === null) throw new Error(`undefined variable: ${name}`);
  if (owner !== fn) {
    owner.captured.add(name);
    fn.directFree.add(name);
  }
  if (isWrite) owner.mutated.add(name);
}

function analyzeBlock(stmts: Stmt[], fn: FuncInfo, map: Map<FuncStmt, FuncInfo>): void {
  for (const stmt of stmts) {
    switch (stmt.tag) {
      case 'let':
        declare(fn, stmt.name);
        analyzeExpr(stmt.init, fn, map);
        break;
      case 'assign':
        reference(fn, stmt.name, true);
        analyzeExpr(stmt.value, fn, map);
        break;
      case 'func': {
        declare(fn, stmt.name);
        const child = new FuncInfo(fn, stmt);
        fn.children.push(child);
        map.set(stmt, child);
        for (const param of stmt.params) declare(child, param);
        analyzeBlock(stmt.body, child, map);
        break;
      }
      case 'if':
        analyzeBlock(stmt.then, fn, map);
        analyzeBlock(stmt.else, fn, map);
        break;
      case 'while':
        analyzeBlock(stmt.body, fn, map);
        break;
      case 'return':
      case 'print':
      case 'expr':
        analyzeExpr(stmt.value, fn, map);
        break;
    }
  }
}

function analyzeExpr(expr: Expr, fn: FuncInfo, map: Map<FuncStmt, FuncInfo>): void {
  switch (expr.tag) {
    case 'var':
      reference(fn, expr.name, false);
      break;
    case 'bin':
      analyzeExpr(expr.left, fn, map);
      analyzeExpr(expr.right, fn, map);
      break;
    case 'call':
      analyzeExpr(expr.fn, fn, map);
      for (const arg of expr.args) analyzeExpr(arg, fn, map);
      break;
    default:
      break;
  }
}

function computeFree(fn: FuncInfo): void {
  fn.free = new Set(fn.directFree);
  for (const child of fn.children) {
    computeFree(child);
    for (const name of child.free) {
      if (!fn.declared.has(name)) fn.free.add(name);
    }
  }
}

function computeBoxed(fn: FuncInfo): void {
  for (const name of fn.declared) {
    if (fn.mutated.has(name) && fn.captured.has(name)) fn.boxed.add(name);
  }
  for (const child of fn.children) computeBoxed(child);
}

function captureKind(from: FuncInfo, name: string): Capture['by'] {
  let owner: FuncInfo | null = from;
  while (owner !== null && !owner.declared.has(name)) owner = owner.parent;
  if (owner === null) throw new Error(`undefined variable: ${name}`);
  return owner.boxed.has(name) ? 'box' : 'value';
}

function rewriteBlock(stmts: Stmt[], fn: FuncInfo, map: Map<FuncStmt, FuncInfo>): TStmt[] {
  return stmts.map((stmt): TStmt => {
    switch (stmt.tag) {
      case 'let':
        return { tag: 'let', name: stmt.name, init: stmt.init, boxed: fn.boxed.has(stmt.name) };
      case 'assign':
        return { tag: 'assign', name: stmt.name, value: stmt.value };
      case 'func': {
        const child = map.get(stmt);
        if (!child) throw new Error(`missing analysis for function: ${stmt.name}`);
        const selfRef = child.free.has(stmt.name);
        const captures: Capture[] = [...child.free]
          .filter((name) => name !== stmt.name)
          .sort()
          .map((name) => ({ name, by: captureKind(fn, name) }));
        return {
          tag: 'func',
          name: stmt.name,
          params: stmt.params,
          boxedParams: stmt.params.filter((param) => child.boxed.has(param)),
          captures,
          selfRef,
          body: rewriteBlock(stmt.body, child, map),
        };
      }
      case 'if':
        return {
          tag: 'if',
          cond: stmt.cond,
          then: rewriteBlock(stmt.then, fn, map),
          else: rewriteBlock(stmt.else, fn, map),
        };
      case 'while':
        return { tag: 'while', cond: stmt.cond, body: rewriteBlock(stmt.body, fn, map) };
      case 'return':
        return { tag: 'return', value: stmt.value };
      case 'print':
        return { tag: 'print', value: stmt.value };
      case 'expr':
        return { tag: 'expr', value: stmt.value };
    }
  });
}
