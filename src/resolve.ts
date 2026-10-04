import type { Env, Type } from "./types.js";

// Resolves chained `ref` nodes to a concrete (non-ref) type.
// Throws on unknown names and on degenerate aliases such as `A = A`.
export function resolve(t: Type, env: Env): Type {
  const seen: Set<string> = new Set();
  let cur = t;
  while (cur.kind === "ref") {
    if (seen.has(cur.name)) {
      throw new Error(`degenerate recursive type alias: ${cur.name}`);
    }
    seen.add(cur.name);
    const next = env.get(cur.name);
    if (next === undefined) {
      throw new Error(`unknown type reference: ${cur.name}`);
    }
    cur = next;
  }
  return cur;
}
