import { GraphError } from './errors.ts';

export interface CellNode {
  kind: 'cell';
  id: string;
}

export interface ComputedNode {
  kind: 'computed';
  id: string;
  definitionId: string;
  definitionVersion: number;
  deps: string[];
  monotonic: boolean;
}

export type GraphNode = CellNode | ComputedNode;

export function isComputed(node: GraphNode): node is ComputedNode {
  return node.kind === 'computed';
}

/**
 * Tarjan SCC over the computed-node dependency subgraph. Returns only the
 * cyclic components (size > 1, or a single node with a self-loop).
 */
export function findCyclicComponents(nodes: Map<string, GraphNode>): string[][] {
  const index = new Map<string, number>();
  const lowlink = new Map<string, number>();
  const onStack = new Set<string>();
  const stack: string[] = [];
  const cyclic: string[][] = [];
  let counter = 0;

  const strongconnect = (id: string): void => {
    index.set(id, counter);
    lowlink.set(id, counter);
    counter += 1;
    stack.push(id);
    onStack.add(id);

    const node = nodes.get(id);
    if (node && isComputed(node)) {
      for (const dep of node.deps) {
        const depNode = nodes.get(dep);
        if (!depNode || !isComputed(depNode)) continue;
        if (!index.has(dep)) {
          strongconnect(dep);
          lowlink.set(id, Math.min(lowlink.get(id)!, lowlink.get(dep)!));
        } else if (onStack.has(dep)) {
          lowlink.set(id, Math.min(lowlink.get(id)!, index.get(dep)!));
        }
      }
    }

    if (lowlink.get(id) === index.get(id)) {
      const component: string[] = [];
      let member: string;
      do {
        member = stack.pop()!;
        onStack.delete(member);
        component.push(member);
      } while (member !== id);
      const selfLoop =
        component.length === 1 &&
        isComputed(nodes.get(component[0])!) &&
        (nodes.get(component[0]) as ComputedNode).deps.includes(component[0]);
      if (component.length > 1 || selfLoop) cyclic.push(component);
    }
  };

  for (const id of nodes.keys()) {
    if (!index.has(id)) strongconnect(id);
  }
  return cyclic;
}

/**
 * Illegal evaluation cycles: any dependency ring where at least one member is
 * not declared monotonic. Checked separately from monotonic rings.
 */
export function assertNoEvaluationCycles(nodes: Map<string, GraphNode>): void {
  for (const component of findCyclicComponents(nodes)) {
    const allMonotonic = component.every((id) => {
      const node = nodes.get(id);
      return node !== undefined && isComputed(node) && node.monotonic;
    });
    if (!allMonotonic) {
      throw new GraphError(
        'EVALUATION_CYCLE',
        `illegal evaluation cycle: ${component.join(' -> ')}`,
      );
    }
  }
}

/** Allowed monotonic rings: cyclic components whose members are all monotonic. */
export function findMonotonicRings(nodes: Map<string, GraphNode>): string[][] {
  return findCyclicComponents(nodes).filter((component) =>
    component.every((id) => {
      const node = nodes.get(id);
      return node !== undefined && isComputed(node) && node.monotonic;
    }),
  );
}
