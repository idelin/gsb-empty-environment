/**
 * Domains: finite unions of intervals. Division by a denominator whose
 * range crosses zero either throws (mode 'reject') or splits the
 * denominator at zero (mode 'split'), producing one result part per
 * branch (typically unbounded rays). If the number of parts exceeds
 * `maxParts`, all parts are conservatively hulled into a single interval
 * and the result is flagged with precisionLoss plus an explanatory note.
 */

import { Dec } from './decimal.ts';
import * as Iv from './interval.ts';

export interface DomainResult {
  readonly parts: readonly Iv.Interval[];
  readonly precisionLoss: boolean;
  readonly note: string | null;
}

export interface DivOptions {
  readonly mode: 'reject' | 'split';
  readonly maxParts: number;
}

export class Domain {
  readonly parts: readonly Iv.Interval[];

  private constructor(parts: readonly Iv.Interval[]) {
    this.parts = parts;
  }

  static of(iv: Iv.Interval): Domain {
    return new Domain(Iv.isEmpty(iv) ? [] : [iv]);
  }

  static empty(): Domain {
    return new Domain([]);
  }

  hull(): Iv.Interval {
    let acc: Iv.Interval = Iv.EMPTY;
    for (const p of this.parts) acc = Iv.hull(acc, p);
    return acc;
  }

  contains(d: Dec): boolean {
    return this.parts.some((p) => Iv.containsValue(p, d));
  }

  intersect(other: Domain): Domain {
    const out: Iv.Interval[] = [];
    for (const a of this.parts) {
      for (const b of other.parts) {
        const i = Iv.intersect(a, b);
        if (!Iv.isEmpty(i)) out.push(i);
      }
    }
    return new Domain(out);
  }

  union(other: Domain, maxParts: number): DomainResult {
    return capParts([...this.parts, ...other.parts], maxParts);
  }

  div(other: Domain, prec: number, opts: DivOptions): DomainResult {
    const out: Iv.Interval[] = [];
    for (const a of this.parts) {
      for (const b of other.parts) {
        const segs = splitAtZero(b);
        if (segs.length > 1 && opts.mode === 'reject') {
          throw new Iv.ZeroDivisionError('Domain.div: denominator crosses zero (reject mode)');
        }
        for (const s of segs) {
          const q = Iv.div(a, s, prec);
          if (!Iv.isEmpty(q)) out.push(q);
        }
      }
    }
    return capParts(out, opts.maxParts);
  }

  toString(): string {
    return this.parts.length === 0 ? '∅' : this.parts.map(Iv.toString).join(' ∪ ');
  }
}

/** Split a range at zero when zero lies strictly inside; zero endpoints stay. */
function splitAtZero(b: Iv.Interval): Iv.Interval[] {
  if (b.kind === 'unknown') return [Iv.range(null, Dec.zero), Iv.range(Dec.zero, null)];
  if (b.kind !== 'range') return [];
  const loNeg = b.lo === null || Dec.sign(b.lo) < 0;
  const hiPos = b.hi === null || Dec.sign(b.hi) > 0;
  if (loNeg && hiPos) return [Iv.range(b.lo, Dec.zero), Iv.range(Dec.zero, b.hi)];
  return [b];
}

function capParts(parts: Iv.Interval[], maxParts: number): DomainResult {
  if (parts.length <= maxParts) return { parts, precisionLoss: false, note: null };
  let acc: Iv.Interval = Iv.EMPTY;
  for (const p of parts) acc = Iv.hull(acc, p);
  return {
    parts: [acc],
    precisionLoss: true,
    note: `hulled ${parts.length} parts into 1 (cap ${maxParts}); precision lost`,
  };
}
