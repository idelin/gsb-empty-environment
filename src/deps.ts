// Static analysis over a rule set: read/write path dependencies between
// rules, plus early detection of alias conflicts, mutual trigger cycles
// and statically provable non-idempotent combinations.

import { pathToString } from "./json.ts";
import {
  normalizeWhitespace,
  ruleReads,
  ruleWrites,
} from "./rules.ts";
import type {
  EnumAliasRule,
  PathPattern,
  Rule,
  WhitespaceRule,
} from "./rules.ts";

/** Two patterns intersect when a concrete path can match both. */
export function patternsIntersect(a: PathPattern, b: PathPattern): boolean {
  if (a.length !== b.length) return false;
  return a.every((seg, i) => seg === "*" || b[i] === "*" || seg === b[i]);
}

function anyIntersection(a: readonly PathPattern[], b: readonly PathPattern[]): string[] {
  const hits: string[] = [];
  for (const pa of a) {
    for (const pb of b) {
      if (patternsIntersect(pa, pb)) {
        hits.push(`${pathToString(pa as never)} ~ ${pathToString(pb as never)}`);
      }
    }
  }
  return hits;
}

export interface DependencyEdge {
  /** Rule whose writes trigger the other rule. */
  from: string;
  /** Rule whose reads intersect the other's writes. */
  to: string;
  /** Human-readable intersecting pattern pairs. */
  paths: string[];
}

export type IssueType = "alias-conflict" | "trigger-cycle" | "non-idempotent-combo";

export interface RuleIssue {
  type: IssueType;
  ruleIds: string[];
  message: string;
}

export interface RuleAnalysis {
  edges: DependencyEdge[];
  issues: RuleIssue[];
}

/** Read/write dependency edges: A -> B iff writes(A) intersects reads(B). */
export function dependencyEdges(rules: readonly Rule[]): DependencyEdge[] {
  const edges: DependencyEdge[] = [];
  for (const a of rules) {
    for (const b of rules) {
      if (a.id === b.id) continue;
      const paths = anyIntersection(ruleWrites(a), ruleReads(b));
      if (paths.length > 0) edges.push({ from: a.id, to: b.id, paths });
    }
  }
  return edges;
}

/** Strongly connected components (Tarjan) over the dependency graph. */
function stronglyConnectedComponents(nodes: string[], edges: DependencyEdge[]): string[][] {
  const adj = new Map<string, string[]>();
  for (const n of nodes) adj.set(n, []);
  for (const e of edges) adj.get(e.from)?.push(e.to);

  const index = new Map<string, number>();
  const low = new Map<string, number>();
  const onStack = new Set<string>();
  const stack: string[] = [];
  const sccs: string[][] = [];
  let counter = 0;

  function visit(v: string): void {
    index.set(v, counter);
    low.set(v, counter);
    counter++;
    stack.push(v);
    onStack.add(v);
    for (const w of adj.get(v) ?? []) {
      if (!index.has(w)) {
        visit(w);
        low.set(v, Math.min(low.get(v)!, low.get(w)!));
      } else if (onStack.has(w)) {
        low.set(v, Math.min(low.get(v)!, index.get(w)!));
      }
    }
    if (low.get(v) === index.get(v)) {
      const scc: string[] = [];
      let w: string;
      do {
        w = stack.pop()!;
        onStack.delete(w);
        scc.push(w);
      } while (w !== v);
      sccs.push(scc);
    }
  }

  for (const n of nodes) if (!index.has(n)) visit(n);
  return sccs;
}

function enumRules(rules: readonly Rule[]): EnumAliasRule[] {
  return rules.filter((r): r is EnumAliasRule => r.kind === "enum-alias");
}

function aliasIssues(rules: readonly Rule[]): RuleIssue[] {
  const issues: RuleIssue[] = [];
  const enums = enumRules(rules);

  // Chains inside a single rule: canonical value is itself an alias.
  for (const rule of enums) {
    for (const [alias, canonical] of Object.entries(rule.aliases)) {
      const next = rule.aliases[canonical];
      if (next !== undefined && next !== canonical) {
        issues.push({
          type: "non-idempotent-combo",
          ruleIds: [rule.id],
          message:
            `alias chain inside rule "${rule.id}": "${alias}" -> "${canonical}" -> "${next}"; ` +
            `a second pass rewrites "${canonical}" again`,
        });
      }
    }
  }

  // Pairs of enum rules whose write sets overlap.
  for (let i = 0; i < enums.length; i++) {
    for (let j = i + 1; j < enums.length; j++) {
      const a = enums[i];
      const b = enums[j];
      if (anyIntersection(ruleWrites(a), ruleWrites(b)).length === 0) continue;
      for (const [alias, canonical] of Object.entries(a.aliases)) {
        if (Object.prototype.hasOwnProperty.call(b.aliases, alias) && b.aliases[alias] !== canonical) {
          issues.push({
            type: "alias-conflict",
            ruleIds: [a.id, b.id],
            message:
              `alias "${alias}" maps to "${canonical}" in "${a.id}" but to ` +
              `"${b.aliases[alias]}" in "${b.id}" on overlapping paths`,
          });
        }
        const chained = b.aliases[canonical];
        if (chained !== undefined && chained !== canonical) {
          issues.push({
            type: "non-idempotent-combo",
            ruleIds: [a.id, b.id],
            message:
              `alias chain across rules: "${alias}" -[${a.id}]-> "${canonical}" ` +
              `-[${b.id}]-> "${chained}" on overlapping paths`,
          });
        }
      }
    }
  }
  return issues;
}

/** A whitespace rule ordered before an enum rule whose canonical values
 *  are not whitespace-normal makes the combination non-idempotent. */
function whitespaceEnumIssues(rules: readonly Rule[]): RuleIssue[] {
  const issues: RuleIssue[] = [];
  rules.forEach((rule, idx) => {
    if (rule.kind !== "whitespace") return;
    const ws = rule as WhitespaceRule;
    for (const other of rules.slice(idx + 1)) {
      if (other.kind !== "enum-alias") continue;
      if (anyIntersection(ruleWrites(ws), ruleReads(other)).length === 0) continue;
      for (const [alias, canonical] of Object.entries(other.aliases)) {
        if (normalizeWhitespace(canonical, ws.mode) !== canonical) {
          issues.push({
            type: "non-idempotent-combo",
            ruleIds: [ws.id, other.id],
            message:
              `whitespace rule "${ws.id}" runs before enum rule "${other.id}", but canonical ` +
              `value "${canonical}" (alias "${alias}") is not whitespace-normal; ` +
              `a second pass would rewrite it`,
          });
        }
      }
    }
  });
  return issues;
}

export function analyzeRules(rules: readonly Rule[]): RuleAnalysis {
  const edges = dependencyEdges(rules);
  const issues: RuleIssue[] = [];

  // A rule "relocates" data when it writes paths it does not read
  // (e.g. copy/derive rules). Value-transform rules read and write the
  // same paths; their shared-path interaction is sequential ordering,
  // which the idempotency checks cover -- not a trigger cycle.
  const relocates = new Set(
    rules
      .filter((r) => {
        const reads = new Set(ruleReads(r).map((p) => pathToString(p as never)));
        return ruleWrites(r).some((p) => !reads.has(pathToString(p as never)));
      })
      .map((r) => r.id),
  );

  for (const scc of stronglyConnectedComponents(rules.map((r) => r.id), edges)) {
    if (scc.length > 1 && scc.some((id) => relocates.has(id))) {
      issues.push({
        type: "trigger-cycle",
        ruleIds: scc.slice().sort(),
        message: `mutual trigger cycle among rules: ${scc.slice().sort().join(", ")}`,
      });
    }
  }

  issues.push(...aliasIssues(rules));
  issues.push(...whitespaceEnumIssues(rules));
  return { edges, issues };
}

/** Ids of rules that must be recomputed when `changedIds` change:
 *  the changed rules plus everything transitively reading their writes. */
export function dependencyClosure(
  rules: readonly Rule[],
  changedIds: readonly string[],
): string[] {
  const edges = dependencyEdges(rules);
  const affected = new Set(changedIds);
  let grew = true;
  while (grew) {
    grew = false;
    for (const e of edges) {
      if (affected.has(e.from) && !affected.has(e.to)) {
        affected.add(e.to);
        grew = true;
      }
    }
  }
  return [...affected];
}
