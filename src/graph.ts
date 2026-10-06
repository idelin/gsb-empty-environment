export function topoOrder(ids: string[], deps: Map<string, string[]>): string[] {
  const indegree = new Map<string, number>();
  const dependents = new Map<string, string[]>();
  for (const id of ids) {
    indegree.set(id, (deps.get(id) ?? []).length);
  }
  for (const [id, upstream] of deps) {
    for (const dep of upstream) {
      dependents.set(dep, [...(dependents.get(dep) ?? []), id]);
    }
  }
  const queue = ids.filter((id) => (indegree.get(id) ?? 0) === 0);
  const order: string[] = [];
  while (queue.length > 0) {
    const id = queue.shift()!;
    order.push(id);
    for (const next of dependents.get(id) ?? []) {
      const remaining = (indegree.get(next) ?? 0) - 1;
      indegree.set(next, remaining);
      if (remaining === 0) queue.push(next);
    }
  }
  if (order.length !== ids.length) {
    throw new Error('dependency graph has a cycle');
  }
  return order;
}
