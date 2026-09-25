/**
 * Unit registry. Every unit maps affinely to its dimension's canonical base:
 *
 *     baseValue = scale * value + offset
 *
 * `kind` distinguishes absolute quantities (e.g. a temperature reading)
 * from difference quantities (e.g. a temperature delta). A difference unit
 * must have zero offset. Conversion between mismatched kinds is always
 * rejected: an absolute reading and a delta are different types, even
 * where the numeric offsets happen to coincide.
 */

import { Rational } from './rational.ts';

export type QuantityKind = 'absolute' | 'difference';

export interface UnitDef {
  readonly name: string;
  readonly dimension: string;
  readonly kind: QuantityKind;
  readonly scale: Rational;
  readonly offset: Rational;
}

export interface InlineUnitSpec {
  readonly name?: string;
  readonly dimension: string;
  readonly kind?: QuantityKind;
  readonly scale: string | number | Rational;
  readonly offset?: string | number | Rational;
}

export type UnitRef = string | InlineUnitSpec;

export function defineUnit(spec: InlineUnitSpec & { name: string }): UnitDef {
  const kind = spec.kind ?? 'difference';
  const scale = Rational.from(spec.scale);
  const offset = spec.offset === undefined ? Rational.zero() : Rational.from(spec.offset);
  if (scale.isZero()) throw new Error(`unit ${spec.name}: scale must be non-zero`);
  if (kind === 'difference' && !offset.isZero()) {
    throw new Error(`unit ${spec.name}: difference units must have zero offset`);
  }
  return { name: spec.name, dimension: spec.dimension, kind, scale, offset };
}

const registry = new Map<string, UnitDef>();

export function registerUnit(spec: InlineUnitSpec & { name: string }): UnitDef {
  const unit = defineUnit(spec);
  registry.set(unit.name, unit);
  return unit;
}

export function getUnit(name: string): UnitDef | undefined {
  return registry.get(name);
}

/** Resolve a unit reference (registered name or inline definition). */
export function resolveUnit(ref: UnitRef): UnitDef {
  if (typeof ref !== 'string') {
    const kind = ref.kind ?? 'difference';
    return defineUnit({ ...ref, kind, name: ref.name ?? '<inline>' });
  }
  const unit = registry.get(ref);
  if (!unit) throw new Error(`unknown unit: ${JSON.stringify(ref)}`);
  return unit;
}

/** Convert an exact rational value between two resolved units. */
export function convertRational(value: Rational, from: UnitDef, to: UnitDef): Rational {
  const compat = checkCompatible(from, to);
  if (compat !== null) throw new Error(compat);
  const base = from.scale.mul(value).add(from.offset);
  return base.sub(to.offset).div(to.scale);
}

/**
 * Returns null when conversion is physically meaningful, else an explanation.
 * Dimensions must match and quantity kinds must be identical.
 */
export function checkCompatible(from: UnitDef, to: UnitDef): string | null {
  if (from.dimension !== to.dimension) {
    return `dimension mismatch: ${from.dimension} vs ${to.dimension}`;
  }
  if (from.kind !== to.kind) {
    return (
      `quantity kind mismatch: ${from.name} is ${from.kind}, ` +
      `${to.name} is ${to.kind} (absolute readings and deltas are different types)`
    );
  }
  return null;
}

// ---- built-in units -------------------------------------------------------

// length (canonical base: metre)
registerUnit({ name: 'm', dimension: 'length', scale: 1 });
registerUnit({ name: 'mm', dimension: 'length', scale: '0.001' });
registerUnit({ name: 'cm', dimension: 'length', scale: '0.01' });
registerUnit({ name: 'km', dimension: 'length', scale: 1000 });
registerUnit({ name: 'in', dimension: 'length', scale: '0.0254' });
registerUnit({ name: 'ft', dimension: 'length', scale: '0.3048' });

// mass (canonical base: kilogram)
registerUnit({ name: 'kg', dimension: 'mass', scale: 1 });
registerUnit({ name: 'g', dimension: 'mass', scale: '0.001' });

// absolute temperature (canonical base: kelvin)
registerUnit({ name: 'K', dimension: 'temperature', kind: 'absolute', scale: 1 });
registerUnit({ name: 'degC', dimension: 'temperature', kind: 'absolute', scale: 1, offset: '273.15' });
registerUnit({ name: 'degF', dimension: 'temperature', kind: 'absolute', scale: Rational.parse('5').div(Rational.of(9n)), offset: Rational.parse('45967').div(Rational.of(180n)) });

// temperature difference (canonical base: kelvin difference) — no offsets
registerUnit({ name: 'dK', dimension: 'temperature', kind: 'difference', scale: 1 });
registerUnit({ name: 'dC', dimension: 'temperature', kind: 'difference', scale: 1 });
registerUnit({ name: 'dF', dimension: 'temperature', kind: 'difference', scale: Rational.parse('5').div(Rational.of(9n)) });
