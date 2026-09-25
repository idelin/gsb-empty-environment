import { MigrationError } from "./errors.ts";
import type { JsonValue } from "./json.ts";
import type { Schema } from "./schema.ts";

export interface StepContext {
  readonly from: string;
  readonly to: string;
}

/** A single migration edge between two versions. */
export interface MigrationStep {
  readonly name: string;
  readonly from: string;
  readonly to: string;
  /** Priority weight: lower is preferred. Defaults to 1. */
  readonly cost?: number;
  readonly inputSchema: Schema;
  readonly outputSchema: Schema;
  readonly apply: (doc: JsonValue, ctx: StepContext) => JsonValue;
}

export interface Route {
  readonly steps: MigrationStep[];
  readonly totalCost: number;
}

/**
 * Directed weighted graph of versions. Path selection is deterministic:
 * the unique minimum-cost route wins. If two or more distinct routes tie
 * at the minimum cost, the selection is ambiguous and rejected.
 */
export class VersionGraph {
  private readonly steps: MigrationStep[] = [];

  addStep(step: MigrationStep): this {
    const cost = step.cost ?? 1;
    if (!Number.isFinite(cost) || cost <= 0) {
      throw new MigrationError(
        "NO_PATH",
        `step "${step.name}" has invalid cost ${step.cost}; cost must be a positive finite number`,
        { path: `${step.from} -> ${step.to}`, step: step.name },
      );
    }
    for (const existing of this.steps) {
      if (existing.name === step.name) {
        throw new MigrationError(
          "NO_PATH",
          `duplicate step name "${step.name}"`,
          { path: `${step.from} -> ${step.to}` },
        );
      }
    }
    this.steps.push(step);
    return this;
  }

  versions(): string[] {
    const set = new Set<string>();
    for (const s of this.steps) {
      set.add(s.from);
      set.add(s.to);
    }
    return [...set].sort();
  }

  /** Find the unique cheapest route, or reject on ambiguity / absence. */
  findPath(from: string, to: string): Route {
    if (from === to) return { steps: [], totalCost: 0 };

    const adjacency = new Map<string, MigrationStep[]>();
    for (const step of this.steps) {
      const list = adjacency.get(step.from) ?? [];
      list.push(step);
      adjacency.set(step.from, list);
    }

    // Dijkstra over (cost, route) keeping, per node, the best cost and
    // every route that achieves it (to detect ties).
    const best = new Map<string, number>();
    const routes = new Map<string, MigrationStep[][]>();
    best.set(from, 0);
    routes.set(from, [[]]);

    const settled = new Set<string>();
    for (;;) {
      let node: string | undefined;
      let nodeCost = Infinity;
      for (const [n, c] of best) {
        if (!settled.has(n) && c < nodeCost) {
          node = n;
          nodeCost = c;
        }
      }
      if (node === undefined) break;
      settled.add(node);

      for (const step of adjacency.get(node) ?? []) {
        const cost = nodeCost + (step.cost ?? 1);
        const known = best.get(step.to);
        if (known === undefined || cost < known) {
          best.set(step.to, cost);
          const next: MigrationStep[][] = [];
          for (const r of routes.get(node) ?? []) next.push([...r, step]);
          routes.set(step.to, next);
        } else if (cost === known) {
          const next = routes.get(step.to) ?? [];
          for (const r of routes.get(node) ?? []) next.push([...r, step]);
          routes.set(step.to, next);
        }
      }
    }

    const totalCost = best.get(to);
    if (totalCost === undefined) {
      throw new MigrationError(
        "NO_PATH",
        `no migration route from "${from}" to "${to}"`,
        { path: `${from} -> ${to}` },
      );
    }

    const winners = dedupeRoutes(routes.get(to) ?? []);
    if (winners.length > 1) {
      const rendered = winners.map((r) => renderRoute(from, r)).join("  |  ");
      throw new MigrationError(
        "AMBIGUOUS_PATH",
        `${winners.length} routes with equal cost ${totalCost}: ${rendered}`,
        { path: `${from} -> ${to}` },
      );
    }
    return { steps: winners[0], totalCost };
  }
}

function dedupeRoutes(routes: MigrationStep[][]): MigrationStep[][] {
  const seen = new Set<string>();
  const out: MigrationStep[][] = [];
  for (const route of routes) {
    const key = route.map((s) => s.name).join("");
    if (!seen.has(key)) {
      seen.add(key);
      out.push(route);
    }
  }
  return out;
}

function renderRoute(from: string, steps: MigrationStep[]): string {
  let out = from;
  for (const s of steps) out += ` -[${s.name}]-> ${s.to}`;
  return out;
}
