import type { Constraint, JsonValue } from "./types.ts";
import { deepEqual } from "./canonical.ts";

interface CompiledForbid {
  kind: "forbid";
  /** [axisIndex, expectedValue] 对，按轴序排列。 */
  entries: { axis: number; value: JsonValue }[];
}

interface CompiledRequire {
  kind: "require";
  when: { axis: number; value: JsonValue }[];
  allow: { axis: number; values: JsonValue[] }[];
}

export type CompiledConstraint = CompiledForbid | CompiledRequire;

export function compileConstraints(
  constraints: Constraint[],
  axisNames: string[]
): CompiledConstraint[] {
  const indexOf = new Map(axisNames.map((n, i) => [n, i]));
  return constraints.map((c) => {
    if ("forbid" in c) {
      const entries = Object.keys(c.forbid)
        .sort()
        .map((name) => ({ axis: indexOf.get(name)!, value: c.forbid[name]! }));
      return { kind: "forbid", entries };
    }
    const when = Object.keys(c.require)
      .sort()
      .map((name) => ({ axis: indexOf.get(name)!, value: c.require[name]! }));
    const allow = Object.keys(c.allow)
      .sort()
      .map((name) => ({ axis: indexOf.get(name)!, values: c.allow[name]! }));
    return { kind: "require", when, allow };
  });
}

function matchesPrefix(
  equalities: { axis: number; value: JsonValue }[],
  pos: number[],
  domains: JsonValue[][],
  depth: number
): "contradicted" | "partial" | "full" {
  let allAssigned = true;
  for (const { axis, value } of equalities) {
    if (axis > depth) {
      allAssigned = false;
      continue;
    }
    if (!deepEqual(domains[axis]![pos[axis]!]!, value)) return "contradicted";
  }
  return allAssigned ? "full" : "partial";
}

/**
 * 约束传播（前向检查）：判断前缀 pos[0..depth] 是否仍可能扩展为可行组合。
 * - forbid：所有轴均已赋值且全部匹配 -> 不可行。
 * - require：when 完全匹配时，allow 轴若已赋值必须在允许列表内；
 *   若未赋值，则其值域与允许列表的交集必须非空，否则整枝剪掉。
 */
export function prefixFeasible(
  pos: number[],
  depth: number,
  domains: JsonValue[][],
  compiled: CompiledConstraint[]
): boolean {
  for (const c of compiled) {
    if (c.kind === "forbid") {
      if (matchesPrefix(c.entries, pos, domains, depth) === "full") return false;
      continue;
    }
    if (matchesPrefix(c.when, pos, domains, depth) !== "full") continue;
    for (const { axis, values } of c.allow) {
      if (axis <= depth) {
        const v = domains[axis]![pos[axis]!]!;
        if (!values.some((allowed) => deepEqual(allowed, v))) return false;
      } else {
        const domain = domains[axis]!;
        const hasSupport = domain.some((v) =>
          values.some((allowed) => deepEqual(allowed, v))
        );
        if (!hasSupport) return false;
      }
    }
  }
  return true;
}
