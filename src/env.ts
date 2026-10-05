// Hash-consed persistent environments.
//
// An environment is an immutable cons list of bindings. Every list is
// interned by content, so two environments with the same bindings are
// the *same object* and value equality is reference equality. This is
// what lets the incremental evaluator stop re-evaluating (equality
// cutoff) when an edit did not actually change the visible scope.

import type { NodeId } from './ast.ts';

export interface Env {
  readonly parent: Env | null;
  readonly name: string;
  readonly declId: NodeId;
  readonly blockId: NodeId;
  readonly key: string;
}

const table = new Map<string, Env>();

export function internEnv(parent: Env | null, name: string, declId: NodeId, blockId: NodeId): Env {
  const key = `${parent === null ? '' : parent.key}#${name}@${declId}`;
  let env = table.get(key);
  if (env === undefined) {
    env = { parent, name, declId, blockId, key };
    table.set(key, env);
  }
  return env;
}

// Walk the chain and return the frame that binds `name`, or null when
// the reference is unbound.
export function lookupEnv(env: Env | null, name: string): Env | null {
  let cur = env;
  while (cur !== null) {
    if (cur.name === name) return cur;
    cur = cur.parent;
  }
  return null;
}

// Innermost-first list of visible bindings.
export function flattenEnv(env: Env | null): Array<{ name: string; declId: NodeId }> {
  const out: Array<{ name: string; declId: NodeId }> = [];
  let cur = env;
  while (cur !== null) {
    out.push({ name: cur.name, declId: cur.declId });
    cur = cur.parent;
  }
  return out;
}
