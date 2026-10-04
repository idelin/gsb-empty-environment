// Bit-vector circuits over CNF literals. Vectors are LSB-first.
// All operators truncate to the vector width, matching the interpreter.

import { CnfBuilder } from './cnf.ts';
import type { Lit } from './cnf.ts';

export type Bv = Lit[];

export function bvConst(b: CnfBuilder, value: bigint, width: number): Bv {
  const out: Bv = new Array(width);
  for (let i = 0; i < width; i++) {
    out[i] = ((value >> BigInt(i)) & 1n) === 1n ? b.trueLit : b.falseLit;
  }
  return out;
}

export function bvNot(b: CnfBuilder, x: Bv): Bv {
  return x.map((l) => -l);
}

const zipOp = (f: (a: Lit, c: Lit) => Lit) =>
  (b: CnfBuilder, x: Bv, y: Bv): Bv => x.map((l, i) => f(l, y[i]));

export const bvAnd: (b: CnfBuilder, x: Bv, y: Bv) => Bv = (b, x, y) =>
  zipOp((p, q) => b.and(p, q))(b, x, y);
export const bvOr: (b: CnfBuilder, x: Bv, y: Bv) => Bv = (b, x, y) =>
  zipOp((p, q) => b.or(p, q))(b, x, y);
export const bvXor: (b: CnfBuilder, x: Bv, y: Bv) => Bv = (b, x, y) =>
  zipOp((p, q) => b.xor(p, q))(b, x, y);

export function bvAdd(b: CnfBuilder, x: Bv, y: Bv, carryIn: Lit): Bv {
  const out: Bv = new Array(x.length);
  let carry = carryIn;
  for (let i = 0; i < x.length; i++) {
    const xi = x[i];
    const yi = y[i];
    const xxy = b.xor(xi, yi);
    out[i] = b.xor(xxy, carry);
    // majority(xi, yi, carry)
    carry = b.or(b.and(xi, yi), b.and(carry, xxy));
  }
  return out; // final carry dropped: truncation to width
}

export function bvSub(b: CnfBuilder, x: Bv, y: Bv): Bv {
  return bvAdd(b, x, bvNot(b, y), b.trueLit);
}

export function bvNeg(b: CnfBuilder, x: Bv): Bv {
  return bvAdd(b, bvNot(b, x), bvConst(b, 1n, x.length), b.falseLit);
}

export function bvIte(b: CnfBuilder, c: Lit, t: Bv, e: Bv): Bv {
  return t.map((l, i) => b.mux(c, l, e[i]));
}

export function bvIsNonzero(b: CnfBuilder, x: Bv): Lit {
  let acc: Lit = b.falseLit;
  for (const l of x) acc = b.or(acc, l);
  return acc;
}

// Barrel shift. Semantics (matching the interpreter): amounts >= width yield
// 0. Shifts in [width, 2^stages) produce 0 naturally; amounts with bits at or
// above `stages` are >= 2^stages >= width and are forced to 0 by the guard.
function barrelShift(b: CnfBuilder, x: Bv, amt: Bv, left: boolean): Bv {
  const width = x.length;
  const stages = Math.max(0, Math.ceil(Math.log2(width)));
  let cur = x.slice();
  for (let k = 0; k < stages; k++) {
    const step = 1 << k;
    const sel = amt[k];
    const shifted: Bv = new Array(width);
    for (let i = 0; i < width; i++) {
      const src = left ? i - step : i + step;
      shifted[i] = src >= 0 && src < width ? cur[src] : b.falseLit;
    }
    cur = cur.map((l, i) => b.mux(sel, shifted[i], l));
  }
  let guard: Lit = b.falseLit;
  for (let i = stages; i < amt.length; i++) guard = b.or(guard, amt[i]);
  return cur.map((l) => b.mux(guard, b.falseLit, l));
}

export function bvShl(b: CnfBuilder, x: Bv, amt: Bv): Bv {
  return barrelShift(b, x, amt, true);
}

export function bvShr(b: CnfBuilder, x: Bv, amt: Bv): Bv {
  return barrelShift(b, x, amt, false);
}
