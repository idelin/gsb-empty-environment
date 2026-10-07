export class CycleError extends Error {
  readonly cycle: readonly string[];

  constructor(cycle: readonly string[]) {
    super(`cyclic field dependency: ${cycle.join(' -> ')}`);
    this.name = 'CycleError';
    this.cycle = cycle;
  }
}

/**
 * Returns the first dependency cycle found (as a path whose last node
 * repeats the first node of the loop), or null when the graph is a DAG.
 */
export function findCycle(
  deps: ReadonlyMap<string, readonly string[]>,
): string[] | null {
  const state = new Map<string, 1 | 2>(); // 1 = on stack, 2 = done
  const stack: string[] = [];

  const visit = (node: string): string[] | null => {
    state.set(node, 1);
    stack.push(node);
    for (const dep of deps.get(node) ?? []) {
      const mark = state.get(dep);
      if (mark === 1) {
        return [...stack.slice(stack.indexOf(dep)), dep];
      }
      if (mark === undefined) {
        const found = visit(dep);
        if (found) return found;
      }
    }
    stack.pop();
    state.set(node, 2);
    return null;
  };

  for (const root of deps.keys()) {
    if (state.has(root)) continue;
    const found = visit(root);
    if (found) return found;
  }
  return null;
}

/**
 * Kahn's algorithm over the dependency graph. Every field appears after
 * all of its dependencies. Throws CycleError if the graph is not a DAG.
 */
export function topoOrder(
  deps: ReadonlyMap<string, readonly string[]>,
): string[] {
  const indegree = new Map<string, number>();
  const dependents = new Map<string, string[]>();
  for (const [node, nodeDeps] of deps) {
    indegree.set(node, nodeDeps.length);
    for (const dep of nodeDeps) {
      const list = dependents.get(dep) ?? [];
      list.push(node);
      dependents.set(dep, list);
    }
  }

  const queue: string[] = [];
  for (const [node, degree] of indegree) {
    if (degree === 0) queue.push(node);
  }

  const order: string[] = [];
  for (let head = 0; head < queue.length; head++) {
    const node = queue[head];
    order.push(node);
    for (const dependent of dependents.get(node) ?? []) {
      const degree = (indegree.get(dependent) ?? 0) - 1;
      indegree.set(dependent, degree);
      if (degree === 0) queue.push(dependent);
    }
  }

  if (order.length !== deps.size) {
    const cycle = findCycle(deps);
    throw new CycleError(cycle ?? [...deps.keys()]);
  }
  return order;
}
