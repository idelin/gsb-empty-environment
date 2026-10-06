import { buildParentMap, childrenOf, findNode, flattenPreOrder, isAncestor } from './tree.ts';
import type { OutlineNode } from './tree.ts';

/**
 * Drop target in PRE-REMOVAL coordinates: index refers to the target
 * parent's children array as it was before any selected node is removed.
 * parentId null means the root level.
 */
export interface MoveTarget {
  parentId: string | null;
  index: number;
}

export type MoveError =
  | 'empty-selection'
  | 'unknown-node'
  | 'unknown-target-parent'
  | 'target-inside-selection'
  | 'index-out-of-range';

export type MoveResult =
  | { ok: true; tree: OutlineNode[]; movedIds: string[] }
  | { ok: false; error: MoveError; tree: OutlineNode[] };

/**
 * Reduce a raw multi-selection to the top-most nodes: when an ancestor and
 * its descendant are both selected, the descendant is ignored (it moves
 * with the ancestor anyway). The result is deduplicated and returned in
 * document (pre-order) order, regardless of click order.
 */
export function normalizeSelection(
  nodes: OutlineNode[],
  selectedIds: readonly string[],
): string[] {
  const wanted = new Set(selectedIds);
  const parentMap = buildParentMap(nodes);
  const out: string[] = [];
  for (const entry of flattenPreOrder(nodes)) {
    if (!wanted.has(entry.id)) continue;
    let covered = false;
    for (const kept of out) {
      if (isAncestor(parentMap, kept, entry.id)) {
        covered = true;
        break;
      }
    }
    if (!covered) out.push(entry.id);
  }
  return out;
}

function fail(error: MoveError, tree: OutlineNode[]): MoveResult {
  return { ok: false, error, tree };
}

/**
 * Move the selected nodes (multi-selection, possibly non-adjacent) to the
 * target position. Pure: the input tree is never mutated and is returned
 * unchanged on any validation failure.
 *
 * Rules:
 * - Selected descendants of selected ancestors are ignored.
 * - The target parent may not be a selected node or inside a selected
 *   subtree (no moving a node into itself or its own descendant).
 * - target.index is interpreted in pre-removal coordinates and adjusted
 *   after the selected siblings are removed.
 * - Moved nodes keep their source document (pre-order) order.
 */
export function moveNodes(
  nodes: OutlineNode[],
  selectedIds: readonly string[],
  target: MoveTarget,
): MoveResult {
  if (selectedIds.length === 0) return fail('empty-selection', nodes);

  const parentMap = buildParentMap(nodes);
  for (const id of selectedIds) {
    if (!parentMap.has(id)) return fail('unknown-node', nodes);
  }

  const movedIds = normalizeSelection(nodes, selectedIds);
  const movedSet = new Set(movedIds);

  if (target.parentId !== null && !parentMap.has(target.parentId)) {
    return fail('unknown-target-parent', nodes);
  }

  // Reject dropping into (or onto) the selection itself.
  if (target.parentId !== null) {
    if (movedSet.has(target.parentId)) return fail('target-inside-selection', nodes);
    for (const id of movedIds) {
      if (isAncestor(parentMap, id, target.parentId)) {
        return fail('target-inside-selection', nodes);
      }
    }
  }

  const targetSiblings = childrenOf(nodes, target.parentId);
  if (!targetSiblings) return fail('unknown-target-parent', nodes);
  if (!Number.isInteger(target.index) || target.index < 0 || target.index > targetSiblings.length) {
    return fail('index-out-of-range', nodes);
  }

  // Convert pre-removal coordinates: how many moved nodes sit in the target
  // parent before the requested index.
  let removedBefore = 0;
  targetSiblings.forEach((sibling, i) => {
    if (i < target.index && movedSet.has(sibling.id)) removedBefore += 1;
  });
  const insertIndex = target.index - removedBefore;

  // Clone the forest (original stays untouched for undo / failure).
  const cloneMap = new Map<string, OutlineNode>();
  const cloneNode = (node: OutlineNode): OutlineNode => {
    const copy: OutlineNode = { id: node.id, children: node.children.map(cloneNode) };
    cloneMap.set(copy.id, copy);
    return copy;
  };
  const nextRoots = nodes.map(cloneNode);

  // Detach moved nodes from their cloned parents.
  const movedNodes: OutlineNode[] = [];
  for (const id of movedIds) {
    const cloned = cloneMap.get(id)!;
    movedNodes.push(cloned);
    const siblings = childrenOf(nextRoots, parentMap.get(id) ?? null);
    if (!siblings) return fail('unknown-node', nodes);
    const at = siblings.indexOf(cloned);
    if (at >= 0) siblings.splice(at, 1);
  }

  // Insert in source document order.
  const destination =
    target.parentId === null ? nextRoots : findNode(nextRoots, target.parentId)!.children;
  destination.splice(insertIndex, 0, ...movedNodes);

  return { ok: true, tree: nextRoots, movedIds };
}
