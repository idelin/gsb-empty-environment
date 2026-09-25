// Normalization engine: applies an explicitly ordered rule set to a JSON
// tree and records per-field changes, path-located errors and inverse
// patches. Errors never destroy data: the original value is preserved.

import {
  deepEqual,
  getAt,
  pathToString,
  removeAt,
  setAt,
  walk,
} from "./json.ts";
import type { JsonValue, PathSegment } from "./json.ts";
import { matchPattern, transformValue } from "./rules.ts";
import type { Rule } from "./rules.ts";

/** Marker for "the field did not exist before the rule ran". */
export const ABSENT = Symbol("absent");
export type MaybeAbsent = JsonValue | typeof ABSENT;

export interface ChangeRecord {
  /** Global sequence number within one normalization run. */
  seq: number;
  ruleId: string;
  /** Field identity: exact path into the tree. */
  path: PathSegment[];
  /** Field identity rendered as a JSON-Pointer string. */
  field: string;
  before: MaybeAbsent;
  after: MaybeAbsent;
}

export interface ErrorRecord {
  ruleId: string;
  path: PathSegment[];
  field: string;
  message: string;
  /** The offending value, preserved verbatim in the output tree. */
  value: JsonValue;
}

export interface PatchOp {
  op: "replace" | "add" | "remove";
  path: PathSegment[];
  field: string;
  ruleId: string;
  value?: JsonValue;
}

export interface NormalizeResult {
  output: JsonValue;
  changes: ChangeRecord[];
  errors: ErrorRecord[];
  /** Ops that restore the input when applied to `output`, in order. */
  inverse: PatchOp[];
}

export function buildInverse(changes: ChangeRecord[]): PatchOp[] {
  return changes
    .slice()
    .reverse()
    .map((c) =>
      c.before === ABSENT
        ? { op: "remove" as const, path: [...c.path], field: c.field, ruleId: c.ruleId }
        : {
            op: "replace" as const,
            path: [...c.path],
            field: c.field,
            ruleId: c.ruleId,
            value: c.before,
          },
    );
}

export function applyPatchOps(tree: JsonValue, ops: PatchOp[]): JsonValue {
  let current = tree;
  for (const op of ops) {
    if (op.op === "remove") {
      current = removeAt(current, op.path);
    } else {
      current = setAt(current, op.path, op.value ?? null);
    }
  }
  return current;
}

function sortedMatchedPaths(root: JsonValue, rule: Rule): PathSegment[][] {
  const seen = new Map<string, PathSegment[]>();
  for (const { path } of walk(root)) {
    for (const pattern of rule.targets) {
      if (matchPattern(pattern, path)) {
        seen.set(pathToString(path), path);
        break;
      }
    }
  }
  return [...seen.entries()]
    .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
    .map(([, path]) => path);
}

/** Applies `rules` to `input` in array order. Pure: `input` is not mutated. */
export function applyRules(input: JsonValue, rules: readonly Rule[]): NormalizeResult {
  let current = input;
  const changes: ChangeRecord[] = [];
  const errors: ErrorRecord[] = [];
  let seq = 0;

  for (const rule of rules) {
    if (rule.kind === "copy") {
      const src = getAt(current, rule.from);
      if (!src.found) {
        errors.push({
          ruleId: rule.id,
          path: [...rule.from],
          field: pathToString(rule.from),
          message: "copy source path missing",
          value: null,
        });
        continue;
      }
      const dst = getAt(current, rule.to);
      if (dst.found && deepEqual(dst.value, src.value)) continue;
      changes.push({
        seq: seq++,
        ruleId: rule.id,
        path: [...rule.to],
        field: pathToString(rule.to),
        before: dst.found ? (dst.value as JsonValue) : ABSENT,
        after: src.value as JsonValue,
      });
      current = setAt(current, rule.to, src.value as JsonValue);
      continue;
    }

    for (const path of sortedMatchedPaths(current, rule)) {
      const got = getAt(current, path);
      if (!got.found) continue;
      const value = got.value as JsonValue;
      const result = transformValue(rule, value);
      if (result.status === "error") {
        errors.push({
          ruleId: rule.id,
          path: [...path],
          field: pathToString(path),
          message: result.error,
          value,
        });
      } else if (result.status === "changed") {
        changes.push({
          seq: seq++,
          ruleId: rule.id,
          path: [...path],
          field: pathToString(path),
          before: value,
          after: result.value,
        });
        current = setAt(current, path, result.value);
      }
    }
  }

  return { output: current, changes, errors, inverse: buildInverse(changes) };
}

/** True when running the rules a second time changes nothing further. */
export function isStableAfterOnePass(rules: readonly Rule[], input: JsonValue): boolean {
  const once = applyRules(input, rules);
  const twice = applyRules(once.output, rules);
  return deepEqual(once.output, twice.output);
}
