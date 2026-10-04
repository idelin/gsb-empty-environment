// Bit-blasting: compile an expression into a bit-vector circuit (CNF).

import type { Expr } from './ast.ts';
import { CnfBuilder } from './cnf.ts';
import {
  bvAdd, bvAnd, bvConst, bvIsNonzero, bvIte, bvNeg, bvNot, bvOr, bvShl,
  bvShr, bvSub, bvXor,
} from './bv.ts';
import type { Bv } from './bv.ts';

export function bitblast(
  e: Expr,
  b: CnfBuilder,
  inputs: ReadonlyMap<string, Bv>,
  width: number,
): Bv {
  switch (e.kind) {
    case 'const':
      return bvConst(b, e.value, width);
    case 'var': {
      const bv = inputs.get(e.name);
      if (!bv) throw new Error(`no input bits for variable '${e.name}'`);
      return bv;
    }
    case 'not':
      return bvNot(b, bitblast(e.a, b, inputs, width));
    case 'neg':
      return bvNeg(b, bitblast(e.a, b, inputs, width));
    case 'ite': {
      const c = bvIsNonzero(b, bitblast(e.c, b, inputs, width));
      const t = bitblast(e.t, b, inputs, width);
      const f = bitblast(e.e, b, inputs, width);
      return bvIte(b, c, t, f);
    }
    case 'bin': {
      const x = bitblast(e.a, b, inputs, width);
      const y = bitblast(e.b, b, inputs, width);
      switch (e.op) {
        case 'and': return bvAnd(b, x, y);
        case 'or': return bvOr(b, x, y);
        case 'xor': return bvXor(b, x, y);
        case 'add': return bvAdd(b, x, y, b.falseLit);
        case 'sub': return bvSub(b, x, y);
        case 'shl': return bvShl(b, x, y);
        case 'shr': return bvShr(b, x, y);
      }
    }
  }
}
