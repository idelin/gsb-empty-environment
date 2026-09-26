import {
  assertSchema,
  collectFields,
  conditionFields,
  type Condition,
  type GroupRule,
  type Schema,
  type UndecidedPolicy,
} from './schema.ts';
import { evalCondition, type View } from './conditions.ts';
import { getValue, isPresent } from './presence.ts';
import { Solver } from './solver.ts';

export interface Issue {
  type: 'conflict' | 'missing' | 'undecided';
  message: string;
  /** JSON Pointer into the instance ('' = the instance root). */
  instancePath: string;
  /** JSON Pointer into the schema (e.g. '/groups/0', '/groups/1/when'). */
  rulePath: string;
  /** Instance fields involved in the issue. */
  fields: string[];
}

export interface ConflictCore {
  /** Instance fields whose PRESENCE participates in the core (instance paths '/<field>'). */
  facts: string[];
  /** Rule paths ('/groups/<i>') participating in the core. */
  rules: string[];
}

export interface ValidationResult {
  /** true iff status is 'valid' AND no issue (e.g. undecided-as-error) was raised. */
  valid: boolean;
  status: 'valid' | 'missing' | 'conflict';
  issues: Issue[];
  /**
   * All minimum-cardinality completions: every witness of the smallest
   * size, each verified by full re-evaluation. Empty unless status='missing'.
   */
  completions: string[][];
  /** One subset-minimal conflict core. Empty unless status='conflict'. */
  conflictCores: ConflictCore[];
}

export interface ValidateOptions {
  /** Contract policy for undecided conditions. Default 'inactive'. */
  onUndecided?: UndecidedPolicy;
  /** Guard for the small-domain enumeration contract. Default 20. */
  maxFields?: number;
}

const DEFAULT_MAX_FIELDS = 20;

function* combinations<T>(items: T[], k: number): Generator<T[]> {
  const n = items.length;
  if (k < 0 || k > n) return;
  if (k === 0) {
    yield [];
    return;
  }
  const idx = Array.from({ length: k }, (_, i) => i);
  for (;;) {
    yield idx.map((i) => items[i]);
    let i = k - 1;
    while (i >= 0 && idx[i] === n - k + i) i--;
    if (i < 0) return;
    idx[i]++;
    for (let j = i + 1; j < k; j++) idx[j] = idx[j - 1] + 1;
  }
}

function boundsOf(rule: GroupRule): { min: number; max: number } {
  switch (rule.kind) {
    case 'exactlyOne':
      return { min: 1, max: 1 };
    case 'atLeastOne':
      return { min: 1, max: rule.fields.length };
    case 'atMostK':
      return { min: 0, max: rule.k as number };
  }
}

function missingConditionFields(cond: Condition, view: View): string[] {
  return [...new Set(conditionFields(cond))].filter((f) => !view.has(f)).sort();
}

/**
 * Validates `instance` (a plain object) against the schema subset.
 *
 * Joint satisfiability: all conditionally-active groups are solved
 * TOGETHER — a per-group pass is never treated as a joint pass. Completion
 * candidates re-evaluate every condition, so added fields may enable or
 * disable conditional groups (conditions are not assumed monotone).
 */
export function validate(
  instance: unknown,
  schema: Schema,
  options: ValidateOptions = {},
): ValidationResult {
  const policy: UndecidedPolicy = options.onUndecided ?? 'inactive';
  const maxFields = options.maxFields ?? DEFAULT_MAX_FIELDS;

  assertSchema(schema);
  if (typeof instance !== 'object' || instance === null || Array.isArray(instance)) {
    throw new TypeError('instance must be a plain object');
  }

  const groups = schema.groups;
  const allFields = collectFields(schema);
  if (allFields.length > maxFields) {
    throw new Error(
      `field domain too large (${allFields.length} > ${maxFields}); ` +
        'this contract only promises small-domain enumeration',
    );
  }

  const presentInInstance = new Set(allFields.filter((f) => isPresent(instance, f)));
  const addable = allFields.filter((f) => !presentInInstance.has(f));
  const issues: Issue[] = [];

  /** A view of a hypothetical world: instance plus an optional set of added fields. */
  const viewFor = (added: ReadonlySet<string> | null): View => ({
    has: (f) => presentInInstance.has(f) || (added !== null && added.has(f)),
    // Only instance-own fields have an inspectable VALUE; a completion
    // promises presence, not a value, so conditions on added values stay undecided.
    valueKnown: (f) => presentInInstance.has(f),
    get: (f) => getValue(instance, f),
  });

  const activation = (
    rule: GroupRule,
    view: View,
    ruleIndex: number,
    report: boolean,
  ): 'active' | 'inactive' => {
    if (rule.when === undefined) return 'active';
    const tri = evalCondition(rule.when, view);
    if (tri === 'true') return 'active';
    if (tri === 'false') return 'inactive';
    if (policy === 'active') return 'active';
    if (policy === 'error' && report) {
      issues.push({
        type: 'undecided',
        message: `condition of group ${ruleIndex} cannot be decided (missing field); policy 'error'`,
        instancePath: '',
        rulePath: `/groups/${ruleIndex}/when`,
        fields: missingConditionFields(rule.when, view),
      });
    }
    return 'inactive';
  };

  const candidateWorks = (added: ReadonlySet<string> | null, report: boolean): boolean => {
    const view = viewFor(added);
    for (let i = 0; i < groups.length; i++) {
      const rule = groups[i];
      if (activation(rule, view, i, report) !== 'active') continue;
      const count = rule.fields.filter((f) => view.has(f)).length;
      const { min, max } = boundsOf(rule);
      if (count < min || count > max) return false;
    }
    return true;
  };

  // 1) The instance as-is (reports undecided-condition issues once).
  const instanceWorks = candidateWorks(null, true);

  // 2) Minimum-cardinality completions, every witness fully re-verified.
  const completions: string[][] = [];
  if (!instanceWorks) {
    for (let size = 1; size <= addable.length && completions.length === 0; size++) {
      for (const combo of combinations(addable, size)) {
        if (candidateWorks(new Set(combo), false)) completions.push([...combo].sort());
      }
    }
  }

  const found = instanceWorks || completions.length > 0;

  // 3) Missing-field issues: rules active under the instance but violated by it.
  if (!instanceWorks && found) {
    const view = viewFor(null);
    for (let i = 0; i < groups.length; i++) {
      const rule = groups[i];
      if (activation(rule, view, i, false) !== 'active') continue;
      const count = rule.fields.filter((f) => view.has(f)).length;
      const { min, max } = boundsOf(rule);
      if (count < min || count > max) {
        issues.push({
          type: 'missing',
          message:
            `group ${i} (${rule.kind}) requires between ${min} and ${max} of ` +
            `[${rule.fields.join(', ')}], instance has ${count}`,
          instancePath: '',
          rulePath: `/groups/${i}`,
          fields: [...rule.fields],
        });
      }
    }
  }

  // 4) Conflict: no superset of the instance can satisfy the joint system.
  //    Compute one subset-minimal core over {presence facts} x {group rules}.
  const conflictCores: ConflictCore[] = [];
  if (!found) {
    issues.push({
      type: 'conflict',
      message: 'no completion of the instance can jointly satisfy the conditionally active groups',
      instancePath: '',
      rulePath: '/groups',
      fields: [],
    });

    type CoreItem = { kind: 'fact'; field: string } | { kind: 'rule'; index: number };

    const subsetUnsat = (items: CoreItem[]): boolean => {
      const fixed = new Set(
        items.filter((i) => i.kind === 'fact').map((i) => (i as { field: string }).field),
      );
      const ruleIdxs = items
        .filter((i) => i.kind === 'rule')
        .map((i) => (i as { index: number }).index);
      const free = allFields.filter((f) => !fixed.has(f));

      const solver = new Solver();
      const varOf = new Map(allFields.map((f) => [f, solver.addVar()]));

      for (let mask = 0; mask < 2 ** free.length; mask++) {
        solver.push();
        for (const f of fixed) solver.assign(varOf.get(f) as number, 1);
        free.forEach((f, bit) => {
          solver.assign(varOf.get(f) as number, ((mask >> bit) & 1) as 0 | 1);
        });
        const candidate = new Set(allFields.filter((f) => solver.value(varOf.get(f) as number) === 1));
        const view: View = {
          has: (f) => candidate.has(f),
          valueKnown: (f) => presentInInstance.has(f),
          get: (f) => getValue(instance, f),
        };
        for (const idx of ruleIdxs) {
          const rule = groups[idx];
          if (activation(rule, view, idx, false) !== 'active') continue;
          const { min, max } = boundsOf(rule);
          solver.addConstraint({
            id: `/groups/${idx}`,
            vars: rule.fields.map((f) => varOf.get(f) as number),
            min,
            max,
          });
        }
        const ok = solver.propagate(); // every var is assigned: this verifies the witness
        solver.pop(); // restores assignments AND added constraints
        if (ok) return false; // a witness exists: the subset is satisfiable
      }
      return true;
    };

    let core: CoreItem[] = [
      ...[...presentInInstance].sort().map((f): CoreItem => ({ kind: 'fact', field: f })),
      ...groups.map((_, i): CoreItem => ({ kind: 'rule', index: i })),
    ];
    // Deletion-based minimization: drop any item whose removal keeps unsat.
    for (const item of [...core]) {
      const trial = core.filter((x) => x !== item);
      if (subsetUnsat(trial)) core = trial;
    }
    conflictCores.push({
      facts: core
        .filter((i) => i.kind === 'fact')
        .map((i) => (i as { field: string }).field)
        .sort(),
      rules: core
        .filter((i) => i.kind === 'rule')
        .map((i) => `/groups/${(i as { index: number }).index}`),
    });
  }

  const status = instanceWorks ? 'valid' : found ? 'missing' : 'conflict';
  return {
    valid: status === 'valid' && issues.length === 0,
    status,
    issues,
    completions,
    conflictCores,
  };
}
