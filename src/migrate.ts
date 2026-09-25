import { UnitMigrationError } from './errors.ts';
import { assertJsonTree, type JsonValue } from './json.ts';
import type { CompiledField, CompiledPlan, PathSegment } from './plan.ts';
import {
  Rational,
  bigintToSafeNumber,
  formatRational,
  roundToInteger,
  roundingInterval,
} from './rational.ts';
import type { QuantityKind } from './units.ts';

export interface AuditEntry {
  ruleId: number;
  /** Concrete path of the migrated value, e.g. "$.sensors[2].tempC". */
  path: string;
  from: string;
  to: string;
  quantity: QuantityKind;
  input: JsonValue;
  /** Exact rational result before any rounding (decimal or "p/q" form). */
  exact: string;
  output: JsonValue;
  /** true when information was lost (integer rounding with non-zero error). */
  lossy: boolean;
  /** Inverse plans only: whether this rule's forward migration was lossless. */
  lossless?: boolean;
  /** Exact (output - exact) as a rational, present for integer targets. */
  error?: string;
  /** Worst-case bound: the exact value lies in output + [lo, hi]. */
  errorInterval?: { lo: string; loInclusive: boolean; hi: string; hiInclusive: boolean };
  note?: string;
}

export interface MigrationWarning {
  path: string;
  message: string;
}

export type MigrationResult =
  | { ok: true; value: JsonValue; audit: AuditEntry[]; warnings: MigrationWarning[] }
  | { ok: false; errors: UnitMigrationError[] };

/**
 * Runs a compiled plan over a JSON tree.
 *
 * The input is never mutated. A fully migrated new object is built first;
 * cross-field constraints are validated against that complete object, and
 * if any of them fails the whole migration is rolled back — the result is
 * `{ ok: false, errors }` and no partial output is produced.
 */
export function migrate(plan: CompiledPlan, input: JsonValue): MigrationResult {
  try {
    assertJsonTree(input);
    const audit: AuditEntry[] = [];
    const warnings: MigrationWarning[] = [];
    const replacements = new Map<string, JsonValue>();

    for (const field of plan.fields) {
      const matches: { value: JsonValue; path: string }[] = [];
      matchNode(input, field, field.segments, 0, '$', matches, warnings);
      for (const m of matches) {
        if (replacements.has(m.path)) {
          throw new UnitMigrationError(
            'COMPILE_PATH',
            `two rules matched the same node at ${m.path}`,
            { path: m.path },
          );
        }
        replacements.set(m.path, convertValue(field, m.value, m.path, audit, plan));
      }
    }

    const value = cloneWithReplacements(input, '$', replacements);

    const errors: UnitMigrationError[] = [];
    for (const constraint of plan.constraints) {
      let violation: ReturnType<CompiledPlan['constraints'][number]['check']>;
      try {
        violation = constraint.check(value);
      } catch (e) {
        violation = { message: `constraint threw: ${(e as Error).message}` };
      }
      if (violation !== null && violation !== undefined) {
        const message = typeof violation === 'string' ? violation : violation.message;
        const path = typeof violation === 'object' ? violation.path : undefined;
        errors.push(
          new UnitMigrationError('CONSTRAINT', `constraint "${constraint.name}" failed: ${message}`, {
            path,
          }),
        );
      }
    }
    if (errors.length > 0) {
      // Atomic rollback: no partial object escapes, the input is untouched.
      return { ok: false, errors };
    }
    return { ok: true, value, audit, warnings };
  } catch (e) {
    if (e instanceof UnitMigrationError) return { ok: false, errors: [e] };
    throw e;
  }
}

function matchNode(
  node: JsonValue,
  field: CompiledField,
  segments: PathSegment[],
  index: number,
  path: string,
  out: { value: JsonValue; path: string }[],
  warnings: MigrationWarning[],
): void {
  if (index === segments.length) {
    out.push({ value: node, path });
    return;
  }
  const segment = segments[index];
  if (typeof node !== 'object' || node === null || Array.isArray(node)) {
    throw new UnitMigrationError(
      'PATH_TYPE',
      `rule "${field.pattern}": expected an object at ${path} while resolving "${segment.key}"`,
      { path },
    );
  }
  const record = node as Record<string, JsonValue>;
  if (!(segment.key in record)) {
    warnings.push({
      path,
      message: `rule "${field.pattern}": key "${segment.key}" is missing at ${path}`,
    });
    return;
  }
  const child = record[segment.key];
  const childPath = `${path}.${segment.key}`;
  if (segment.many) {
    if (!Array.isArray(child)) {
      throw new UnitMigrationError(
        'PATH_TYPE',
        `rule "${field.pattern}": expected an array at ${childPath}`,
        { path: childPath },
      );
    }
    if (child.length === 0) {
      warnings.push({ path: childPath, message: `rule "${field.pattern}": array is empty` });
    }
    child.forEach((item, i) => {
      matchNode(item, field, segments, index + 1, `${childPath}[${i}]`, out, warnings);
    });
  } else {
    matchNode(child, field, segments, index + 1, childPath, out, warnings);
  }
}

function parseFieldNumber(raw: JsonValue, path: string): Rational {
  if (typeof raw === 'number') {
    return Rational.of(raw);
  }
  if (typeof raw === 'string') {
    try {
      return Rational.parse(raw);
    } catch {
      throw new UnitMigrationError('VALUE_NOT_NUMERIC', `value at ${path} is not numeric: ${JSON.stringify(raw)}`, { path });
    }
  }
  throw new UnitMigrationError(
    'VALUE_NOT_NUMERIC',
    `value at ${path} must be a number or numeric string, got ${Array.isArray(raw) ? 'array' : typeof raw}`,
    { path },
  );
}

function convertValue(
  field: CompiledField,
  raw: JsonValue,
  path: string,
  audit: AuditEntry[],
  plan: CompiledPlan,
): JsonValue {
  const x = parseFieldNumber(raw, path);
  const exact =
    field.quantity === 'absolute'
      ? x.mul(field.affine.ratio).add(field.affine.offset)
      : x.mul(field.affine.ratio);

  const inverseLossless = plan.losslessByPath?.get(path);
  const base: AuditEntry = {
    ruleId: field.id,
    path,
    from: field.from,
    to: field.to,
    quantity: field.quantity,
    input: raw,
    exact: formatRational(exact),
    output: null,
    lossy: false,
    ...(inverseLossless !== undefined ? { lossless: inverseLossless } : {}),
    ...(inverseLossless === false
      ? { note: 'inverse of a lossy forward migration; this value is approximate' }
      : {}),
  };

  if (field.precision.type === 'integer') {
    const mode = field.precision.rounding;
    const n = roundToInteger(exact, mode);
    const out = bigintToSafeNumber(n);
    if (out === null) {
      throw new UnitMigrationError(
        'UNSAFE_INTEGER',
        `rounded value at ${path} exceeds the safe-integer range`,
        { path },
      );
    }
    const error = Rational.of(n).sub(exact);
    const interval = roundingInterval(mode);
    audit.push({
      ...base,
      output: out,
      lossy: !error.isZero,
      error: formatRational(error),
      errorInterval: {
        lo: formatRational(interval.lo),
        loInclusive: interval.loInclusive,
        hi: formatRational(interval.hi),
        hiInclusive: interval.hiInclusive,
      },
    });
    return out;
  }

  // exact precision: emit a JSON number only when it represents the value
  // exactly; otherwise emit an exact string rather than silently truncating.
  if (exact.isInteger) {
    const out = bigintToSafeNumber(exact.num);
    if (out !== null) {
      audit.push({ ...base, output: out });
      return out;
    }
    const asString = exact.toString();
    audit.push({ ...base, output: asString, note: 'integer exceeds the safe range; emitted as string' });
    return asString;
  }
  const asNumber = exact.toNumber();
  if (Rational.of(asNumber).equals(exact)) {
    audit.push({ ...base, output: asNumber });
    return asNumber;
  }
  const asString = exact.toTerminatingDecimal() ?? exact.toString();
  audit.push({
    ...base,
    output: asString,
    note: 'exact value is not representable as a JSON number; emitted as string to avoid silent rounding',
  });
  return asString;
}

function cloneWithReplacements(
  node: JsonValue,
  path: string,
  replacements: Map<string, JsonValue>,
): JsonValue {
  const replacement = replacements.get(path);
  if (replacement !== undefined) return replacement;
  if (Array.isArray(node)) {
    // Arrays are ordered: indices map one-to-one, order preserved.
    return node.map((item, i) => cloneWithReplacements(item, `${path}[${i}]`, replacements));
  }
  if (typeof node === 'object' && node !== null) {
    const out: Record<string, JsonValue> = {};
    for (const [key, item] of Object.entries(node)) {
      out[key] = cloneWithReplacements(item, `${path}.${key}`, replacements);
    }
    return out;
  }
  return node;
}
