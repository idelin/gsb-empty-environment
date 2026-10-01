import { ContradictoryConversionError } from './errors.ts';
import { Rational } from './rational.ts';

/**
 * An affine conversion between two units:  y = m * x + c.
 * For pure ratio conversions c = 0. Delta quantities always use only `m`.
 */
export interface Affine {
  readonly m: Rational;
  readonly c: Rational;
}

const IDENTITY: Affine = { m: Rational.ONE, c: Rational.ZERO };

/** Apply g first, then f: (f ∘ g)(x) = f.m*g.m * x + (f.m*g.c + f.c). */
function compose(f: Affine, g: Affine): Affine {
  return {
    m: f.m.mul(g.m),
    c: f.m.mul(g.c).add(f.c),
  };
}

function invert(a: Affine): Affine {
  return { m: Rational.ONE.div(a.m), c: a.c.neg().div(a.m) };
}

function affineEquals(a: Affine, b: Affine): boolean {
  return a.m.equals(b.m) && a.c.equals(b.c);
}

/**
 * Directed conversion graph over unit symbols. Every edge is stored together
 * with its exact inverse. Adding an edge that closes a cycle is validated:
 * if the graph already implies a conversion between the two endpoints and
 * the new edge disagrees exactly, it is rejected as contradictory.
 */
export class ConversionGraph {
  private readonly edges = new Map<string, Map<string, Affine>>();

  addUnit(symbol: string): void {
    if (!this.edges.has(symbol)) this.edges.set(symbol, new Map());
  }

  hasUnit(symbol: string): boolean {
    return this.edges.has(symbol);
  }

  /**
   * Register `to = from * m + c` (exact rationals). The reverse edge is
   * derived automatically. Throws ContradictoryConversionError when an
   * existing path between the endpoints implies a different conversion.
   */
  addConversion(from: string, to: string, m: Rational, c: Rational = Rational.ZERO): void {
    if (m.isZero()) throw new RangeError('conversion factor must be non-zero');
    this.addUnit(from);
    this.addUnit(to);
    const edge: Affine = { m, c };
    const existing = this.findConversion(from, to);
    if (existing !== null) {
      if (!affineEquals(existing, edge)) {
        throw new ContradictoryConversionError(
          `contradictory conversion edge ${from} -> ${to}: ` +
            `graph implies y = ${existing.m.toString()}*x + ${existing.c.toString()}, ` +
            `new edge claims y = ${m.toString()}*x + ${c.toString()}`,
        );
      }
      return; // consistent duplicate: nothing to add
    }
    this.edges.get(from)!.set(to, edge);
    this.edges.get(to)!.set(from, invert(edge));
  }

  /**
   * The conversion implied by the graph between two units, or null if they
   * are not connected. BFS composes affine maps along the shortest path.
   */
  findConversion(from: string, to: string): Affine | null {
    if (!this.edges.has(from) || !this.edges.has(to)) return null;
    if (from === to) return IDENTITY;
    const visited = new Map<string, Affine>([[from, IDENTITY]]);
    const queue: string[] = [from];
    while (queue.length > 0) {
      const node = queue.shift()!;
      const soFar = visited.get(node)!;
      for (const [next, edge] of this.edges.get(node)!) {
        if (visited.has(next)) continue;
        const composed = compose(edge, soFar);
        if (next === to) return composed;
        visited.set(next, composed);
        queue.push(next);
      }
    }
    return null;
  }

  /** Convert an absolute value (full affine map). */
  convertAbsolute(value: Rational, from: string, to: string): Rational {
    const conv = this.requireConversion(from, to);
    return conv.m.mul(value).add(conv.c);
  }

  /** Convert a delta value (ratio only, offset discarded). */
  convertDelta(value: Rational, from: string, to: string): Rational {
    const conv = this.requireConversion(from, to);
    return conv.m.mul(value);
  }

  private requireConversion(from: string, to: string): Affine {
    const conv = this.findConversion(from, to);
    if (conv === null) {
      throw new ContradictoryConversionError(`no conversion path from ${from} to ${to}`);
    }
    return conv;
  }
}
