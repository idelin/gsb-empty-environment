/**
 * Migration rule compilation. Rules bound to the same schema path form an
 * ordered conversion chain (multi-step). Compilation is purely static:
 *
 *  - path consistency: every rule path must resolve to a numeric leaf;
 *  - per-step compatibility: dimension and quantity kind must match;
 *  - contradiction cycles: if a chain revisits a unit by name, the affine
 *    transform composed over the cycle must be the identity, otherwise the
 *    rules contradict each other;
 *  - integer targets must declare an explicit rounding mode.
 */

import { Rational, type RoundingMode } from './rational.ts';
import {
  checkCompatible,
  resolveUnit,
  type UnitDef,
  type UnitRef,
} from './units.ts';
import {
  parsePathPattern,
  resolveNumberLeaf,
  type NumberLeaf,
  type PathSegment,
  type SchemaNode,
} from './schema.ts';
import { MigrationError } from './errors.ts';

export interface MigrationRule {
  /** Schema path pattern, e.g. `readings[].value`. */
  readonly path: string;
  /** Target unit of this conversion step (registered name or inline def). */
  readonly to: UnitRef;
  /** Optional explicit rounding override for integer targets. */
  readonly rounding?: RoundingMode;
}

export interface ConversionStep {
  readonly from: UnitDef;
  readonly to: UnitDef;
}

export interface CompiledPath {
  readonly pattern: string;
  readonly segments: readonly PathSegment[];
  readonly leaf: NumberLeaf;
  readonly steps: readonly ConversionStep[];
  readonly sourceUnit: UnitDef;
  readonly finalUnit: UnitDef;
  readonly integerTarget: boolean;
  readonly rounding?: RoundingMode;
}

export interface CompiledMigration {
  readonly schema: SchemaNode;
  readonly paths: readonly CompiledPath[];
}

/** Affine transform: y = scale * x + offset (exact rationals). */
interface Affine {
  scale: Rational;
  offset: Rational;
}

function identity(): Affine {
  return { scale: Rational.one(), offset: Rational.zero() };
}

function compose(outer: Affine, inner: Affine): Affine {
  // outer(inner(x)) = outer.scale * inner.scale * x + outer.scale * inner.offset + outer.offset
  return {
    scale: outer.scale.mul(inner.scale),
    offset: outer.scale.mul(inner.offset).add(outer.offset),
  };
}

function stepAffine(from: UnitDef, to: UnitDef): Affine {
  // x_to = (from.scale * x + from.offset - to.offset) / to.scale
  return {
    scale: from.scale.div(to.scale),
    offset: from.offset.sub(to.offset).div(to.scale),
  };
}

export function compileMigration(
  schema: SchemaNode,
  rules: readonly MigrationRule[],
): CompiledMigration {
  const byPath = new Map<string, MigrationRule[]>();
  for (const rule of rules) {
    const list = byPath.get(rule.path);
    if (list) list.push(rule);
    else byPath.set(rule.path, [rule]);
  }

  const compiled: CompiledPath[] = [];
  for (const [pattern, chain] of byPath) {
    compiled.push(compileChain(schema, pattern, chain));
  }
  return { schema, paths: compiled };
}

function compileChain(
  schema: SchemaNode,
  pattern: string,
  chain: readonly MigrationRule[],
): CompiledPath {
  const segments = parsePathPattern(pattern);
  const leaf = resolveNumberLeaf(schema, segments, pattern);

  let sourceUnit: UnitDef;
  try {
    sourceUnit = resolveUnit(leaf.unit);
  } catch (err) {
    throw new MigrationError('unknown-unit', (err as Error).message, pattern);
  }

  const steps: ConversionStep[] = [];
  let current = sourceUnit;
  let lastRounding: RoundingMode | undefined;

  // Track visits by unit name to detect conversion cycles.
  const visits = new Map<string, { index: number; transform: Affine }>();
  let composed = identity();
  visits.set(current.name, { index: 0, transform: composed });

  for (const rule of chain) {
    let target: UnitDef;
    try {
      target = resolveUnit(rule.to);
    } catch (err) {
      throw new MigrationError('unknown-unit', (err as Error).message, pattern);
    }

    const incompatible = checkCompatible(current, target);
    if (incompatible !== null) {
      const code = incompatible.startsWith('dimension') ? 'dimension-mismatch' : 'kind-mismatch';
      throw new MigrationError(code, incompatible, pattern, {
        from: current.name,
        to: target.name,
      });
    }

    const affine = stepAffine(current, target);
    composed = compose(affine, composed);
    steps.push({ from: current, to: target });
    if (rule.rounding !== undefined) lastRounding = rule.rounding;

    // Anonymous inline units are not tracked: distinct definitions may
    // share the placeholder name, and only named units can form a
    // meaningful contradiction cycle.
    const prior = target.name === '<inline>' ? undefined : visits.get(target.name);
    if (prior !== undefined) {
      // Transform accumulated over the cycle: composed ∘ inverse(prior.transform)
      const inv: Affine = {
        scale: Rational.one().div(prior.transform.scale),
        offset: prior.transform.offset.neg().div(prior.transform.scale),
      };
      const cycleTransform = compose(composed, inv);
      const isIdentity =
        cycleTransform.scale.equals(Rational.one()) && cycleTransform.offset.isZero();
      if (!isIdentity) {
        throw new MigrationError(
          'contradictory-cycle',
          `contradictory conversion cycle at unit ${target.name}: ` +
            `cycle composes to scale=${cycleTransform.scale.toString()} ` +
            `offset=${cycleTransform.offset.toString()}, not identity`,
          pattern,
          { unit: target.name, cycleStartStep: prior.index, cycleEndStep: steps.length },
        );
      }
    } else if (target.name !== '<inline>') {
      visits.set(target.name, { index: steps.length, transform: composed });
    }

    current = target;
  }

  const integerTarget = leaf.integer;
  const rounding = lastRounding ?? leaf.rounding;
  if (integerTarget && rounding === undefined) {
    throw new MigrationError(
      'missing-rounding-mode',
      `integer target requires an explicit rounding mode (schema or rule)`,
      pattern,
    );
  }

  return {
    pattern,
    segments,
    leaf,
    steps,
    sourceUnit,
    finalUnit: current,
    integerTarget,
    rounding,
  };
}
