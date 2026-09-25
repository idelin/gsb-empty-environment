import type { JsonValue } from './json.ts';
import { deepClone, isPlainObject } from './json.ts';
import { compileRule, matchPattern, buildTarget } from './pattern.ts';
import type { CompiledRule } from './pattern.ts';
import { formatPath } from './path.ts';

export interface Rule {
  from: string;
  to: string;
}

export interface MoveOp {
  /** Concrete source path segments in the original tree. */
  from: string[];
  /** Concrete target path segments. */
  to: string[];
  /** Snapshot of the value at `from` (deep-cloned). */
  value: JsonValue;
  /** Index of the rule that produced this op. */
  ruleIndex: number;
}

export interface PlannedMove extends MoveOp {
  fromPath: string;
  toPath: string;
}

/**
 * Build the full move plan by matching rules against a snapshot of the
 * ORIGINAL tree. Target nodes produced by the plan are never re-matched,
 * so rules cannot recursively rewrite their own output.
 *
 * Per node, the first matching rule wins; once a node matches, its subtree
 * is moved wholesale and is not descended into for further matching.
 */
export function planMoves(tree: JsonValue, rules: Rule[]): PlannedMove[] {
  const compiled: CompiledRule[] = rules.map((r, i) => compileRule(r.from, r.to, i));
  const ops: PlannedMove[] = [];

  const visit = (node: JsonValue, path: string[]): void => {
    for (const rule of compiled) {
      const captures = matchPattern(rule.from, path);
      if (!captures) continue;
      const to = buildTarget(rule.to, captures);
      ops.push({
        from: path,
        to,
        value: deepClone(node),
        ruleIndex: rule.index,
        fromPath: formatPath(path),
        toPath: formatPath(to),
      });
      return; // matched subtree is moved wholesale; do not descend
    }
    if (Array.isArray(node)) {
      node.forEach((child, i) => visit(child, [...path, String(i)]));
    } else if (isPlainObject(node)) {
      for (const key of Object.keys(node)) {
        visit(node[key] as JsonValue, [...path, key]);
      }
    }
  };

  visit(tree, []);
  return ops;
}
