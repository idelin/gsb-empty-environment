import { arrayDescriptor, ctorId, graphCtorId } from "./canonical.ts";
import type { TypeGraph, TypeNode } from "./types.ts";

export interface NormalizedNode {
  id: string;
  /** undefined marks a pure alias edge (ref node). */
  ctorId?: string;
  /** Structural children for ctor nodes. */
  children?: string[];
  /** Alias target for ref nodes. */
  refTarget?: string;
  origin: TypeNode;
}

export interface NormalizedGraph {
  root: string;
  nodes: Map<string, NormalizedNode>;
}

export interface GuardFailure {
  kind: "bare-alias-cycle" | "dangling-ref" | "unknown-node";
  nodeId: string;
  detail: string;
}

export interface NormalizationResult {
  normalized?: NormalizedGraph;
  failure?: GuardFailure;
}

/**
 * Normalize a message graph and enforce the guard rule: every cycle must
 * traverse at least one structural constructor. A cycle made only of ref
 * edges (a bare alias ring) is illegal. Fully iterative: no recursion, so
 * chains deeper than the JS call stack are handled.
 */
export function normalizeGraph(graph: TypeGraph): NormalizationResult {
  const nodes = new Map<string, NormalizedNode>();
  for (const [id, node] of Object.entries(graph.nodes)) {
    if (node.kind === "ref") {
      nodes.set(id, { id, refTarget: node.target, origin: node });
    } else if (node.kind === "array") {
      nodes.set(id, {
        id,
        ctorId: ctorId(arrayDescriptor(node.rank)),
        children: [node.element.kind === "ref" ? node.element.target : node.element.id],
        origin: node,
      });
    } else {
      nodes.set(id, {
        id,
        ctorId: graphCtorId({
          assemblyPublicKey: node.assemblyPublicKey,
          assemblyVersion: node.assemblyVersion,
          typeName: node.typeName,
          arity: node.args.length,
        }),
        children: node.args.map((arg) => (arg.kind === "ref" ? arg.target : arg.id)),
        origin: node,
      });
    }
  }

  if (!nodes.has(graph.root)) {
    return {
      failure: {
        kind: "unknown-node",
        nodeId: graph.root,
        detail: "root node is missing from the graph",
      },
    };
  }
  for (const node of nodes.values()) {
    if (node.refTarget && !nodes.has(node.refTarget)) {
      return {
        failure: {
          kind: "dangling-ref",
          nodeId: node.id,
          detail: `ref points at missing node ${node.refTarget}`,
        },
      };
    }
    if (node.children) {
      for (const child of node.children) {
        if (!nodes.has(child)) {
          return {
            failure: {
              kind: "unknown-node",
              nodeId: child,
              detail: `node ${node.id} references missing child`,
            },
          };
        }
      }
    }
  }

  const failure = findBareAliasCycle(nodes, graph.root);
  if (failure) {
    return { failure };
  }
  return { normalized: { root: graph.root, nodes } };
}

/**
 * Iterative DFS. Only ref edges extend the active path without contributing a
 * constructor; structural nodes reset the "seen constructor" marker, which is
 * exactly the guard condition required by the spec.
 */
function findBareAliasCycle(
  nodes: Map<string, NormalizedNode>,
  root: string,
): GuardFailure | undefined {
  const color = new Map<string, 0 | 1 | 2>();
  interface Frame {
    id: string;
    next: number;
  }
  const stack: Frame[] = [{ id: root, next: 0 }];
  color.set(root, 1);

  while (stack.length > 0) {
    const frame = stack[stack.length - 1];
    const node = nodes.get(frame.id)!;
    const successors = node.refTarget
      ? [node.refTarget]
      : node.children ?? [];
    if (frame.next >= successors.length) {
      color.set(frame.id, 2);
      stack.pop();
      continue;
    }
    const successor = successors[frame.next++];
    const state = color.get(successor);
    if (state === 1) {
      const active = stack.map((entry) => entry.id);
      const cycleStart = active.indexOf(successor);
      const cycle = active.slice(cycleStart);
      const guarded = cycle.some((id) => nodes.get(id)!.ctorId !== undefined);
      if (!guarded) {
        return {
          kind: "bare-alias-cycle",
          nodeId: successor,
          detail: `unprotected alias ring: ${cycle.join(" -> ")} -> ${successor}`,
        };
      }
    } else if (state === undefined) {
      color.set(successor, 1);
      stack.push({ id: successor, next: 0 });
    }
  }
  return undefined;
}

/** Edge label used in rejection paths. */
export function edgeLabel(node: NormalizedNode, childIndex: number): string {
  const origin = node.origin;
  if (origin.kind === "array") {
    return "element";
  }
  if (origin.kind === "named") {
    return `args[${childIndex}]`;
  }
  return `ref->${node.refTarget}`;
}
