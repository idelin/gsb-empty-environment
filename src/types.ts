/** A plain config object. Keys are flat strings (dotted names are just names). */
export type ConfigObject = Record<string, unknown>;

/** A piece of information that could not be carried through a transform. */
export interface LossEntry {
  /** Rule that produced this loss. */
  ruleId: string;
  /** Key the lost data belonged to. */
  key: string;
  /** Human-readable reason the data could not be preserved. */
  reason: string;
  /** The dropped data, when available. */
  value?: unknown;
}

/** Loss as reported by a rule's transform (ruleId is attached by the engine). */
export type RuleLoss = Omit<LossEntry, 'ruleId'>;

/** Where a produced key came from. */
export interface ProvenanceEntry {
  /** The produced (new) key. */
  key: string;
  /** Rule that produced it. */
  ruleId: string;
  /** Source keys the value was derived from. */
  sources: string[];
}

/** Output of a rule's forward transform. */
export interface ForwardResult {
  /** New key -> value. Keys must be declared in the rule's `newKeys`. */
  values: ConfigObject;
  /** Information dropped by the transform (irreversible part). */
  losses?: RuleLoss[];
}

/**
 * A single migration rule.
 *
 * Rules may split one key into many (oldKeys.length === 1, newKeys.length > 1),
 * merge many keys into one (oldKeys.length > 1, newKeys.length === 1),
 * rename (1 -> 1) or do any N -> M reshaping.
 */
export interface MigrationRule {
  /** Unique, stable rule identifier. */
  id: string;
  /**
   * Higher priority runs earlier (forward) / later (reverse).
   * Ties break by `id` ascending so execution is deterministic. Default 0.
   */
  priority?: number;
  /**
   * Precondition evaluated against the current (in-progress) config.
   * The rule is skipped when it returns false.
   */
  when?: (config: ConfigObject) => boolean;
  /** Old keys consumed by this rule. */
  oldKeys: string[];
  /** New keys produced by this rule. */
  newKeys: string[];
  /**
   * Forward transform. Receives the present subset of `oldKeys`
   * and returns the new values plus any losses.
   */
  forward(oldValues: ConfigObject): ForwardResult;
  /**
   * Reverse transform for the reversible part of the rule.
   * Receives the present subset of `newKeys`, returns old key -> value.
   * Omit it when the rule is not reversible; `unmigrate` then records
   * a loss and leaves the new keys untouched.
   */
  backward?(newValues: ConfigObject): ConfigObject;
}

/** Result of `migrate` / `unmigrate`. */
export interface MigrateResult {
  /** The migrated config (input is never mutated). */
  config: ConfigObject;
  /** Ids of rules that actually ran, in execution order. */
  appliedRules: string[];
  /** Origin of every produced key. */
  provenance: ProvenanceEntry[];
  /** All losses recorded while running transforms. */
  losses: LossEntry[];
  /**
   * Keys present in the input that no rule consumed and that are not
   * declared as any rule's target keys — i.e. leftover unknown old fields.
   */
  unconsumed: string[];
}
