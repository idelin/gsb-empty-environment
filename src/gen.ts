// Seedable random generator of well-typed surface expressions, used to
// property-test the refactorer. Names come from a tiny pool to force
// shadowing; `return` is only generated inside lambdas so programs run.

import type { S } from './resolve.ts';

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

export type Ty = 'num' | 'bool' | 'fn'; // fn := num -> num

interface EnvEntry {
  name: string;
  ty: Ty;
}

const NAMES = ['x', 'y', 'z', 'f', 'g'];

export function genExpr(
  rng: () => number,
  ty: Ty,
  env: EnvEntry[],
  depth: number,
  lamRet: Ty | null,
): S {
  const pick = <T>(xs: T[]): T => xs[Math.floor(rng() * xs.length)];
  // Innermost binding wins: a name is only visible with its nearest type.
  const visible = new Map<string, Ty>();
  for (const e of env) visible.set(e.name, e.ty);
  const ofType = (t: Ty) => [...visible.entries()].filter(([, ty]) => ty === t).map(([name]) => ({ name, ty: t }));
  const maybeReturn = (body: () => S): S => {
    if (lamRet === ty && rng() < 0.12) return { k: 'ret', v: body() };
    return body();
  };

  if (depth <= 0) {
    const vars = ofType(ty);
    if (vars.length > 0 && rng() < 0.7) return { k: 'var', n: pick(vars).name };
    if (ty === 'num') return { k: 'num', v: Math.floor(rng() * 10) };
    if (ty === 'bool') return { k: 'bool', v: rng() < 0.5 };
    // fn at depth 0: constant lambda
    return { k: 'lam', p: pick(NAMES), b: { k: 'num', v: Math.floor(rng() * 10) } };
  }

  const d = depth - 1;
  const genLet = (): S => {
    const vt: Ty = pick(['num', 'num', 'bool', 'fn'] as Ty[]);
    const n = pick(NAMES);
    return {
      k: 'let',
      n,
      v: genExpr(rng, vt, env, d, null),
      b: genExpr(rng, ty, [...env, { name: n, ty: vt }], d, lamRet),
    };
  };
  const genIf = (): S => ({
    k: 'if',
    c: genExpr(rng, 'bool', env, d, null),
    t: genExpr(rng, ty, env, d, lamRet),
    e: genExpr(rng, ty, env, d, lamRet),
  });

  switch (ty) {
    case 'num':
      return maybeReturn(() => {
        const vars = ofType('num');
        const fns = ofType('fn');
        const opts: S[] = [
          { k: 'num', v: Math.floor(rng() * 10) },
          {
            k: 'bin',
            op: pick(['add', 'sub', 'mul'] as const),
            l: genExpr(rng, 'num', env, d, null),
            r: genExpr(rng, 'num', env, d, null),
          },
          genLet(),
          genIf(),
        ];
        if (vars.length > 0) opts.push({ k: 'var', n: pick(vars).name });
        if (fns.length > 0) {
          opts.push({ k: 'app', f: { k: 'var', n: pick(fns).name }, a: [genExpr(rng, 'num', env, d, null)] });
        }
        return pick(opts);
      });
    case 'bool':
      return maybeReturn(() => {
        const vars = ofType('bool');
        const opts: S[] = [
          { k: 'bool', v: rng() < 0.5 },
          {
            k: 'bin',
            op: pick(['lt', 'eq'] as const),
            l: genExpr(rng, 'num', env, d, null),
            r: genExpr(rng, 'num', env, d, null),
          },
          {
            k: 'bin',
            op: pick(['and', 'or'] as const),
            l: genExpr(rng, 'bool', env, d, null),
            r: genExpr(rng, 'bool', env, d, null),
          },
          genLet(),
          genIf(),
        ];
        if (vars.length > 0) opts.push({ k: 'var', n: pick(vars).name });
        return pick(opts);
      });
    case 'fn': {
      const p = pick(NAMES);
      const body = genExpr(rng, 'num', [...env, { name: p, ty: 'num' }], d, 'num');
      return { k: 'lam', p, b: body };
    }
  }
}
