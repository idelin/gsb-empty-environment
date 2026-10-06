import { Canvas, cloneNode, type CanvasNode, type NodeId, type Ref } from "./model.ts";
import type { IdAllocator } from "./ids.ts";

export class PasteError extends Error {}

/** An immutable snapshot of a copied subgraph. */
export interface Clipboard {
  readonly nodes: readonly CanvasNode[];
}

/** Deep-copy the given nodes (and only them) into a clipboard snapshot. */
export function copy(canvas: Canvas, ids: readonly NodeId[]): Clipboard {
  const nodes = ids.map((id) => {
    const node = canvas.get(id);
    if (!node) {
      throw new PasteError(`cannot copy: unknown node id: ${id}`);
    }
    return cloneNode(node);
  });
  return { nodes };
}

export interface PasteResult {
  /** The newly created nodes, in clipboard order. */
  nodes: CanvasNode[];
  /** old id -> new id for every copied node. */
  idMap: ReadonlyMap<NodeId, NodeId>;
}

/**
 * Paste a clipboard into the canvas.
 *
 * - every copied node receives a fresh id from the public sequence;
 * - references between copied nodes (internal) are rewritten to the new ids;
 * - references to nodes outside the copied set (external) are kept only when
 *   the target still exists on the canvas and its type matches the reference's
 *   expected type; otherwise the reference is marked broken;
 * - the paste is atomic: any failure restores the canvas and leaves the
 *   public id sequence untouched.
 */
export function paste(canvas: Canvas, clipboard: Clipboard, allocator: IdAllocator): PasteResult {
  if (clipboard.nodes.length === 0) {
    throw new PasteError("cannot paste: clipboard is empty");
  }

  const reservation = allocator.reserve(clipboard.nodes.length);
  const added: NodeId[] = [];
  try {
    const idMap = new Map<NodeId, NodeId>();
    clipboard.nodes.forEach((node, index) => {
      idMap.set(node.id, reservation.ids[index]!);
    });

    const pasted = clipboard.nodes.map((source) => {
      const node = cloneNode(source);
      node.id = idMap.get(source.id)!;
      node.refs = source.refs.map((ref) => fixupRef(ref, idMap, canvas));
      return node;
    });

    for (const node of pasted) {
      canvas.addNode(node);
      added.push(node.id);
    }

    reservation.commit();
    return { nodes: pasted, idMap };
  } catch (cause) {
    for (const id of added) {
      canvas.removeNode(id);
    }
    reservation.rollback();
    if (cause instanceof PasteError) {
      throw cause;
    }
    throw new PasteError(`paste failed: ${(cause as Error).message}`);
  }
}

function fixupRef(ref: Ref, idMap: ReadonlyMap<NodeId, NodeId>, canvas: Canvas): Ref {
  const internal = ref.target !== null ? idMap.get(ref.target) : undefined;
  if (internal !== undefined) {
    return { key: ref.key, expectedType: ref.expectedType, target: internal, status: "ok" };
  }
  if (ref.target !== null) {
    const target = canvas.get(ref.target);
    if (target && target.type === ref.expectedType) {
      return { key: ref.key, expectedType: ref.expectedType, target: target.id, status: "ok" };
    }
  }
  const broken: Ref = { key: ref.key, expectedType: ref.expectedType, target: null, status: "broken" };
  if (ref.target !== null) {
    broken.brokenTarget = ref.target;
  }
  return broken;
}
