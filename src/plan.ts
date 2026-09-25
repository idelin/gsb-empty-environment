import { UnitMigrationError } from './errors.ts';
import type { JsonValue } from './json.ts';
import { ROUNDING_MODES, type RoundingMode } from './rational.ts';
import { UnitRegistry, type Affine, type QuantityKind } from './units.ts';

/** One parsed path segment: an object key, optionally an array wildcard. */
export interface PathSegment {
  key: string;
  /** true when the segment is `key[*]` (iterate array elements, order preserved). */
  many: boolean;
}

/**
 * Parses a path pattern like `sensors[*].reading.tempC` into segments.
 * "" or "$" addresses the root value itself.
 */
export function parsePathPattern(pattern: string): PathSegment[] {
  if (pattern === '' || pattern === '$') return [];
  const parts = pattern.split('.');
  const segments: PathSegment[] = [];
  for (const part of parts) {
    const m = /^([^.[\]]+?)(\[\*\])?$/.exec(part);
    if (!m) {
      throw new UnitMigrationError(
        'COMPILE_PATH',
        `invalid path segment "${part}" in pattern "${pattern}"`,
      );
    }
    segments.push({ key: m[1], many: m[2] !== undefined });
  }
  return segments;
}

/**
 * Explicit target precision for a migrated field.
 * - `exact`: full rational result; emitted as a JSON number only when that is
 *   lossless, otherwise as an exact string (never silently truncated).
 * - `integer`: rounded with the named strategy; the rounding loss is written
 *   to the audit trail together with its worst-case error interval.
 */
export type PrecisionSpec = { type: 'exact' } | { type: 'integer'; rounding: RoundingMode };

export interface FieldRule {
  /** Path pattern, e.g. `sensors[*].tempC`. */
  path: string;
  from: string;
  to: string;
  /** Absolute quantities use the affine offset; delta quantities only scale. */
  quantity: QuantityKind;
  precision?: PrecisionSpec;
}

/**
 * A cross-field constraint, validated against the *complete* migrated
 * object. Return null when satisfied, or a message (optionally with a path)
 * describing the violation. Any failure aborts the migration atomically.
 */
export interface Constraint {
  name: string;
  check: (root: JsonValue) => null | string | { message: string; path?: string };
}

export interface MigrationPlanSpec {
  fields: FieldRule[];
  constraints?: Constraint[];
}

export interface CompiledField {
  id: number;
  pattern: string;
  segments: PathSegment[];
  from: string;
  to: string;
  quantity: QuantityKind;
  precision: PrecisionSpec;
  /** Composed exact affine `to = from * ratio + offset`. */
  affine: Affine;
}

export interface CompiledPlan {
  registry: UnitRegistry;
  fields: CompiledField[];
  constraints: Constraint[];
  /**
   * Present only on inverse plans (see invertPlan): per-path losslessness of
   * the forward migration. Inverse migrations of lossy forwards are marked
   * and never claim to be lossless.
   */
  losslessByPath?: Map<string, boolean>;
}

/**
 * Compiles a migration plan: validates path patterns, checks path
 * consistency (no duplicates, no pattern that is a prefix of another —
 * a node cannot be both a numeric field and a container), resolves units
 * and pre-composes the exact affine for every rule. Conversion-cycle
 * contradictions are caught earlier, when the UnitRegistry is built.
 */
export function compilePlan(registry: UnitRegistry, spec: MigrationPlanSpec): CompiledPlan {
  const fields: CompiledField[] = spec.fields.map((f, id) => {
    const segments = parsePathPattern(f.path);
    if (f.quantity !== 'absolute' && f.quantity !== 'delta') {
      throw new UnitMigrationError(
        'COMPILE_UNIT',
        `rule "${f.path}": quantity must be "absolute" or "delta", got ${JSON.stringify(f.quantity)}`,
      );
    }
    let affine: Affine;
    try {
      affine = registry.converter(f.from, f.to);
    } catch (e) {
      throw new UnitMigrationError('COMPILE_UNIT', `rule "${f.path}": ${(e as Error).message}`);
    }
    const precision: PrecisionSpec = f.precision ?? { type: 'exact' };
    if (precision.type === 'integer' && !ROUNDING_MODES.includes(precision.rounding)) {
      throw new UnitMigrationError(
        'COMPILE_PATH',
        `rule "${f.path}": unknown rounding mode ${JSON.stringify(precision.rounding)}`,
      );
    }
    return {
      id,
      pattern: f.path,
      segments,
      from: f.from,
      to: f.to,
      quantity: f.quantity,
      precision,
      affine,
    };
  });

  for (let i = 0; i < fields.length; i += 1) {
    for (let j = i + 1; j < fields.length; j += 1) {
      const a = fields[i].segments;
      const b = fields[j].segments;
      const min = Math.min(a.length, b.length);
      let eq = 0;
      while (eq < min && a[eq].key === b[eq].key && a[eq].many === b[eq].many) eq += 1;
      if (eq === a.length && eq === b.length) {
        throw new UnitMigrationError(
          'COMPILE_PATH',
          `duplicate path pattern "${fields[i].pattern}"`,
        );
      }
      if (eq === a.length || eq === b.length) {
        const [shorter, longer] = a.length < b.length ? [fields[i], fields[j]] : [fields[j], fields[i]];
        throw new UnitMigrationError(
          'COMPILE_PATH',
          `inconsistent paths: "${shorter.pattern}" is a prefix of "${longer.pattern}"; ` +
            'a node cannot be both a numeric field and a container',
        );
      }
    }
  }

  return { registry, fields, constraints: spec.constraints ?? [] };
}
