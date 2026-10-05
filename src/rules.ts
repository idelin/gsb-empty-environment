import type { ConfigObject, MigrationRule } from './types.js';

export interface RuleOptions {
  priority?: number;
  when?: (config: ConfigObject) => boolean;
}

/** Convenience factory for a reversible 1:1 key rename. */
export function rename(id: string, from: string, to: string, options: RuleOptions = {}): MigrationRule {
  return {
    id,
    priority: options.priority,
    when: options.when,
    oldKeys: [from],
    newKeys: [to],
    forward: (old) => ({ values: { [to]: old[from] } }),
    backward: (current) => ({ [from]: current[to] }),
  };
}

/**
 * Convenience factory for a one-to-many split.
 * `split` maps the old value to new key -> value; `join`, when provided,
 * makes the rule reversible.
 */
export function split(
  id: string,
  from: string,
  to: string[],
  splitFn: (value: unknown) => ConfigObject,
  joinFn?: (values: ConfigObject) => unknown,
  options: RuleOptions = {},
): MigrationRule {
  return {
    id,
    priority: options.priority,
    when: options.when,
    oldKeys: [from],
    newKeys: to,
    forward: (old) => ({ values: splitFn(old[from]) }),
    ...(joinFn ? { backward: (current) => ({ [from]: joinFn(current) }) } : {}),
  };
}

/**
 * Convenience factory for a many-to-one merge.
 * `mergeFn` maps present old values to the merged value; `splitFn`, when
 * provided, makes the rule reversible.
 */
export function merge(
  id: string,
  from: string[],
  to: string,
  mergeFn: (values: ConfigObject) => unknown,
  splitFn?: (value: unknown) => ConfigObject,
  options: RuleOptions = {},
): MigrationRule {
  return {
    id,
    priority: options.priority,
    when: options.when,
    oldKeys: from,
    newKeys: [to],
    forward: (old) => ({ values: { [to]: mergeFn(old) } }),
    ...(splitFn ? { backward: (current) => splitFn(current[to]) } : {}),
  };
}
