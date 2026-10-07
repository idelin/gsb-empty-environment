import { Graph, type CanvasNode, type NodeId, type NodeRef } from './graph.ts';
import type { IdAllocator } from './id-allocator.ts';

export interface PasteResult {
  /** Newly pasted nodes, in clipboard order. */
  readonly nodes: readonly CanvasNode[];
  /** old clipboard id -> new public id */
  readonly idMap: ReadonlyMap<NodeId, NodeId>;
  /** External references that could not be kept and were marked dangling. */
  readonly brokenRefs: readonly BrokenRef[];
}

export interface BrokenRef {
  readonly nodeId: NodeId;
  readonly key: string;
  readonly originalTargetId: NodeId;
  readonly reason: 'missing-target' | 'type-mismatch';
}

export class PasteError extends Error {}

/**
 * Paste a copied subgraph into `graph`.
 *
 * - Every copied node receives a fresh public id.
 * - References whose target is inside the copied set (internal) are
 *   rewired to the corresponding new node.
 * - References to nodes outside the copied set (external) are kept only
 *   when the target exists in the destination graph and its type matches
 *   the slot's declared `targetType`; otherwise the slot is marked
 *   dangling.
 * - The operation is atomic: any failure rolls the graph back untouched
 *   and never consumes the public id sequence.
 */
export function pasteSubgraph(
  clipboard: readonly CanvasNode[],
  graph: Graph,
  allocator: IdAllocator,
): PasteResult {
  validateClipboard(clipboard);

  const copiedIds = new Set(clipboard.map((n) => n.id));
  const tx = allocator.beginTransaction();
  try {
    const idMap = new Map<NodeId, NodeId>();
    for (const node of clipboard) {
      idMap.set(node.id, tx.allocate());
    }

    const brokenRefs: BrokenRef[] = [];
    const pasted: CanvasNode[] = clipboard.map((node) => {
      const newId = idMap.get(node.id)!;
      const refs: NodeRef[] = node.refs.map((ref) =>
        rewireRef(ref, copiedIds, idMap, graph, newId, brokenRefs),
      );
      return { id: newId, type: node.type, refs };
    });

    // Atomic apply: throws before mutating if anything conflicts.
    graph.addAll(pasted);
    tx.commit();
    return { nodes: pasted, idMap, brokenRefs };
  } catch (err) {
    tx.rollback();
    throw err;
  }
}

function rewireRef(
  ref: NodeRef,
  copiedIds: ReadonlySet<NodeId>,
  idMap: ReadonlyMap<NodeId, NodeId>,
  graph: Graph,
  newNodeId: NodeId,
  brokenRefs: BrokenRef[],
): NodeRef {
  if (ref.kind === 'dangling') {
    // A slot that was already broken in the source stays broken.
    return { ...ref };
  }

  if (copiedIds.has(ref.targetId)) {
    // Internal reference: rewire to the freshly allocated node.
    return { ...ref, targetId: idMap.get(ref.targetId)! };
  }

  // External reference: keep only if the target still exists and the
  // runtime type matches the slot's declared type.
  const target = graph.get(ref.targetId);
  if (target === undefined) {
    brokenRefs.push({
      nodeId: newNodeId,
      key: ref.key,
      originalTargetId: ref.targetId,
      reason: 'missing-target',
    });
    return {
      kind: 'dangling',
      key: ref.key,
      originalTargetId: ref.targetId,
      targetType: ref.targetType,
      reason: 'missing-target',
    };
  }
  if (target.type !== ref.targetType) {
    brokenRefs.push({
      nodeId: newNodeId,
      key: ref.key,
      originalTargetId: ref.targetId,
      reason: 'type-mismatch',
    });
    return {
      kind: 'dangling',
      key: ref.key,
      originalTargetId: ref.targetId,
      targetType: ref.targetType,
      reason: 'type-mismatch',
    };
  }
  return { ...ref };
}

function validateClipboard(clipboard: readonly CanvasNode[]): void {
  if (clipboard.length === 0) {
    throw new PasteError('clipboard is empty');
  }
  const seen = new Set<NodeId>();
  for (const node of clipboard) {
    if (seen.has(node.id)) {
      throw new PasteError(`duplicate node id in clipboard: ${node.id}`);
    }
    seen.add(node.id);
    const keys = new Set<string>();
    for (const ref of node.refs) {
      if (keys.has(ref.key)) {
        throw new PasteError(
          `duplicate ref slot "${ref.key}" on node ${node.id}`,
        );
      }
      keys.add(ref.key);
    }
  }
}
