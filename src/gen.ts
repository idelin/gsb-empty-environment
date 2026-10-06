import { Type, tvar, tcon, tfun, tlist } from "./types";
import { Subst, applySubst } from "./subst";
import { Constraint } from "./unify";

/** Deterministic PRNG (mulberry32) so generated tests are reproducible. */
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

export interface GenState {
  nextVarId: number;
}

const CONSTANTS = ["Int", "Bool", "String"];

function pick<T>(rng: () => number, xs: readonly T[]): T {
  return xs[Math.floor(rng() * xs.length)]!;
}

/**
 * A random type of bounded depth. Variables are drawn from `pool`;
 * with some probability a fresh variable is allocated, added to the
 * pool, and used.
 */
export function randomType(rng: () => number, st: GenState, pool: number[], depth: number): Type {
  const roll = rng();
  if (depth <= 0 || roll < 0.35) {
    if (pool.length > 0 && (roll < 0.25 || rng() < 0.8)) {
      return tvar(pick(rng, pool));
    }
    return tcon(pick(rng, CONSTANTS));
  }
  if (roll < 0.5) {
    const fresh = st.nextVarId++;
    pool.push(fresh);
    return tvar(fresh);
  }
  if (roll < 0.75) {
    return tfun(randomType(rng, st, pool, depth - 1), randomType(rng, st, pool, depth - 1));
  }
  return tlist(randomType(rng, st, pool, depth - 1));
}

export interface GeneratedSystem {
  readonly constraints: Constraint[];
  /** The known substitution the constraints were built from: a solution by construction. */
  readonly known: Subst;
  /** Variables the known substitution grounds. */
  readonly knownVars: readonly number[];
}

/**
 * Generate constraints that are satisfiable by construction.
 *
 * 1. Build an acyclic "known" substitution sigma: each variable is bound
 *    to a random type over strictly earlier variables (or ground types).
 * 2. For each constraint pick a random type `t` over those variables and
 *    emit `t ~ applySubst(sigma, t)`. Because applySubst normalises
 *    transitively, sigma maps both sides to the same normal form, so
 *    sigma is a solution.
 */
export function genSatisfiable(seed: number, varCount: number, constraintCount: number): GeneratedSystem {
  const rng = mulberry32(seed);
  const st: GenState = { nextVarId: varCount };
  const knownVars: number[] = [];
  for (let i = 0; i < varCount; i++) knownVars.push(i);

  const known = new Map<number, Type>();
  for (let i = 0; i < varCount; i++) {
    // Only earlier variables may appear, keeping the substitution acyclic.
    const earlier = knownVars.slice(0, i);
    known.set(i, randomType(rng, st, [...earlier], 2));
  }

  const constraints: Constraint[] = [];
  for (let n = 0; n < constraintCount; n++) {
    const left = randomType(rng, st, [...knownVars], 3);
    const right = applySubst(known, left);
    constraints.push({ left, right });
  }
  return { constraints, known, knownVars };
}
