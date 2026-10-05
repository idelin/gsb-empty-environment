/**
 * Bit-blaster: compiles bit-vector expressions into CNF constraints inside a
 * `SatSolver` (Tseitin encoding with constant folding).
 *
 * Bit-vectors are represented as arrays of SAT literals, LSB first. Literal
 * `trueLit` / `falseLit` are the two constant literals; `trueLit` is pinned
 * to true by a unit clause.
 */

import type { Expr } from './expr.ts';
import { SatSolver } from './sat.ts';

export type Lit = number;
export type BitVec = Lit[];

export class BitBlaster {
  readonly solver: SatSolver;
  readonly trueLit: Lit;
  readonly falseLit: Lit;

  constructor(solver: SatSolver) {
    this.solver = solver;
    this.trueLit = solver.newVar();
    solver.addClause([this.trueLit]);
    this.falseLit = -this.trueLit;
  }

  // --- gates ---------------------------------------------------------------

  not(a: Lit): Lit {
    return -a;
  }

  and(a: Lit, b: Lit): Lit {
    if (a === this.falseLit || b === this.falseLit) return this.falseLit;
    if (a === this.trueLit) return b;
    if (b === this.trueLit) return a;
    if (a === b) return a;
    if (a === -b) return this.falseLit;
    const o = this.solver.newVar();
    this.solver.addClause([-a, -b, o]);
    this.solver.addClause([a, -o]);
    this.solver.addClause([b, -o]);
    return o;
  }

  or(a: Lit, b: Lit): Lit {
    if (a === this.trueLit || b === this.trueLit) return this.trueLit;
    if (a === this.falseLit) return b;
    if (b === this.falseLit) return a;
    if (a === b) return a;
    if (a === -b) return this.trueLit;
    const o = this.solver.newVar();
    this.solver.addClause([a, b, -o]);
    this.solver.addClause([-a, o]);
    this.solver.addClause([-b, o]);
    return o;
  }

  xor(a: Lit, b: Lit): Lit {
    if (a === this.falseLit) return b;
    if (b === this.falseLit) return a;
    if (a === this.trueLit) return -b;
    if (b === this.trueLit) return -a;
    if (a === b) return this.falseLit;
    if (a === -b) return this.trueLit;
    const o = this.solver.newVar();
    this.solver.addClause([-a, -b, -o]);
    this.solver.addClause([a, b, -o]);
    this.solver.addClause([a, -b, o]);
    this.solver.addClause([-a, b, o]);
    return o;
  }

  /** s ? t : e (single bit). */
  mux(s: Lit, t: Lit, e: Lit): Lit {
    if (s === this.trueLit) return t;
    if (s === this.falseLit) return e;
    if (t === e) return t;
    if (t === this.trueLit && e === this.falseLit) return s;
    if (t === this.falseLit && e === this.trueLit) return -s;
    const o = this.solver.newVar();
    this.solver.addClause([-s, -t, o]); //  s & t  =>  o
    this.solver.addClause([s, -e, o]); //  ~s & e =>  o
    this.solver.addClause([-s, t, -o]); // s & ~t => ~o
    this.solver.addClause([s, e, -o]); //  ~s & ~e => ~o
    return o;
  }

  // --- bit-vector constructors ----------------------------------------------

  constVec(value: bigint, width: number): BitVec {
    const bits: BitVec = [];
    for (let i = 0; i < width; i++) {
      bits.push(((value >> BigInt(i)) & 1n) === 1n ? this.trueLit : this.falseLit);
    }
    return bits;
  }

  // --- bit-vector operations (LSB-first arrays, uniform width) ---------------

  bvNot(a: BitVec): BitVec {
    return a.map((bit) => -bit);
  }

  bvAnd(a: BitVec, b: BitVec): BitVec {
    return a.map((bit, i) => this.and(bit, b[i]));
  }

  bvOr(a: BitVec, b: BitVec): BitVec {
    return a.map((bit, i) => this.or(bit, b[i]));
  }

  bvXor(a: BitVec, b: BitVec): BitVec {
    return a.map((bit, i) => this.xor(bit, b[i]));
  }

  private bvAddCarry(a: BitVec, b: BitVec, carryIn: Lit): BitVec {
    let carry = carryIn;
    const out: BitVec = [];
    for (let i = 0; i < a.length; i++) {
      const axb = this.xor(a[i], b[i]);
      out.push(this.xor(axb, carry));
      carry = this.or(this.and(a[i], b[i]), this.and(carry, axb));
    }
    // The final carry-out is dropped: results are truncated to the width.
    return out;
  }

  bvAdd(a: BitVec, b: BitVec): BitVec {
    return this.bvAddCarry(a, b, this.falseLit);
  }

  bvSub(a: BitVec, b: BitVec): BitVec {
    return this.bvAddCarry(a, this.bvNot(b), this.trueLit);
  }

  /** Unsigned comparison: a >= c, where c is a constant. */
  geqConst(a: BitVec, c: bigint): Lit {
    let gt = this.falseLit;
    let eq = this.trueLit;
    for (let i = a.length - 1; i >= 0; i--) {
      const bit = (c >> BigInt(i)) & 1n;
      if (bit === 0n) {
        gt = this.or(gt, this.and(eq, a[i]));
        eq = this.and(eq, -a[i]);
      } else {
        eq = this.and(eq, a[i]);
      }
    }
    return this.or(gt, eq);
  }

  private static shiftLeftConst(a: BitVec, k: number, fill: Lit): BitVec {
    const w = a.length;
    const out: BitVec = new Array(w);
    for (let i = 0; i < w; i++) out[i] = i - k >= 0 ? a[i - k] : fill;
    return out;
  }

  private static shiftRightConst(a: BitVec, k: number, fill: Lit): BitVec {
    const w = a.length;
    const out: BitVec = new Array(w);
    for (let i = 0; i < w; i++) out[i] = i + k < w ? a[i + k] : fill;
    return out;
  }

  /**
   * Barrel shift by a variable amount. Any shift amount >= width yields zero
   * (matching the interpreter's fixed semantics), enforced by the final mux.
   */
  private bvShift(
    a: BitVec,
    shamt: BitVec,
    direction: 'left' | 'right',
    arithmetic: boolean,
  ): BitVec {
    const w = a.length;
    let cur = a.slice();
    for (let k = 0; 1 << k < w; k++) {
      const fill =
        direction === 'left' || !arithmetic ? this.falseLit : cur[w - 1];
      const shifted =
        direction === 'left'
          ? BitBlaster.shiftLeftConst(cur, 1 << k, fill)
          : BitBlaster.shiftRightConst(cur, 1 << k, fill);
      const stage = shamt[k];
      cur = cur.map((bit, i) => this.mux(stage, shifted[i], bit));
    }
    const tooBig = this.geqConst(shamt, BigInt(w));
    return cur.map((bit) => this.mux(tooBig, this.falseLit, bit));
  }

  bvShl(a: BitVec, shamt: BitVec): BitVec {
    return this.bvShift(a, shamt, 'left', false);
  }

  bvLshr(a: BitVec, shamt: BitVec): BitVec {
    return this.bvShift(a, shamt, 'right', false);
  }

  bvAshr(a: BitVec, shamt: BitVec): BitVec {
    return this.bvShift(a, shamt, 'right', true);
  }

  /** Condition is "nonzero": any set bit in `cond` selects `then`. */
  bvIte(cond: BitVec, then: BitVec, otherwise: BitVec): BitVec {
    let nz = this.falseLit;
    for (const bit of cond) nz = this.or(nz, bit);
    return then.map((bit, i) => this.mux(nz, bit, otherwise[i]));
  }

  // --- expression compilation -------------------------------------------------

  compile(e: Expr, env: ReadonlyMap<string, BitVec>, width: number): BitVec {
    switch (e.kind) {
      case 'var': {
        const bits = env.get(e.name);
        if (!bits) throw new Error(`unbound variable: ${e.name}`);
        return bits;
      }
      case 'const':
        return this.constVec(e.value, width);
      case 'not':
        return this.bvNot(this.compile(e.arg, env, width));
      case 'ite':
        return this.bvIte(
          this.compile(e.cond, env, width),
          this.compile(e.then, env, width),
          this.compile(e.otherwise, env, width),
        );
      case 'bin': {
        const lhs = this.compile(e.lhs, env, width);
        const rhs = this.compile(e.rhs, env, width);
        switch (e.op) {
          case 'and':
            return this.bvAnd(lhs, rhs);
          case 'or':
            return this.bvOr(lhs, rhs);
          case 'xor':
            return this.bvXor(lhs, rhs);
          case 'add':
            return this.bvAdd(lhs, rhs);
          case 'sub':
            return this.bvSub(lhs, rhs);
          case 'shl':
            return this.bvShl(lhs, rhs);
          case 'lshr':
            return this.bvLshr(lhs, rhs);
          case 'ashr':
            return this.bvAshr(lhs, rhs);
        }
      }
    }
  }
}
