import type { FixRule } from "./engine.ts";
import { deepEqual, type JsonValue } from "./json.ts";
import { getAt, hasAt } from "./pointer.ts";

/** Rewrites `path` from `from` to `to`; precondition: current value === `from`. */
export function rewriteValue(
  id: string,
  priority: number,
  path: string,
  from: JsonValue,
  to: JsonValue,
): FixRule {
  return {
    id,
    priority,
    match(state) {
      if (!hasAt(state, path)) return [];
      if (!deepEqual(getAt(state, path), from)) return [];
      return [{ path, expect: from, replacement: to }];
    },
  };
}

/** Coerces the strings "true"/"false" to booleans at the given paths. */
export function coerceBooleanStrings(
  id: string,
  priority: number,
  paths: string[],
): FixRule {
  return {
    id,
    priority,
    match(state) {
      const matches = [];
      for (const path of paths) {
        if (!hasAt(state, path)) continue;
        const value = getAt(state, path);
        if (value === "true") matches.push({ path, expect: "true", replacement: true });
        else if (value === "false") matches.push({ path, expect: "false", replacement: false });
      }
      return matches;
    },
  };
}

/** Trims whitespace of string values at the given paths. */
export function trimStrings(id: string, priority: number, paths: string[]): FixRule {
  return {
    id,
    priority,
    match(state) {
      const matches = [];
      for (const path of paths) {
        if (!hasAt(state, path)) continue;
        const value = getAt(state, path);
        if (typeof value === "string" && value !== value.trim()) {
          matches.push({ path, expect: value, replacement: value.trim() });
        }
      }
      return matches;
    },
  };
}
