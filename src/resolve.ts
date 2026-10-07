// Surface (name-only) AST -> resolved AST with binding identities.
// Scope resolution uses per-name binder stacks, so shadowing is handled
// structurally; unbound names become program-free variables with stable ids.

import type { BinOp, Binder, BindId, Expr, NodeId } from './ast.ts';

export type S =
  | { k: 'num'; v: number }
  | { k: 'bool'; v: boolean }
  | { k: 'var'; n: string }
  | { k: 'lam'; p: string; b: S }
  | { k: 'app'; f: S; a: S[] }
  | { k: 'let'; n: string; v: S; b: S }
  | { k: 'if'; c: S; t: S; e: S }
  | { k: 'bin'; op: BinOp; l: S; r: S }
  | { k: 'ret'; v: S };

export interface Resolved {
  root: Expr;
  free: Binder[];
}

export function resolve(s: S): Resolved {
  let nextNode: NodeId = 0;
  let nextBind: BindId = 0;
  const scope = new Map<string, BindId[]>();
  const freeByName = new Map<string, BindId>();
  const free: Binder[] = [];

  const push = (name: string, bind: BindId) => {
    const stack = scope.get(name) ?? [];
    stack.push(bind);
    scope.set(name, stack);
  };
  const pop = (name: string) => {
    scope.get(name)!.pop();
  };

  function go(s: S): Expr {
    const id = nextNode++;
    switch (s.k) {
      case 'num':
        return { kind: 'num', id, value: s.v };
      case 'bool':
        return { kind: 'bool', id, value: s.v };
      case 'var': {
        const stack = scope.get(s.n);
        let ref: BindId;
        if (stack && stack.length > 0) {
          ref = stack[stack.length - 1];
        } else {
          ref = freeByName.get(s.n) ?? nextBind++;
          if (!freeByName.has(s.n)) {
            freeByName.set(s.n, ref);
            free.push({ bind: ref, name: s.n });
          }
        }
        return { kind: 'var', id, name: s.n, ref };
      }
      case 'lam': {
        const param: Binder = { bind: nextBind++, name: s.p };
        push(s.p, param.bind);
        const body = go(s.b);
        pop(s.p);
        return { kind: 'lam', id, params: [param], body };
      }
      case 'app':
        return { kind: 'app', id, fn: go(s.f), args: s.a.map(go) };
      case 'let': {
        const value = go(s.v);
        const binder: Binder = { bind: nextBind++, name: s.n };
        push(s.n, binder.bind);
        const body = go(s.b);
        pop(s.n);
        return { kind: 'let', id, binder, value, body };
      }
      case 'if':
        return { kind: 'if', id, cond: go(s.c), then: go(s.t), els: go(s.e) };
      case 'bin':
        return { kind: 'bin', id, op: s.op, lhs: go(s.l), rhs: go(s.r) };
      case 'ret':
        return { kind: 'return', id, value: go(s.v) };
    }
  }

  const root = go(s);
  return { root, free };
}
