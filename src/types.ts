export type JsonObject = Record<string, unknown>;

/** Where a produced value came from. */
export interface Provenance {
  /** Rule that produced the value; null when the key already existed in the input. */
  rule: string | null;
  /** Source keys that were consumed to produce this value. */
  from: string[];
}

/** A piece of information that could not be (re)constructed losslessly. */
export interface LossEntry {
  rule: string;
  phase: 'forward' | 'backward';
  /** Keys affected by the loss. */
  keys: string[];
  reason: string;
  /** The data that was lost, so callers can archive it. */
  value?: unknown;
}

/** A new key whose derived value contradicts a value already present. */
export interface ConflictEntry {
  rule: string;
  key: string;
  /** Value already present (from the input's new-schema key or an earlier rule). */
  existing: unknown;
  /** Value derived by the rule from old keys. */
  incoming: unknown;
}

export class MigrationConflictError extends Error {
  readonly conflicts: ConflictEntry[];
  constructor(conflicts: ConflictEntry[]) {
    super(
      'Migration conflict: old and new keys disagree; refusing to guess.\n' +
        conflicts
          .map(
            (c) =>
              `  key "${c.key}" (rule "${c.rule}"): existing=${JSON.stringify(
                c.existing,
              )} incoming=${JSON.stringify(c.incoming)}`,
          )
          .join('\n'),
    );
    this.name = 'MigrationConflictError';
    this.conflicts = conflicts;
  }
}

export interface RuleContext {
  /** The original input config (never mutated). */
  readonly source: JsonObject;
  /** True if a new key has already been produced (or was present in the input). */
  has(key: string): boolean;
  /** Current value of an already-produced new key. */
  get(key: string): unknown;
  /** Record information that this transform cannot round-trip. */
  addLoss(loss: { keys: string[]; reason: string; value?: unknown }): void;
}

export interface Rule {
  id: string;
  /** Higher priority runs first. Defaults to 0. Ties keep registration order. */
  priority?: number;
  /** Precondition on the source config; the rule is skipped when it returns false. */
  when?: (source: JsonObject) => boolean;
  /** Old-schema keys this rule consumes. */
  from: readonly string[];
  /** New-schema keys this rule produces. */
  to: readonly string[];
  forward(values: Record<string, unknown>, ctx: RuleContext): Record<string, unknown>;
  /** Reverse transform for the reversible part of the rule. */
  backward?(values: Record<string, unknown>, ctx: RuleContext): Record<string, unknown>;
}

export interface MigrationResult {
  /** The migrated config. */
  config: JsonObject;
  /** Provenance of every key in `config`. */
  provenance: Record<string, Provenance>;
  /** Information lost by non-round-trippable transforms. */
  losses: LossEntry[];
  /** Input keys not consumed by any rule. */
  unconsumed: string[];
  /** Ids of rules that ran, in execution order. */
  appliedRules: string[];
}

export interface MigrateOptions {
  /** Copy unconsumed input keys into the output. Defaults to false. */
  carryUnknown?: boolean;
}
