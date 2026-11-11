import type { AccessPath } from "./model.ts";

export interface TaintTag {
  sourceId: string;
  fn: string;
  ctx: string[];
}

export interface TaintFact {
  base: string;
  fields: string[];
  tag: TaintTag;
}

export type State = Map<string, TaintFact>;

export function tagKey(tag: TaintTag): string {
  return `${tag.sourceId}@${tag.fn}@${JSON.stringify(tag.ctx)}`;
}

export function factKey(fact: TaintFact): string {
  return `${fact.base}.${fact.fields.join(".")}#${tagKey(fact.tag)}`;
}

export function isPrefixFields(prefix: string[], full: string[]): boolean {
  if (prefix.length > full.length) return false;
  for (let i = 0; i < prefix.length; i++) {
    if (prefix[i] !== full[i]) return false;
  }
  return true;
}

export function addFact(state: State, fact: TaintFact, maxDepth: number): boolean {
  const truncated: TaintFact = {
    base: fact.base,
    fields: fact.fields.slice(0, maxDepth),
    tag: fact.tag,
  };
  const tk = tagKey(truncated.tag);
  for (const existing of state.values()) {
    if (
      tagKey(existing.tag) === tk &&
      existing.base === truncated.base &&
      isPrefixFields(existing.fields, truncated.fields)
    ) {
      return false;
    }
  }
  for (const [key, existing] of state) {
    if (
      tagKey(existing.tag) === tk &&
      existing.base === truncated.base &&
      isPrefixFields(truncated.fields, existing.fields)
    ) {
      state.delete(key);
    }
  }
  state.set(factKey(truncated), truncated);
  return true;
}

export function joinInto(dst: State, src: State, maxDepth: number): boolean {
  let changed = false;
  for (const fact of src.values()) {
    if (addFact(dst, fact, maxDepth)) changed = true;
  }
  return changed;
}

export function readTaint(state: State, base: string, fields: string[]): TaintTag[] {
  const out = new Map<string, TaintTag>();
  for (const fact of state.values()) {
    if (fact.base !== base) continue;
    if (isPrefixFields(fact.fields, fields) || isPrefixFields(fields, fact.fields)) {
      out.set(tagKey(fact.tag), fact.tag);
    }
  }
  return [...out.values()];
}

export function transferRead(
  state: State,
  base: string,
  fields: string[],
): Array<{ suffix: string[]; tag: TaintTag }> {
  const out: Array<{ suffix: string[]; tag: TaintTag }> = [];
  const seen = new Set<string>();
  for (const fact of state.values()) {
    if (fact.base !== base) continue;
    let suffix: string[] | null = null;
    if (isPrefixFields(fields, fact.fields)) {
      suffix = fact.fields.slice(fields.length);
    } else if (isPrefixFields(fact.fields, fields)) {
      suffix = [];
    }
    if (suffix === null) continue;
    const key = `${suffix.join(".")}#${tagKey(fact.tag)}`;
    if (!seen.has(key)) {
      seen.add(key);
      out.push({ suffix, tag: fact.tag });
    }
  }
  return out;
}

export function killRootedAt(state: State, base: string, fields: string[]): boolean {
  let changed = false;
  for (const [key, fact] of state) {
    if (fact.base === base && isPrefixFields(fields, fact.fields)) {
      state.delete(key);
      changed = true;
    }
  }
  return changed;
}

export function applyAssign(
  state: State,
  target: AccessPath,
  from: AccessPath,
  maxDepth: number,
): void {
  if (target.fields.length === 0) {
    killRootedAt(state, target.base, []);
  }
  for (const { suffix, tag } of transferRead(state, from.base, from.fields)) {
    addFact(
      state,
      { base: target.base, fields: [...target.fields, ...suffix], tag },
      maxDepth,
    );
  }
}
