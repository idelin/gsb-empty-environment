import { Rat } from './rational.ts';

// Rounding is always explicit: a mode plus a positive rational quantum.
// Where rounding happens (per segment vs rule total vs final total) is part
// of the ladder configuration and is never reordered implicitly.
export type RoundingMode =
  | 'floor'      // toward -Infinity
  | 'ceil'       // toward +Infinity
  | 'truncate'   // toward zero
  | 'half-up'    // ties toward +Infinity
  | 'half-down'  // ties toward -Infinity
  | 'half-even'; // ties to even multiple of the quantum

export interface RoundingSpec {
  quantum: Rat;
  mode: RoundingMode;
}

export function roundTo(value: Rat, spec: RoundingSpec): Rat {
  if (spec.quantum.lte(Rat.zero())) throw new Error('roundTo: quantum must be positive');
  const k = roundRationalToInt(value.div(spec.quantum), spec.mode);
  return spec.quantum.mul(Rat.of(k));
}

function roundRationalToInt(v: Rat, mode: RoundingMode): bigint {
  const fl = v.floor();
  const rem = v.sub(Rat.of(fl)); // in [0, 1)
  if (rem.isZero()) return fl;
  switch (mode) {
    case 'floor': return fl;
    case 'ceil': return fl + 1n;
    case 'truncate': return v.sign() >= 0 ? fl : fl + 1n;
    case 'half-up': {
      const twice = rem.mul(Rat.of(2n));
      return twice.gte(Rat.one()) ? fl + 1n : fl;
    }
    case 'half-down': {
      const twice = rem.mul(Rat.of(2n));
      return twice.gt(Rat.one()) ? fl + 1n : fl;
    }
    case 'half-even': {
      const twice = rem.mul(Rat.of(2n));
      if (twice.gt(Rat.one())) return fl + 1n;
      if (twice.lt(Rat.one())) return fl;
      return fl % 2n === 0n ? fl : fl + 1n;
    }
  }
}
