import type { TypeGraph, TypeNode, TypeRef } from './types.ts';

export interface GraphValidation {
  /** All node ids reachable from the root, in discovery order. */
  reachable: string[];
  /** Ids of nodes that act as container constructors (have args/element). */
  containers: Set<string>;
}

export class GraphError extends Error {
  readonly nodeId: string;
  readonly path: number[];
  constructor(
    message: string,
    nodeId: string,
    path: number[],
  ) {
    super(message);
    this.name = 'GraphError';
    this.nodeId = nodeId;
    this.path = path;
  }
}

export function refId(ref: TypeRef): string {
  return typeof ref === 'string' ? ref : ref.id;
}

/** Resolve a reference (inline or by id) to a concrete node. */
export function resolve(graph: TypeGraph, ref: TypeRef): TypeNode {
  if (typeof ref !== 'string') {
    if (graph.nodes[ref.id] !== undefined && graph.nodes[ref.id] !== ref) {
      throw new GraphError(`duplicate inline node id ${ref.id}`, ref.id, []);
    }
    return ref;
  }
  const node = graph.nodes[ref];
  if (node === undefined) {
    throw new GraphError(`dangling reference to node ${ref}`, ref, []);
  }
  return node;
}

function* childRefs(node: TypeNode): Generator<{ pos: number; ref: TypeRef }> {
  if (node.arrayRank !== undefined && node.arrayRank > 0) {
    if (node.element !== undefined) yield { pos: 0, ref: node.element };
    return;
  }
  for (const [pos, ref] of (node.args ?? []).entries()) {
    yield { pos, ref };
  }
}

/**
 * Structural validation of a descriptor graph.
 *
 * A back-reference is protected only when the cycle passes through a
 * container constructor (a node carrying generic arguments or an array
 * element). A ring with no container is a bare alias ring and is rejected.
 * The "allowed" half of the guard (the container must be one the tenant
 * grammar admits) is enforced during membership checking.
 */
/**
 * Structural validation of a descriptor graph.
 *
 * A back-reference is protected only when the cycle passes through a
 * container constructor the tenant grammar actually admits. `isContainer`
 * receives the candidate node and answers that question; when omitted, any
 * node carrying children is treated as a container (structural pre-check).
 * A ring with no admitted container is a bare alias ring and rejected.
 */
export function validateGraph(
  graph: TypeGraph,
  isContainer?: (node: TypeNode) => boolean,
): GraphValidation {
  const reachable: string[] = [];
  const containers = new Set<string>();
  const state = new Map<string, 0 | 1 | 2>(); // 0 unseen, 1 on stack, 2 done

  interface Frame {
    node: TypeNode;
    parent: Frame | undefined;
    position: number;
    childIndex: number;
    /** Number of container constructors crossed on the current cycle path. */
    guards: number;
  }

  const root = resolve(graph, graph.root);
  const stack: Frame[] = [{ node: root, parent: undefined, position: -1, childIndex: 0, guards: 0 }];
  state.set(root.id, 1);

  while (stack.length > 0) {
    const frame = stack[stack.length - 1];
    const children = [...childRefs(frame.node)];
    if (frame.childIndex === 0) {
      reachable.push(frame.node.id);
      const actsAsContainer = children.length > 0 && (isContainer?.(frame.node) ?? true);
      if (children.length > 0) {
        containers.add(frame.node.id);
      }
      if (actsAsContainer) {
        frame.guards += 1;
      }
    }
    if (frame.childIndex >= children.length) {
      state.set(frame.node.id, 2);
      stack.pop();
      continue;
    }
    const { pos, ref } = children[frame.childIndex];
    frame.childIndex += 1;
    const child = resolve(graph, ref);
    const childState = state.get(child.id) ?? 0;
    if (childState === 1) {
      // Back edge: locate the ancestor frame to measure guards on the cycle.
      const ancestorIndex = stack.findIndex((f) => f.node.id === child.id);
      let crossedContainer = false;
      for (let i = ancestorIndex; i < stack.length; i++) {
        const candidate = stack[i].node;
        const childCount = [...childRefs(candidate)].length;
        if (childCount > 0 && (isContainer?.(candidate) ?? true)) crossedContainer = true;
      }
      if (!crossedContainer) {
        throw new GraphError(
          `bare alias ring detected at recursive reference to ${child.id}`,
          child.id,
          reconstructPath(stack[stack.length - 1], pos),
        );
      }
      continue;
    }
    if (childState === 2) continue;
    state.set(child.id, 1);
    stack.push({ node: child, parent: frame, position: pos, childIndex: 0, guards: frame.guards });
  }

  return { reachable, containers };
}

function reconstructPath(frame: Frame, finalPosition: number): number[] {
  const path: number[] = [finalPosition];
  let current: Frame | undefined = frame;
  while (current !== undefined && current.position >= 0) {
    path.push(current.position);
    current = current.parent;
  }
  path.reverse();
  return path;
}
