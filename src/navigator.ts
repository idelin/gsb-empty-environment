import {
  EPS,
  center,
  crossAxisDelta,
  isDirectionalCandidate,
  mainAxisDelta,
  type Direction,
} from './geometry.ts';
import type { LayoutTree, NodeId } from './tree.ts';

export type NavReason = 'directional' | 'wrap' | 'fallback';

export interface NavPlan {
  target: NodeId;
  reason: NavReason;
  /** Unmounted ancestors/self that must be mounted before focus can land. */
  mountTargets: NodeId[];
  /** Whether scroll is required to bring the target into view. */
  needsScroll: boolean;
}

export interface PlanOptions {
  wrap?: boolean;
  exclude?: ReadonlySet<NodeId>;
}

/**
 * Public main-axis priority rule for directional candidates:
 *   1. keep only candidates that pass the published edge-based candidacy
 *      rule (see isDirectionalCandidate) and whose center moves strictly
 *      along the main axis;
 *   2. score = main-axis distance + CROSS_AXIS_PENALTY * cross-axis distance
 *      (the main axis dominates; diagonal/off-axis candidates are penalized
 *      by their cross-axis deviation);
 *   3. break ties by logical (reading) order.
 */
export const CROSS_AXIS_PENALTY = 2;

export function planNext(
  tree: LayoutTree,
  currentId: NodeId,
  dir: Direction,
  opts: PlanOptions = {},
): NavPlan | null {
  const wrap = opts.wrap ?? true;
  const exclude = opts.exclude ?? new Set<NodeId>();
  const scope = tree.modalScopeOf(currentId);
  const eligible = tree
    .subtreeIds(scope)
    .filter((id) => id !== currentId && !exclude.has(id) && tree.isEligible(id));
  if (eligible.length === 0) return null;

  const currentCenter = center(tree.effectiveRect(currentId));
  const currentRect = tree.effectiveRect(currentId);
  const scored = eligible
    .filter((id) => isDirectionalCandidate(dir, currentRect, tree.effectiveRect(id)))
    .map((id) => {
      const c = center(tree.effectiveRect(id));
      return {
        id,
        main: mainAxisDelta(dir, currentCenter, c),
        cross: crossAxisDelta(dir, currentCenter, c),
        li: tree.logicalIndex(id),
      };
    })
    .filter((s) => s.main > EPS);
  scored.sort(
    (a, b) =>
      a.main + CROSS_AXIS_PENALTY * a.cross - (b.main + CROSS_AXIS_PENALTY * b.cross) ||
      a.li - b.li,
  );

  let target: NodeId | null = null;
  let reason: NavReason;
  if (scored.length > 0) {
    target = scored[0].id;
    reason = 'directional';
  } else if (wrap) {
    target = wrapPick(tree, eligible, dir, currentCenter);
    reason = 'wrap';
  } else {
    target = logicalSuccessor(tree, eligible, currentId);
    reason = 'fallback';
  }
  if (target === null) return null;
  return {
    target,
    reason,
    mountTargets: tree.unmountedChain(target),
    needsScroll: tree.needsScroll(target),
  };
}

/** Wrap-around: jump to the extreme opposite edge, tie-broken by cross distance then logical order. */
function wrapPick(
  tree: LayoutTree,
  eligible: NodeId[],
  dir: Direction,
  currentCenter: { x: number; y: number },
): NodeId | null {
  let best: { id: NodeId; edge: number; cross: number; li: number } | null = null;
  for (const id of eligible) {
    const c = center(tree.effectiveRect(id));
    const edge =
      dir === 'right' || dir === 'left' ? c.x : c.y;
    const keyed = dir === 'right' || dir === 'down' ? edge : -edge;
    const cross =
      dir === 'right' || dir === 'left'
        ? Math.abs(c.y - currentCenter.y)
        : Math.abs(c.x - currentCenter.x);
    const li = tree.logicalIndex(id);
    if (
      best === null ||
      keyed < best.edge - EPS ||
      (Math.abs(keyed - best.edge) <= EPS &&
        (cross < best.cross - EPS ||
          (Math.abs(cross - best.cross) <= EPS && li < best.li)))
    ) {
      best = { id, edge: keyed, cross, li };
    }
  }
  return best?.id ?? null;
}

/** Legal fallback: the next eligible node in logical (reading) order, cyclically within the scope. */
function logicalSuccessor(tree: LayoutTree, eligible: NodeId[], currentId: NodeId): NodeId | null {
  const eligibleSet = new Set(eligible);
  const ordered = tree.logicalOrder().filter((id) => eligibleSet.has(id));
  if (ordered.length === 0) return null;
  const currentLi = tree.logicalIndex(currentId);
  for (const id of ordered) {
    if (tree.logicalIndex(id) > currentLi) return id;
  }
  return ordered[0];
}

/** Recovery target when the current focus is gone: first focusable-visible node in logical order. */
export function logicalFallback(tree: LayoutTree): NodeId | null {
  for (const id of tree.logicalOrder()) {
    if (tree.isFocusableNow(id)) return id;
  }
  return null;
}
