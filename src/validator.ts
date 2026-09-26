import type {
  CombinatorDiagnosis,
  Json,
  Schema,
  SchemaObject,
  ValidateOptions,
  ValidationError,
} from './types.ts';
import { canonical, keywordChecks, typeOf } from './keywords.ts';
import { toPointer } from './pointer.ts';
import { minimumHittingSet } from './cover.ts';

interface ResolvedOptions {
  maxErrors: number;
  coverMaxNodes: number;
}

/**
 * Rule identity for deduplication: the violated constraint (keyword +
 * parameters) at an instance location, independent of where in the schema
 * the rule was written. Identical rules in sibling branches collapse to one
 * shared error.
 */
export function ruleId(error: ValidationError): string {
  return `${error.keyword}@${error.instancePath}:${canonical(error.params ?? {})}`;
}

export function validate(schema: Schema, instance: Json, options: ValidateOptions = {}): ValidationError[] {
  const opts: ResolvedOptions = {
    maxErrors: options.maxErrors ?? Infinity,
    coverMaxNodes: options.coverMaxNodes ?? 50_000,
  };
  const errors: ValidationError[] = [];
  validateInto(schema, instance, [], [], errors, opts);
  return errors;
}

function isObjectSchema(schema: Schema): schema is SchemaObject {
  return typeof schema === 'object' && schema !== null;
}

function validateInto(
  schema: Schema,
  instance: Json,
  instPath: string[],
  schemaPath: string[],
  errors: ValidationError[],
  opts: ResolvedOptions,
): void {
  if (schema === true) return;
  if (schema === false) {
    errors.push({
      instancePath: toPointer(instPath),
      schemaPath: toPointer(schemaPath),
      keyword: 'false',
      message: 'boolean schema false rejects every instance',
    });
    return;
  }

  // Atomic assertion keywords.
  for (const [keyword, check] of Object.entries(keywordChecks)) {
    if (!(keyword in schema)) continue;
    const message = check(schema[keyword], instance);
    if (message !== null) {
      errors.push({
        instancePath: toPointer(instPath),
        schemaPath: toPointer([...schemaPath, keyword]),
        keyword,
        message,
        params: { expected: schema[keyword], actual: keyword === 'type' ? typeOf(instance) : undefined },
      });
    }
  }

  // Applicators.
  if (instance !== null && typeof instance === 'object' && !Array.isArray(instance)) {
    const properties = schema.properties;
    if (isObjectSchema(properties as Schema)) {
      for (const [key, sub] of Object.entries(properties as SchemaObject)) {
        if (key in instance) {
          validateInto(sub as Schema, instance[key], [...instPath, key], [...schemaPath, 'properties', key], errors, opts);
        }
      }
    }
    if (schema.additionalProperties === false) {
      const known = new Set(Object.keys((properties as SchemaObject | undefined) ?? {}));
      for (const key of Object.keys(instance)) {
        if (!known.has(key)) {
          errors.push({
            instancePath: toPointer([...instPath, key]),
            schemaPath: toPointer([...schemaPath, 'additionalProperties']),
            keyword: 'additionalProperties',
            message: `additional property ${JSON.stringify(key)} not allowed`,
            params: { additionalProperty: key },
          });
        }
      }
    }
  }

  if (Array.isArray(instance) && isObjectSchema(schema.items as Schema)) {
    const items = schema.items as Schema;
    instance.forEach((item, i) => {
      validateInto(items, item, [...instPath, String(i)], [...schemaPath, 'items'], errors, opts);
    });
  }

  for (const kind of ['oneOf', 'anyOf'] as const) {
    const branches = schema[kind];
    if (Array.isArray(branches)) {
      evaluateCombinator(kind, branches as Schema[], instance, instPath, schemaPath, errors, opts);
    }
  }
}

function evaluateCombinator(
  kind: 'oneOf' | 'anyOf',
  branches: Schema[],
  instance: Json,
  instPath: string[],
  schemaPath: string[],
  errors: ValidationError[],
  opts: ResolvedOptions,
): void {
  const reports = branches.map((sub, index) => {
    const branchErrors: ValidationError[] = [];
    validateInto(sub, instance, instPath, [...schemaPath, kind, String(index)], branchErrors, opts);
    return {
      index,
      schemaPath: toPointer([...schemaPath, kind, String(index)]),
      ok: branchErrors.length === 0,
      errors: branchErrors,
    };
  });
  const matched = reports.filter((r) => r.ok).map((r) => r.index);

  // A satisfied combinator surfaces nothing: failed-branch evidence is
  // discarded so a successful branch is never polluted by sibling errors.
  if (kind === 'anyOf' && matched.length >= 1) return;
  if (kind === 'oneOf' && matched.length === 1) return;

  const here = toPointer(instPath);
  const combinatorPath = toPointer([...schemaPath, kind]);

  if (matched.length > 1) {
    // oneOf ambiguity: the instance itself is the finite witness.
    errors.push({
      instancePath: here,
      schemaPath: combinatorPath,
      keyword: kind,
      message: `matches ${matched.length} of ${branches.length} branches, expected exactly 1`,
      params: { matched: matched.length, branches: branches.length },
      diagnosis: {
        kind,
        reason: 'multiple-branches-matched',
        branches: reports,
        matched,
        selectedErrors: [],
        witness: { instancePath: here, matchedBranches: matched },
        complete: true,
      },
    });
    return;
  }

  // Zero matches: build a minimal error set that refutes every branch.
  const byId = new Map<string, { error: ValidationError; branches: number[]; alsoAt: string[] }>();
  reports.forEach((report, branchIndex) => {
    for (const error of report.errors) {
      const id = ruleId(error);
      const entry = byId.get(id);
      if (entry) {
        entry.branches.push(branchIndex);
        entry.alsoAt.push(error.schemaPath);
      } else {
        byId.set(id, { error, branches: [branchIndex], alsoAt: [] });
      }
    }
  });

  const ids = [...byId.keys()];
  const indexOf = new Map(ids.map((id, i) => [id, i]));
  const sets = reports.map((report) => [...new Set(report.errors.map((e) => indexOf.get(ruleId(e))!))]);
  const occurrences = reports.reduce((n, r) => n + r.errors.length, 0);

  const cover = minimumHittingSet(sets, ids.length, opts.coverMaxNodes);

  let selected = cover.selected.map((i) => {
    const entry = byId.get(ids[i])!;
    const error: ValidationError = { ...entry.error };
    if (entry.alsoAt.length > 0) error.alsoAt = entry.alsoAt;
    return error;
  });

  // Error budget: truncation may only claim completeness when every failed
  // branch keeps at least one piece of selected evidence.
  let complete = true;
  let uncoveredBranches: number[] | undefined;
  if (selected.length > opts.maxErrors) {
    const kept = selected.slice(0, Math.max(0, opts.maxErrors));
    const keptIds = new Set(kept.map(ruleId));
    const uncovered = reports
      .filter((r) => !r.errors.some((e) => keptIds.has(ruleId(e))))
      .map((r) => r.index);
    if (uncovered.length > 0) {
      complete = false;
      uncoveredBranches = uncovered;
    }
    selected = kept;
  }

  const diagnosis: CombinatorDiagnosis = {
    kind,
    reason: 'no-branch-matched',
    branches: reports,
    matched: [],
    selectedErrors: selected,
    cover: {
      optimal: cover.optimal,
      lowerBound: cover.lowerBound,
      upperBound: cover.upperBound,
      sharedErrors: occurrences - ids.length,
    },
    complete,
  };
  if (uncoveredBranches) diagnosis.uncoveredBranches = uncoveredBranches;

  errors.push({
    instancePath: here,
    schemaPath: combinatorPath,
    keyword: kind,
    message: `matches 0 of ${branches.length} branches`,
    params: { matched: 0, branches: branches.length },
    diagnosis,
  });
}
