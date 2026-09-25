import { Rational, roundToInteger, type RationalInput, type RoundingMode } from './rational.ts';
import { UnitRegistry, type QuantityKind } from './units.ts';

export interface ChainStep {
  unit: string;
  /** Exact rational value at this step. */
  value: Rational;
}

export interface ChainResult {
  steps: ChainStep[];
  result: Rational;
}

/**
 * Multi-step conversion through a chain of units with fully deferred
 * rounding: every intermediate value stays an exact rational. The result is
 * cross-checked against the directly composed first->last conversion (the
 * exact rational reference); any disagreement indicates an internal bug and
 * throws.
 */
export function chainExact(
  registry: UnitRegistry,
  value: RationalInput,
  units: string[],
  quantity: QuantityKind,
): ChainResult {
  if (units.length === 0) throw new Error('unit chain must not be empty');
  const start = Rational.of(value);
  const steps: ChainStep[] = [{ unit: units[0], value: start }];
  let current = start;
  for (let i = 1; i < units.length; i += 1) {
    current = registry.convert(current, units[i - 1], units[i], quantity);
    steps.push({ unit: units[i], value: current });
  }
  if (units.length > 2) {
    const direct = registry.convert(start, units[0], units[units.length - 1], quantity);
    if (!direct.equals(current)) {
      throw new Error(
        `internal inconsistency: chained conversion ${units.join(' -> ')} gives ` +
          `${current.toString()} but the composed conversion gives ${direct.toString()}`,
      );
    }
  }
  return { steps, result: current };
}

/**
 * The same chain, but rounding to an integer after *every* step — the
 * classic error-accumulating strategy. Contrasted with `chainExact` +
 * a single final rounding by `deferredVsStepwise`.
 */
export function chainStepRounded(
  registry: UnitRegistry,
  value: RationalInput,
  units: string[],
  quantity: QuantityKind,
  mode: RoundingMode,
): ChainResult {
  if (units.length === 0) throw new Error('unit chain must not be empty');
  const start = Rational.of(value);
  const steps: ChainStep[] = [{ unit: units[0], value: start }];
  let current = start;
  for (let i = 1; i < units.length; i += 1) {
    const exact = registry.convert(current, units[i - 1], units[i], quantity);
    current = Rational.of(roundToInteger(exact, mode));
    steps.push({ unit: units[i], value: current });
  }
  return { steps, result: current };
}

export interface DeferredVsStepwise {
  /** Exact rational end value (no rounding anywhere). */
  exact: Rational;
  /** Deferred strategy: exact through the chain, one rounding at the end. */
  deferred: Rational;
  /** Stepwise strategy: integer rounding after every hop. */
  stepwise: Rational;
  /** deferred - stepwise, as an exact rational. */
  difference: Rational;
}

/**
 * Quantifies, with exact rational arithmetic, how much the stepwise
 * rounding strategy deviates from deferred rounding for a given chain.
 */
export function deferredVsStepwise(
  registry: UnitRegistry,
  value: RationalInput,
  units: string[],
  quantity: QuantityKind,
  mode: RoundingMode,
): DeferredVsStepwise {
  const exact = chainExact(registry, value, units, quantity).result;
  const deferred = Rational.of(roundToInteger(exact, mode));
  const stepwise = chainStepRounded(registry, value, units, quantity, mode).result;
  return { exact, deferred, stepwise, difference: deferred.sub(stepwise) };
}
