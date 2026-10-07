export interface RuleContext {
  value(name: string): unknown;
  visible(name: string): boolean;
}

export type ValidateResult = string | null | undefined;
export type MaybePromise<T> = T | Promise<T>;

export type Validator = (
  value: unknown,
  ctx: RuleContext,
) => MaybePromise<ValidateResult>;

export interface FieldRules {
  /** Names of fields this field's rules read via the context. */
  deps?: readonly string[];
  /** Visibility rule; omitted means always visible. */
  visibleWhen?: (ctx: RuleContext) => boolean;
  /** Sync or async validator; returns an error message or null/undefined. */
  validate?: Validator;
}

export type FormSchema = Record<string, FieldRules>;

export interface FieldSnapshot {
  readonly value: unknown;
  readonly visible: boolean;
  readonly errors: readonly string[];
  readonly validating: boolean;
}
