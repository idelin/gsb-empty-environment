// random.ts — seeded PRNG, typed random program generator, env generation.

import { BindingId, Surface } from './core';
import { Env, EvalError, Value } from './interp';

export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// All generated functions are num -> num, so generated programs are total.
export type Ty = 'num' | 'bool' | 'fun';

export class Gen {
  private scope: { name: string; ty: Ty }[] = [];
  readonly freeTypes = new Map<string, Ty>();
  private counter = 0;

  constructor(readonly rand: () => number) {}

  private freshName(): string {
    return `g${this.counter++}`;
  }

  private pick<T>(xs: readonly T[]): T {
    return xs[Math.floor(this.rand() * xs.length)];
  }

  // innermost binding wins, so shadowed names never leak with the wrong type
  private varsOf(ty: Ty): string[] {
    const innermost = new Map<string, Ty>();
    for (const s of this.scope) innermost.set(s.name, s.ty);
    return [...innermost].filter(([, t]) => t === ty).map(([n]) => n);
  }

  private binderName(): string {
    // occasionally shadow an existing in-scope name
    if (this.scope.length > 0 && this.rand() < 0.2) return this.pick(this.scope).name;
    return this.freshName();
  }

  genExpr(ty: Ty, depth: number, inLam: boolean): Surface {
    if (inLam && this.rand() < 0.04) {
      return { k: 'return', value: this.genExpr('num', Math.max(0, depth - 1), inLam) };
    }
    if (depth <= 0) return this.genLeaf(ty, inLam);
    const r = this.rand();
    if (r < 0.25) return this.genLeaf(ty, inLam);
    if (r < 0.4) return this.genLet(ty, depth, inLam);
    if (r < 0.55) {
      return {
        k: 'if',
        c: this.genExpr('bool', depth - 1, inLam),
        t: this.genExpr(ty, depth - 1, inLam),
        e: this.genExpr(ty, depth - 1, inLam),
      };
    }
    switch (ty) {
      case 'num': {
        if (this.rand() < 0.25) {
          return {
            k: 'app',
            fn: this.genExpr('fun', depth - 1, inLam),
            args: [this.genExpr('num', depth - 1, inLam)],
          };
        }
        return {
          k: 'prim',
          op: this.pick(['+', '-', '*'] as const),
          args: [this.genExpr('num', depth - 1, inLam), this.genExpr('num', depth - 1, inLam)],
        };
      }
      case 'bool': {
        const r2 = this.rand();
        if (r2 < 0.5) {
          return {
            k: 'prim',
            op: this.pick(['<', '='] as const),
            args: [this.genExpr('num', depth - 1, inLam), this.genExpr('num', depth - 1, inLam)],
          };
        }
        return { k: 'prim', op: 'not', args: [this.genExpr('bool', depth - 1, inLam)] };
      }
      case 'fun': {
        const p = this.binderName();
        this.scope.push({ name: p, ty: 'num' });
        const body = this.genExpr('num', depth - 1, true);
        this.scope.pop();
        return { k: 'lam', params: [p], body };
      }
    }
  }

  private genLeaf(ty: Ty, inLam: boolean): Surface {
    if (this.rand() < 0.15) {
      // introduce (or reuse) a free variable of this type
      const free = [...this.freeTypes].filter(([, t]) => t === ty).map(([n]) => n);
      if (free.length > 0 && this.rand() < 0.5) return { k: 'var', name: this.pick(free) };
      const n = this.freshName();
      this.freeTypes.set(n, ty);
      return { k: 'var', name: n };
    }
    const candidates = this.varsOf(ty);
    if (candidates.length > 0 && this.rand() < 0.7) return { k: 'var', name: this.pick(candidates) };
    switch (ty) {
      case 'num':
        return { k: 'lit', v: Math.floor(this.rand() * 21) - 10 };
      case 'bool':
        return { k: 'lit', v: this.rand() < 0.5 };
      case 'fun': {
        const p = this.binderName();
        this.scope.push({ name: p, ty: 'num' });
        const body = this.genExpr('num', 1, true);
        this.scope.pop();
        return { k: 'lam', params: [p], body };
      }
    }
  }

  private genLet(ty: Ty, depth: number, inLam: boolean): Surface {
    const vt = this.pick(['num', 'num', 'bool', 'fun'] as const);
    const n = this.binderName();
    const value = this.genExpr(vt, depth - 1, inLam);
    this.scope.push({ name: n, ty: vt });
    const body = this.genExpr(ty, depth - 1, inLam);
    this.scope.pop();
    return { k: 'let', defs: [[n, value]], body };
  }
}

export function genEnv(
  globals: Map<string, BindingId>,
  freeTypes: Map<string, Ty>,
  rand: () => number,
): Env {
  const env: Env = new Map();
  for (const [name, bind] of globals) {
    const ty = freeTypes.get(name) ?? 'num';
    switch (ty) {
      case 'num':
        env.set(bind, Math.floor(rand() * 21) - 10);
        break;
      case 'bool':
        env.set(bind, rand() < 0.5);
        break;
      case 'fun': {
        const k = Math.floor(rand() * 5);
        env.set(bind, {
          tag: 'native',
          name,
          fn: (v: Value) => {
            if (typeof v !== 'number') throw new EvalError('native: expected number');
            return v + k;
          },
        });
        break;
      }
    }
  }
  return env;
}
