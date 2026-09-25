import { Rational, formatRational, type RationalInput } from './rational.ts';
import { UnitMigrationError } from './errors.ts';

export interface UnitDecl {
  name: string;
  /** Dimension string, e.g. "length", "temperature". Conversions never cross dimensions. */
  dimension: string;
}

/**
 * A directed affine conversion: a value `x` in unit `from` equals
 * `x * ratio + offset` in unit `to` (for absolute quantities; for delta
 * quantities the offset is dropped, see `convert`).
 */
export interface ConversionDecl {
  from: string;
  to: string;
  ratio: RationalInput;
  offset?: RationalInput;
}

export interface Affine {
  ratio: Rational;
  offset: Rational;
}

/**
 * Absolute quantities sit on an affine scale (20 deg C = 293.15 K);
 * delta quantities are differences and only scale (delta 20 deg C = delta 20 K,
 * but delta 20 deg C = delta 36 deg F).
 */
export type QuantityKind = 'absolute' | 'delta';

interface Edge {
  from: string;
  to: string;
  ratio: Rational;
  offset: Rational;
}

const IDENTITY: Affine = { ratio: Rational.of(1n), offset: Rational.of(0n) };

/**
 * A compiled unit registry. Construction performs the full compile check:
 * every unit of a dimension must be connected, every ratio invertible, and
 * every redundant conversion must agree with the composed affine of the
 * others — otherwise a contradictory conversion cycle is reported.
 */
export class UnitRegistry {
  readonly units: ReadonlyMap<string, UnitDecl>;
  readonly #toBase = new Map<string, Affine>();
  readonly #parent = new Map<string, string>();

  constructor(unitDecls: UnitDecl[], conversions: ConversionDecl[]) {
    const units = new Map<string, UnitDecl>();
    for (const u of unitDecls) {
      if (units.has(u.name)) {
        throw new UnitMigrationError('COMPILE_UNIT', `duplicate unit "${u.name}"`);
      }
      units.set(u.name, u);
    }
    this.units = units;

    const edgesByDimension = new Map<string, Edge[]>();
    for (const c of conversions) {
      const from = units.get(c.from);
      const to = units.get(c.to);
      if (!from) {
        throw new UnitMigrationError('COMPILE_UNIT', `conversion references unknown unit "${c.from}"`);
      }
      if (!to) {
        throw new UnitMigrationError('COMPILE_UNIT', `conversion references unknown unit "${c.to}"`);
      }
      if (c.from === c.to) {
        throw new UnitMigrationError('COMPILE_UNIT', `self conversion on unit "${c.from}"`);
      }
      if (from.dimension !== to.dimension) {
        throw new UnitMigrationError(
          'COMPILE_UNIT',
          `dimension mismatch: "${c.from}" (${from.dimension}) -> "${c.to}" (${to.dimension})`,
        );
      }
      const ratio = Rational.of(c.ratio);
      if (ratio.isZero) {
        throw new UnitMigrationError(
          'COMPILE_UNIT',
          `conversion ${c.from} -> ${c.to} has zero ratio and is not invertible`,
        );
      }
      const offset = c.offset === undefined ? Rational.of(0n) : Rational.of(c.offset);
      const list = edgesByDimension.get(from.dimension) ?? [];
      list.push({ from: c.from, to: c.to, ratio, offset });
      edgesByDimension.set(from.dimension, list);
    }

    const unitsByDimension = new Map<string, string[]>();
    for (const u of units.values()) {
      const list = unitsByDimension.get(u.dimension) ?? [];
      list.push(u.name);
      unitsByDimension.set(u.dimension, list);
    }

    for (const [dimension, names] of unitsByDimension) {
      this.#compileDimension(dimension, names, edgesByDimension.get(dimension) ?? []);
    }
  }

  #compileDimension(dimension: string, names: string[], edges: Edge[]): void {
    if (names.length > 1 && edges.length === 0) {
      throw new UnitMigrationError(
        'COMPILE_UNIT',
        `dimension "${dimension}" has ${names.length} units but no conversions`,
      );
    }
    const adjacency = new Map<string, Edge[]>();
    for (const e of edges) {
      const push = (unit: string, edge: Edge): void => {
        const list = adjacency.get(unit) ?? [];
        list.push(edge);
        adjacency.set(unit, list);
      };
      // Forward: to = from * r + o. Backward: from = to * (1/r) + (-o/r).
      push(e.from, e);
      push(e.to, { from: e.to, to: e.from, ratio: e.ratio.inv(), offset: e.offset.neg().div(e.ratio) });
    }

    const base = names[0];
    this.#toBase.set(base, IDENTITY);
    const queue = [base];
    while (queue.length > 0) {
      const u = queue.shift() as string;
      const toBaseU = this.#toBase.get(u) as Affine;
      for (const edge of adjacency.get(u) ?? []) {
        const v = edge.to;
        const known = this.#toBase.get(v);
        if (!known) {
          // v = u * r + o and base = u * ru + ou
          //   => base = v * (ru / r) + (ou - o * ru / r)
          const ratio = toBaseU.ratio.div(edge.ratio);
          const offset = toBaseU.offset.sub(edge.offset.mul(toBaseU.ratio).div(edge.ratio));
          this.#toBase.set(v, { ratio, offset });
          this.#parent.set(v, u);
          queue.push(v);
        } else {
          // Both endpoints known: the edge must agree with the composed
          // affines, i.e. v = u * r + o must hold with
          //   r = ru / rv and o = (ou - ov) / rv.
          const impliedRatio = toBaseU.ratio.div(known.ratio);
          const impliedOffset = toBaseU.offset.sub(known.offset).div(known.ratio);
          if (!impliedRatio.equals(edge.ratio) || !impliedOffset.equals(edge.offset)) {
            const cycle = this.#cycleUnits(u, v);
            throw new UnitMigrationError(
              'COMPILE_CYCLE',
              `contradictory conversion cycle in dimension "${dimension}": ` +
                `${cycle.join(' -> ')}; edge ${edge.from} -> ${edge.to} claims ` +
                `ratio ${formatRational(edge.ratio)} offset ${formatRational(edge.offset)} but the ` +
                `composed conversions imply ratio ${formatRational(impliedRatio)} ` +
                `offset ${formatRational(impliedOffset)}`,
              { details: { dimension, cycle, edge: { from: edge.from, to: edge.to } } },
            );
          }
        }
      }
    }

    for (const name of names) {
      if (!this.#toBase.has(name)) {
        throw new UnitMigrationError(
          'COMPILE_UNIT',
          `unit "${name}" has no conversion path to unit "${base}" in dimension "${dimension}"`,
        );
      }
    }
  }

  /** Units on the cycle that a conflicting edge closes, for error reporting. */
  #cycleUnits(u: string, v: string): string[] {
    const ancestorsOf = (start: string): string[] => {
      const out = [start];
      let cur = start;
      while (this.#parent.has(cur)) {
        cur = this.#parent.get(cur) as string;
        out.push(cur);
      }
      return out;
    };
    const ancU = ancestorsOf(u);
    const ancV = ancestorsOf(v);
    const indexInV = new Map(ancV.map((name, i) => [name, i]));
    const lcaIndexU = ancU.findIndex((name) => indexInV.has(name));
    const lca = ancU[lcaIndexU];
    const up = ancU.slice(0, lcaIndexU + 1);
    const down = ancV.slice(0, indexInV.get(lca)).reverse();
    return [...up, ...down, u];
  }

  /**
   * The composed exact affine `to = from * ratio + offset` between two units
   * of the same dimension. Throws NO_CONVERSION across dimensions or for
   * unknown units.
   */
  converter(from: string, to: string): Affine {
    const fromUnit = this.units.get(from);
    const toUnit = this.units.get(to);
    if (!fromUnit) {
      throw new UnitMigrationError('NO_CONVERSION', `unknown unit "${from}"`);
    }
    if (!toUnit) {
      throw new UnitMigrationError('NO_CONVERSION', `unknown unit "${to}"`);
    }
    if (fromUnit.dimension !== toUnit.dimension) {
      throw new UnitMigrationError(
        'NO_CONVERSION',
        `no conversion from "${from}" (${fromUnit.dimension}) to "${to}" (${toUnit.dimension}): different dimensions`,
      );
    }
    if (from === to) return IDENTITY;
    const a = this.#toBase.get(from) as Affine;
    const b = this.#toBase.get(to) as Affine;
    // base = from * a.ratio + a.offset; to = (base - b.offset) / b.ratio
    return {
      ratio: a.ratio.div(b.ratio),
      offset: a.offset.sub(b.offset).div(b.ratio),
    };
  }

  /**
   * Converts a value between units, honouring the quantity kind: the affine
   * offset applies only to absolute quantities.
   */
  convert(value: RationalInput, from: string, to: string, quantity: QuantityKind): Rational {
    const affine = this.converter(from, to);
    const x = Rational.of(value);
    return quantity === 'absolute' ? x.mul(affine.ratio).add(affine.offset) : x.mul(affine.ratio);
  }
}
