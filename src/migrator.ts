import {
  ConflictEntry,
  JsonObject,
  LossEntry,
  MigrateOptions,
  MigrationConflictError,
  MigrationResult,
  Provenance,
  Rule,
  RuleContext,
} from './types';
import { deepEqual } from './util';

type Phase = 'forward' | 'backward';

export class Migrator {
  private readonly rules: Rule[] = [];

  constructor(rules: Rule[] = []) {
    rules.forEach((r) => this.register(r));
  }

  register(rule: Rule): this {
    if (!rule.id) throw new Error('Rule must have an id');
    if (this.rules.some((r) => r.id === rule.id)) {
      throw new Error(`Duplicate rule id "${rule.id}"`);
    }
    if (!rule.from.length || !rule.to.length) {
      throw new Error(`Rule "${rule.id}" must declare non-empty from/to keys`);
    }
    this.rules.push(rule);
    return this;
  }

  /** Migrate an old-schema config to the new schema. */
  migrate(source: JsonObject, options: MigrateOptions = {}): MigrationResult {
    return this.run(source, 'forward', options);
  }

  /** Migrate a new-schema config back to the old schema using reversible rules. */
  revert(config: JsonObject, options: MigrateOptions = {}): MigrationResult {
    return this.run(config, 'backward', options);
  }

  private run(input: JsonObject, phase: Phase, options: MigrateOptions): MigrationResult {
    const ordered = [...this.rules]
      .map((rule, index) => ({ rule, index }))
      .sort((a, b) => (b.rule.priority ?? 0) - (a.rule.priority ?? 0) || a.index - b.index)
      .map((e) => e.rule);
    if (phase === 'backward') ordered.reverse();

    const inputKeysOf = (r: Rule): readonly string[] => (phase === 'forward' ? r.from : r.to);
    const outputKeysOf = (r: Rule): readonly string[] => (phase === 'forward' ? r.to : r.from);

    const target: JsonObject = {};
    const provenance: Record<string, Provenance> = {};
    const losses: LossEntry[] = [];
    const appliedRules: string[] = [];
    const consumed = new Set<string>();
    const conflicts: ConflictEntry[] = [];

    // Pass through keys that already belong to the target schema. This makes
    // re-migration a fixed point and sets up conflict detection for inputs
    // that contain both old and new keys.
    for (const rule of ordered) {
      for (const key of outputKeysOf(rule)) {
        if (key in input && !(key in target)) {
          target[key] = input[key];
          consumed.add(key);
          provenance[key] = { rule: null, from: [key] };
        }
      }
    }

    for (const rule of ordered) {
      const inKeys = inputKeysOf(rule);
      const outKeys = outputKeysOf(rule);
      if (phase === 'forward' && rule.when && !rule.when(input)) continue;

      const present = inKeys.filter((k) => k in input);
      if (present.length === 0) continue;

      if (phase === 'backward' && !rule.backward) {
        losses.push({
          rule: rule.id,
          phase,
          keys: present,
          reason: 'rule is not reversible; value(s) cannot be restored',
          value: pick(input, present),
        });
        present.forEach((k) => consumed.add(k));
        continue;
      }

      const ctx: RuleContext = {
        source: input,
        has: (key) => key in target,
        get: (key) => target[key],
        addLoss: (loss) => losses.push({ rule: rule.id, phase, ...loss }),
      };

      const values: Record<string, unknown> = {};
      for (const k of inKeys) values[k] = input[k];
      const transform = phase === 'forward' ? rule.forward : rule.backward!;
      const produced = transform(values, ctx);

      for (const key of Object.keys(produced)) {
        if (!outKeys.includes(key)) {
          throw new Error(
            `Rule "${rule.id}" produced undeclared key "${key}" (declared: ${outKeys.join(', ')})`,
          );
        }
      }
      appliedRules.push(rule.id);

      for (const [key, value] of Object.entries(produced)) {
        if (key in target) {
          if (deepEqual(target[key], value)) {
            // Old and new agree: consume the old keys, keep the value.
            provenance[key] = { rule: rule.id, from: present };
          } else {
            conflicts.push({ rule: rule.id, key, existing: target[key], incoming: value });
          }
        } else {
          target[key] = value;
          provenance[key] = { rule: rule.id, from: present };
        }
      }
      present.forEach((k) => consumed.add(k));
    }

    if (conflicts.length > 0) throw new MigrationConflictError(conflicts);

    const unconsumed = Object.keys(input).filter((k) => !consumed.has(k));
    if (options.carryUnknown) {
      for (const key of unconsumed) {
        target[key] = input[key];
        provenance[key] = { rule: null, from: [key] };
      }
    }

    return { config: target, provenance, losses, unconsumed, appliedRules };
  }
}

function pick(obj: JsonObject, keys: string[]): JsonObject {
  const out: JsonObject = {};
  for (const k of keys) if (k in obj) out[k] = obj[k];
  return out;
}
