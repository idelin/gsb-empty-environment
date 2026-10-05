import { deepClone, deepEqual } from './equal.js';
import { MigrationConflictError } from './errors.js';
import type {
  ConfigObject,
  LossEntry,
  MigrateResult,
  MigrationRule,
  ProvenanceEntry,
} from './types.js';

const hasOwn = (obj: object, key: string): boolean =>
  Object.prototype.hasOwnProperty.call(obj, key);

const pick = (config: ConfigObject, keys: string[]): ConfigObject => {
  const out: ConfigObject = {};
  for (const key of keys) out[key] = config[key];
  return out;
};

const priorityOf = (rule: MigrationRule): number => rule.priority ?? 0;

const compareIds = (a: string, b: string): number => (a < b ? -1 : a > b ? 1 : 0);

/** Forward order: higher priority first, ties by id ascending. */
const forwardOrder = (rules: MigrationRule[]): MigrationRule[] =>
  [...rules].sort(
    (a, b) => priorityOf(b) - priorityOf(a) || compareIds(a.id, b.id),
  );

/** Reverse order: exact inverse of forward application order. */
const reverseOrder = (rules: MigrationRule[]): MigrationRule[] =>
  [...rules].sort(
    (a, b) => priorityOf(a) - priorityOf(b) || compareIds(b.id, a.id),
  );

function assertDeclaredKeys(
  rule: MigrationRule,
  values: ConfigObject,
  declared: string[],
  direction: 'forward' | 'backward',
): void {
  for (const key of Object.keys(values)) {
    if (!declared.includes(key)) {
      throw new Error(
        `Rule "${rule.id}" ${direction} produced undeclared key "${key}".`,
      );
    }
  }
}

function assertNoConflict(
  rule: MigrationRule,
  working: ConfigObject,
  values: ConfigObject,
): void {
  for (const [key, value] of Object.entries(values)) {
    if (hasOwn(working, key) && !deepEqual(working[key], value)) {
      throw new MigrationConflictError(rule.id, key, working[key], value);
    }
  }
}

/**
 * Migrate a config forward: old keys -> new keys.
 *
 * - Rules run in priority order (high first, id ascending on ties).
 * - A rule runs only when its `when` precondition passes and at least one
 *   of its `oldKeys` is present.
 * - If a produced new key already exists with an equal value, the old keys
 *   are consumed silently (this makes repeated migration stable).
 * - If it exists with a different value, a MigrationConflictError is thrown
 *   instead of guessing.
 */
export function migrate(input: ConfigObject, rules: MigrationRule[]): MigrateResult {
  const working = deepClone(input);
  const consumed = new Set<string>();
  const declaredNewKeys = new Set<string>();
  for (const rule of rules) {
    for (const key of rule.newKeys) declaredNewKeys.add(key);
  }

  const appliedRules: string[] = [];
  const provenance: ProvenanceEntry[] = [];
  const losses: LossEntry[] = [];

  for (const rule of forwardOrder(rules)) {
    if (rule.when && !rule.when(working)) continue;
    const presentOld = rule.oldKeys.filter((key) => hasOwn(working, key));
    if (presentOld.length === 0) continue;

    const result = rule.forward(pick(working, presentOld));
    assertDeclaredKeys(rule, result.values, rule.newKeys, 'forward');
    assertNoConflict(rule, working, result.values);

    for (const key of presentOld) {
      delete working[key];
      consumed.add(key);
    }
    for (const [key, value] of Object.entries(result.values)) {
      working[key] = value;
      provenance.push({ key, ruleId: rule.id, sources: [...presentOld] });
    }
    for (const loss of result.losses ?? []) {
      losses.push({ ruleId: rule.id, ...loss });
    }
    appliedRules.push(rule.id);
  }

  const unconsumed = Object.keys(input).filter(
    (key) => !consumed.has(key) && !declaredNewKeys.has(key),
  );
  return { config: working, appliedRules, provenance, losses, unconsumed };
}

/**
 * Migrate a config backward: new keys -> old keys, using each rule's
 * `backward` transform in the exact inverse of forward order.
 *
 * Rules without a `backward` transform are not reversible: their new keys
 * are left in place and a loss is recorded.
 */
export function unmigrate(input: ConfigObject, rules: MigrationRule[]): MigrateResult {
  const working = deepClone(input);
  const consumed = new Set<string>();
  const declaredOldKeys = new Set<string>();
  for (const rule of rules) {
    for (const key of rule.oldKeys) declaredOldKeys.add(key);
  }

  const appliedRules: string[] = [];
  const provenance: ProvenanceEntry[] = [];
  const losses: LossEntry[] = [];

  for (const rule of reverseOrder(rules)) {
    const presentNew = rule.newKeys.filter((key) => hasOwn(working, key));
    if (presentNew.length === 0) continue;

    if (!rule.backward) {
      losses.push({
        ruleId: rule.id,
        key: presentNew.join(', '),
        reason: 'rule is not reversible; new key(s) left in place',
        value: pick(working, presentNew),
      });
      continue;
    }

    const oldValues = rule.backward(pick(working, presentNew));
    assertDeclaredKeys(rule, oldValues, rule.oldKeys, 'backward');
    assertNoConflict(rule, working, oldValues);

    for (const key of presentNew) {
      delete working[key];
      consumed.add(key);
    }
    for (const [key, value] of Object.entries(oldValues)) {
      working[key] = value;
      provenance.push({ key, ruleId: rule.id, sources: [...presentNew] });
    }
    appliedRules.push(rule.id);
  }

  const unconsumed = Object.keys(input).filter(
    (key) => !consumed.has(key) && !declaredOldKeys.has(key),
  );
  return { config: working, appliedRules, provenance, losses, unconsumed };
}
