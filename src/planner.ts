/**
 * Fetch planner. Goal: fetch only what suffices to decide.
 *
 * Children of and/or are evaluated cheapest-first (a heuristic, NOT a
 * proven globally minimal order). For each and/or node the first child in
 * that order is "required" (must be read to start deciding); the rest are
 * "optional" because short-circuiting may make them unnecessary.
 */
import type { Policy } from './policy.ts';

export type CostTable = Record<string, number>;

export function subtreeCost(p: Policy, costs: CostTable): number {
  switch (p.kind) {
    case 'cmp':
    case 'in':
      return costs[p.attr] ?? Number.POSITIVE_INFINITY;
    case 'not':
      return subtreeCost(p.child, costs);
    case 'and':
    case 'or':
      return Math.min(...p.children.map((c) => subtreeCost(c, costs)));
  }
}

export function orderedChildren(children: readonly Policy[], costs: CostTable): Policy[] {
  return children
    .map((c, i) => ({ c, i }))
    .sort((x, y) => {
      const d = subtreeCost(x.c, costs) - subtreeCost(y.c, costs);
      return d !== 0 ? d : x.i - y.i; // stable: keep declaration order on ties
    })
    .map((e) => e.c);
}

export interface PlanStep {
  attr: string;
  required: boolean;
  cost: number;
  path: string;
}

export function plan(policy: Policy, costs: CostTable): PlanStep[] {
  const steps: PlanStep[] = [];
  const walk = (node: Policy, required: boolean, path: string): void => {
    switch (node.kind) {
      case 'cmp':
      case 'in':
        steps.push({ attr: node.attr, required, cost: costs[node.attr] ?? Number.POSITIVE_INFINITY, path });
        return;
      case 'not':
        walk(node.child, required, `${path}!`);
        return;
      case 'and':
      case 'or': {
        const ordered = orderedChildren(node.children, costs);
        ordered.forEach((child, idx) =>
          walk(child, required && idx === 0, `${path}${node.kind === 'and' ? '&' : '|'}${idx}`),
        );
        return;
      }
    }
  };
  walk(policy, true, '');
  return steps;
}
