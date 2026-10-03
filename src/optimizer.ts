import { type Ordering, satisfies } from './ordering.ts';
import { PhysicalNode, Sort } from './operators.ts';

export interface OptimizeStats {
  sortsInserted: number;
  sortsAvoided: number;
}

export interface OptimizeResult {
  plan: PhysicalNode;
  stats: OptimizeStats;
}

/**
 * Bottom-up physical property enforcement.
 *
 * Each node is first asked which ordering it requires from its children;
 * children are optimized against those requirements. Afterwards, if the
 * node's own output does not satisfy the ordering required by its parent
 * (or by the top-level ORDER BY), a Sort enforcer is inserted on top.
 * When an underlying operator (e.g. IndexScan) already delivers the
 * required order, the Sort is decisively omitted.
 */
export function enforceOrdering(
  node: PhysicalNode,
  required: Ordering | null,
  stats: OptimizeStats,
): PhysicalNode {
  const childRequirements = node.requiredChildOrderings();
  node.children = node.children.map((child, i) =>
    enforceOrdering(child, childRequirements[i] ?? null, stats),
  );

  if (required !== null && required.length > 0) {
    if (satisfies(node.providedOrdering(), required)) {
      stats.sortsAvoided += 1;
    } else {
      stats.sortsInserted += 1;
      return new Sort(node, required);
    }
  }
  return node;
}

export function optimize(
  root: PhysicalNode,
  orderBy: Ordering | null = null,
): OptimizeResult {
  const stats: OptimizeStats = { sortsInserted: 0, sortsAvoided: 0 };
  const plan = enforceOrdering(root, orderBy, stats);
  return { plan, stats };
}
