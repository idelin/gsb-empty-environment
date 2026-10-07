export interface StepDefinition {
  id: string;
  deps?: string[];
  execute(ctx: StepContext): Promise<void> | void;
  compensate?(ctx: StepContext): Promise<void> | void;
}

export interface StepContext {
  runId: string;
  stepId: string;
  /** Deterministic idempotency key: retries of the same step id dedupe on it. */
  idempotencyKey: string;
}

/** Kahn's algorithm; returns layers where every step's deps live in earlier layers. */
export function topoLayers(steps: StepDefinition[]): StepDefinition[][] {
  const byId = new Map<string, StepDefinition>();
  for (const s of steps) {
    if (byId.has(s.id)) throw new Error(`duplicate step id: ${s.id}`);
    byId.set(s.id, s);
  }
  for (const s of steps) {
    for (const d of s.deps ?? []) {
      if (!byId.has(d)) throw new Error(`step ${s.id} depends on unknown step ${d}`);
    }
  }

  const indegree = new Map<string, number>();
  const dependents = new Map<string, string[]>();
  for (const s of steps) {
    indegree.set(s.id, (s.deps ?? []).length);
    for (const d of s.deps ?? []) {
      const list = dependents.get(d) ?? [];
      list.push(s.id);
      dependents.set(d, list);
    }
  }

  const layers: StepDefinition[][] = [];
  let frontier = steps.filter((s) => (indegree.get(s.id) ?? 0) === 0).map((s) => s.id);
  let placed = 0;
  while (frontier.length > 0) {
    layers.push(frontier.map((id) => byId.get(id)!));
    placed += frontier.length;
    const next: string[] = [];
    for (const id of frontier) {
      for (const dep of dependents.get(id) ?? []) {
        const remaining = indegree.get(dep)! - 1;
        indegree.set(dep, remaining);
        if (remaining === 0) next.push(dep);
      }
    }
    frontier = next;
  }
  if (placed !== steps.length) throw new Error('dependency graph has a cycle');
  return layers;
}

/** Flattened topological order (layer by layer). */
export function topoOrder(steps: StepDefinition[]): StepDefinition[] {
  return topoLayers(steps).reduce<StepDefinition[]>((acc, layer) => acc.concat(layer), []);
}
