import { JsonObject, Rule, RuleContext } from './types';

export interface RuleOptions {
  priority?: number;
  when?: (source: JsonObject) => boolean;
}

/** 1:1 key rename. Fully reversible. */
export function rename(id: string, from: string, to: string, opts: RuleOptions = {}): Rule {
  return {
    id,
    priority: opts.priority,
    when: opts.when,
    from: [from],
    to: [to],
    forward: (v) => ({ [to]: v[from] }),
    backward: (v) => ({ [from]: v[to] }),
  };
}

function requireString(ruleId: string, key: string, value: unknown): string {
  if (typeof value !== 'string') {
    throw new Error(`Rule "${ruleId}": expected string at key "${key}", got ${typeof value}`);
  }
  return value;
}

/**
 * One-to-many split of a string key. The value is split on the first
 * `to.length - 1` separators, so joining the parts restores the original
 * (reversible). When fewer separators exist than needed, the missing parts
 * are omitted and a loss is recorded, because the backward join cannot
 * reconstruct which parts were absent.
 */
export function split(
  id: string,
  from: string,
  to: readonly string[],
  separator: string,
  opts: RuleOptions = {},
): Rule {
  if (to.length < 2) throw new Error(`Rule "${id}": split needs at least 2 target keys`);
  return {
    id,
    priority: opts.priority,
    when: opts.when,
    from: [from],
    to,
    forward: (values, ctx) => {
      const raw = requireString(id, from, values[from]);
      const pieces = raw.split(separator);
      const out: Record<string, unknown> = {};
      const missing: string[] = [];
      for (let i = 0; i < to.length; i++) {
        if (i >= pieces.length) {
          missing.push(to[i]);
          continue;
        }
        const isLast = i === to.length - 1;
        out[to[i]] = isLast ? pieces.slice(i).join(separator) : pieces[i];
      }
      if (missing.length > 0) {
        ctx.addLoss({
          keys: [from],
          reason: `value had fewer than ${to.length - 1} separator(s) "${separator}"; ` +
            `no source for ${missing.join(', ')}; backward join cannot restore the original shape`,
          value: raw,
        });
      }
      return out;
    },
    backward: (values, ctx) => {
      const parts: string[] = [];
      const missing: string[] = [];
      for (const key of to) {
        if (values[key] === undefined) {
          missing.push(key);
        } else {
          parts.push(requireString(id, key, values[key]));
        }
      }
      if (missing.length > 0) {
        ctx.addLoss({
          keys: missing,
          reason: `missing part(s) ${missing.join(', ')}; joined value may differ from the original`,
        });
      }
      return { [from]: parts.join(separator) };
    },
  };
}

/**
 * Many-to-one merge of string keys joined by a separator. Backward splits on
 * the first `from.length - 1` separators (reversible). If some source keys
 * are absent at merge time, a loss is recorded because the split cannot
 * restore which keys were missing.
 */
export function merge(
  id: string,
  from: readonly string[],
  to: string,
  separator: string,
  opts: RuleOptions = {},
): Rule {
  if (from.length < 2) throw new Error(`Rule "${id}": merge needs at least 2 source keys`);
  return {
    id,
    priority: opts.priority,
    when: opts.when,
    from,
    to: [to],
    forward: (values, ctx) => {
      const parts: string[] = [];
      const missing: string[] = [];
      for (const key of from) {
        if (values[key] === undefined) {
          missing.push(key);
        } else {
          parts.push(requireString(id, key, values[key]));
        }
      }
      if (missing.length > 0) {
        ctx.addLoss({
          keys: missing,
          reason: `source key(s) ${missing.join(', ')} absent; backward split cannot restore their absence`,
          value: pickDefined(values, from),
        });
      }
      return { [to]: parts.join(separator) };
    },
    backward: (values, ctx) => {
      const raw = requireString(id, to, values[to]);
      const pieces = raw.split(separator);
      const out: Record<string, unknown> = {};
      if (pieces.length < from.length) {
        ctx.addLoss({
          keys: [to],
          reason: `merged value has fewer than ${from.length - 1} separator(s) "${separator}"; ` +
            'cannot restore all original keys',
          value: raw,
        });
      }
      for (let i = 0; i < from.length && i < pieces.length; i++) {
        const isLast = i === from.length - 1;
        out[from[i]] = isLast ? pieces.slice(i).join(separator) : pieces[i];
      }
      return out;
    },
  };
}

/** Generic rule for custom transforms; supply `backward` for the reversible part. */
export function transform(spec: Rule): Rule {
  return spec;
}

function pickDefined(values: Record<string, unknown>, keys: readonly string[]): JsonObject {
  const out: JsonObject = {};
  for (const k of keys) if (values[k] !== undefined) out[k] = values[k];
  return out;
}
