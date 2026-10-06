/**
 * Patterns over algebraic data types.
 *
 * A pattern is either a wildcard `_`, a variable binding (which matches
 * exactly like a wildcard for the purpose of checking), or a constructor
 * applied to nested sub-patterns. Nesting depth is finite by construction.
 */

import { Value } from "./types";

export type Pattern =
  | { kind: "wild" }
  | { kind: "var"; name: string }
  | { kind: "ctor"; ctor: string; args: Pattern[] };

export const wild = (): Pattern => ({ kind: "wild" });
export const varp = (name: string): Pattern => ({ kind: "var", name });
export const ctor = (name: string, ...args: Pattern[]): Pattern => ({
  kind: "ctor",
  ctor: name,
  args,
});

/** Shared wildcard singleton for concise pattern literals. */
export const _: Pattern = wild();

export function showPattern(p: Pattern): string {
  switch (p.kind) {
    case "wild":
      return "_";
    case "var":
      return p.name;
    case "ctor":
      return p.args.length === 0
        ? p.ctor
        : `${p.ctor}(${p.args.map(showPattern).join(", ")})`;
  }
}

export function matchPattern(p: Pattern, v: Value): boolean {
  switch (p.kind) {
    case "wild":
    case "var":
      return true;
    case "ctor":
      return (
        p.ctor === v.ctor &&
        p.args.length === v.args.length &&
        p.args.every((sub, i) => matchPattern(sub, v.args[i]!))
      );
  }
}
