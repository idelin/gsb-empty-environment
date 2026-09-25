/**
 * Migration execution.
 *
 * - The input tree is never mutated; a new tree is built (rollback-safe).
 * - All arithmetic is exact rational; rounding happens once, at the final
 *   target of each chain (deferred rounding), with an explicit mode.
 * - Integer targets emit an exact rounding-error interval in the audit.
 * - Cross-field constraints are validated on the COMPLETE new object;
 *   any violation rolls the whole migration back.
 * - Inverse migrations are exact as affine maps but never claim to be
 *   lossless: forward rounding loss cannot be recovered.
 */

import { Rational, roundToInteger } from './rational.ts';
import { convertRational, type UnitDef } from './units.ts';
import { formatConcretePath, type SchemaNode } from './schema.ts';
import { compileMigration, type CompiledMigration, type CompiledPath, type MigrationRule } from './compile.ts';
import { MigrationError } from './errors.ts';

export interface AuditEntry {
  readonly path: string;
  readonly kind: 'exact' | 'rounding-loss' | 'float-roundtrip-loss';
  readonly fromUnit: string;
  readonly toUnit: string;
  /** Exact rational value at the target unit before any rounding. */
  readonly exactValue: string;
  /** Value actually written to the output tree. */
  readonly outputValue: number;
  /**
   * Rounding error interval [lo, hi] in target units: the true exact value
   * lies in [outputValue + lo, outputValue + hi]. Exact rationals.
   */
  readonly errorInterval: { readonly lo: string; readonly hi: string };
  readonly roundingMode?: string;
}

export interface Constraint {
  readonly name: string;
  /** Concrete-free schema patterns involved; used for error reporting. */
  readonly paths: readonly string[];
  /** Validate the COMPLETE migrated object. Return true or a message. */
  readonly check: (root: unknown) => true | string;
}

export interface MigrateOptions {
  readonly constraints?: readonly Constraint[];
}

export type MigrateResult =
  | {
      readonly ok: true;
      readonly data: unknown;
      readonly audit: readonly AuditEntry[];
      /** True only when every conversion was exact (no rounding loss). */
      readonly lossless: boolean;
    }
  | {
      readonly ok: false;
      readonly error: MigrationError;
      /** The original input, untouched. */
      readonly data: unknown;
    };

interface PlanBinding {
  readonly compiled: CompiledPath;
  /** key-path prefix (object keys only) used to match during the walk. */
  readonly keyPrefix: readonly string[];
}

export function migrate(
  schema: SchemaNode,
  rules: readonly MigrationRule[],
  data: unknown,
  options: MigrateOptions = {},
): MigrateResult {
  let plan: CompiledMigration;
  try {
    plan = compileMigration(schema, rules);
  } catch (err) {
    if (err instanceof MigrationError) return { ok: false, error: err, data };
    throw err;
  }
  return migrateCompiled(plan, data, options);
}

export function migrateCompiled(
  plan: CompiledMigration,
  data: unknown,
  options: MigrateOptions = {},
): MigrateResult {
  const audit: AuditEntry[] = [];
  const bindings: PlanBinding[] = plan.paths.map((compiled) => ({
    compiled,
    keyPrefix: compiled.segments.filter((s) => 'key' in s).map((s) => (s as { key: string }).key),
  }));

  try {
    const output = walk(plan.schema, data, [], bindings, audit);
    for (const constraint of options.constraints ?? []) {
      const verdict = constraint.check(output);
      if (verdict !== true) {
        throw new MigrationError(
          'constraint-violation',
          `constraint ${JSON.stringify(constraint.name)} failed on migrated object: ${verdict}`,
          constraint.paths.join(', '),
          { constraint: constraint.name, paths: constraint.paths },
        );
      }
    }
    const lossless = audit.every((a) => a.kind === 'exact');
    return { ok: true, data: output, audit, lossless };
  } catch (err) {
    if (err instanceof MigrationError) {
      // Full rollback: the caller gets the original input back.
      return { ok: false, error: err, data };
    }
    throw err;
  }
}

function matchBinding(
  bindings: readonly PlanBinding[],
  concrete: readonly (string | number)[],
): PlanBinding | undefined {
  const keys = concrete.filter((s): s is string => typeof s === 'string');
  for (const binding of bindings) {
    if (binding.keyPrefix.length !== keys.length) continue;
    let ok = true;
    for (let i = 0; i < keys.length; i++) {
      if (binding.keyPrefix[i] !== keys[i]) {
        ok = false;
        break;
      }
    }
    if (ok) return binding;
  }
  return undefined;
}

function walk(
  schema: SchemaNode,
  value: unknown,
  path: (string | number)[],
  bindings: readonly PlanBinding[],
  audit: AuditEntry[],
): unknown {
  switch (schema.type) {
    case 'object': {
      if (typeof value !== 'object' || value === null || Array.isArray(value)) {
        throw new MigrationError(
          'value-type-mismatch',
          `expected object, got ${Array.isArray(value) ? 'array' : typeof value}`,
          formatConcretePath(path),
        );
      }
      const out: Record<string, unknown> = {};
      for (const key of Object.keys(schema.fields)) {
        if (!(key in (value as Record<string, unknown>))) {
          throw new MigrationError(
            'value-type-mismatch',
            `missing required key ${JSON.stringify(key)}`,
            formatConcretePath([...path, key]),
          );
        }
        out[key] = walk(
          schema.fields[key]!,
          (value as Record<string, unknown>)[key],
          [...path, key],
          bindings,
          audit,
        );
      }
      return out;
    }
    case 'array': {
      if (!Array.isArray(value)) {
        throw new MigrationError(
          'value-type-mismatch',
          `expected array, got ${typeof value}`,
          formatConcretePath(path),
        );
      }
      return value.map((item, index) =>
        walk(schema.items, item, [...path, index], bindings, audit),
      );
    }
    case 'number': {
      if (typeof value !== 'number' || !Number.isFinite(value)) {
        throw new MigrationError(
          'value-type-mismatch',
          `expected finite number, got ${typeof value}`,
          formatConcretePath(path),
        );
      }
      const binding = matchBinding(bindings, path);
      if (binding === undefined) {
        if (schema.integer && !Number.isInteger(value)) {
          throw new MigrationError(
            'value-type-mismatch',
            'expected integer-valued number',
            formatConcretePath(path),
          );
        }
        return value;
      }
      return applyChain(binding.compiled, value, formatConcretePath(path), audit);
    }
    case 'string':
    case 'boolean':
      if (typeof value !== schema.type) {
        throw new MigrationError(
          'value-type-mismatch',
          `expected ${schema.type}, got ${typeof value}`,
          formatConcretePath(path),
        );
      }
      return value;
    case 'null':
      if (value !== null) {
        throw new MigrationError(
          'value-type-mismatch',
          'expected null',
          formatConcretePath(path),
        );
      }
      return value;
    case 'any':
      return value;
  }
}

function applyChain(
  compiled: CompiledPath,
  value: number,
  concretePath: string,
  audit: AuditEntry[],
): number {
  // Exact rational through every step; rounding is deferred to the end.
  let exact = Rational.fromNumber(value);
  for (const step of compiled.steps) {
    exact = convertRational(exact, step.from, step.to);
  }

  if (compiled.integerTarget) {
    const rounded = roundToInteger(exact, compiled.rounding!);
    const error = Rational.of(rounded).sub(exact); // output - exact
    const output = Number(rounded);
    audit.push({
      path: concretePath,
      kind: error.isZero() ? 'exact' : 'rounding-loss',
      fromUnit: compiled.sourceUnit.name,
      toUnit: compiled.finalUnit.name,
      exactValue: exact.toString(),
      outputValue: output,
      errorInterval: { lo: error.neg().toString(), hi: error.neg().toString() },
      roundingMode: compiled.rounding,
    });
    return output;
  }

  const output = exact.toNumber();
  const representable = exact.isExactlyRepresentableAsNumber();
  audit.push({
    path: concretePath,
    kind: representable ? 'exact' : 'float-roundtrip-loss',
    fromUnit: compiled.sourceUnit.name,
    toUnit: compiled.finalUnit.name,
    exactValue: exact.toString(),
    outputValue: output,
    errorInterval: representable
      ? { lo: '0', hi: '0' }
      : floatErrorInterval(exact, output),
  });
  return output;
}

function floatErrorInterval(exact: Rational, output: number): { lo: string; hi: string } {
  const back = Rational.fromNumber(output);
  const diff = back.sub(exact); // output - exact
  return { lo: diff.neg().toString(), hi: diff.neg().toString() };
}

// ---- inverse migration -----------------------------------------------------

export interface InverseMigration {
  readonly rules: readonly MigrationRule[];
  /**
   * Inverse affine maps are exact, but a round trip is NOT lossless
   * whenever the forward migration rounded anything. This flag is always
   * false: the inverse never claims losslessness.
   */
  readonly lossless: false;
  readonly caveat: string;
}

export function invertMigration(
  plan: CompiledMigration,
  forwardAudit: readonly AuditEntry[],
): InverseMigration {
  const rules: MigrationRule[] = [];
  for (const compiled of plan.paths) {
    if (compiled.steps.length === 0) continue;
    rules.push({ path: compiled.pattern, to: compiled.sourceUnit.name });
  }
  const losses = forwardAudit.filter((a) => a.kind !== 'exact');
  return {
    rules,
    lossless: false,
    caveat:
      losses.length === 0
        ? 'forward migration was exact; inverse is exact too, but losslessness is never guaranteed by construction'
        : `forward migration lost information at ${losses.length} field(s) ` +
          `(${losses.map((a) => a.path).join(', ')}); inverse cannot restore it`,
  };
}

// ---- multi-step verification against an exact rational reference ----------

export interface ChainAnalysis {
  /** Exact rational result with rounding deferred to the very end. */
  readonly exactDeferred: string;
  /** Deferred rounding (round once at the end), as string. */
  readonly deferredRounded: string;
  /** Eager rounding (round after every step), as string. */
  readonly eagerRounded: string;
  /** eager - deferred, exact. */
  readonly eagerVsDeferred: string;
  /** Sequential float64 computation through the same steps. */
  readonly floatSequential: number;
  /** exact - floatSequential, as a rational string. */
  readonly floatError: string;
}

/**
 * Verify a multi-step conversion chain against an exact rational reference,
 * exposing the difference between deferred and eager rounding, and the
 * error of a naive float64 pipeline.
 */
export function analyzeChain(
  units: readonly UnitDef[],
  value: Rational,
  rounding: (v: Rational) => Rational,
): ChainAnalysis {
  if (units.length < 2) throw new Error('analyzeChain: need at least two units');

  let exact = value;
  let eager = value;
  let floatVal = value.toNumber();
  for (let i = 0; i + 1 < units.length; i++) {
    exact = convertRational(exact, units[i]!, units[i + 1]!);
    eager = rounding(convertRational(eager, units[i]!, units[i + 1]!));
    const f = units[i]!;
    const t = units[i + 1]!;
    floatVal = (f.scale.toNumber() * floatVal + f.offset.toNumber() - t.offset.toNumber()) / t.scale.toNumber();
  }
  const deferredRounded = rounding(exact);
  return {
    exactDeferred: exact.toString(),
    deferredRounded: deferredRounded.toString(),
    eagerRounded: eager.toString(),
    eagerVsDeferred: eager.sub(deferredRounded).toString(),
    floatSequential: floatVal,
    floatError: exact.sub(Rational.fromNumber(floatVal)).toString(),
  };
}
