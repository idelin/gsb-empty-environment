/**
 * Physical property enforcement (Sort / Order).
 *
 * The optimizer works in two intertwined passes:
 *  - top-down: every operator tells its children which `Ordering` it REQUIRES;
 *  - bottom-up: every physical operator reports which `Ordering` it DELIVERS.
 *
 * If a child's delivered ordering does not satisfy the parent's requirement,
 * the enforcer inserts a physical `Sort` node. If an `IndexScan` already
 * delivers the required order (e.g. ORDER BY u.id over an index on u.id),
 * the Sort is omitted entirely — no external sort, no extra cost.
 */
import type { Catalog } from './catalog.ts';
import type { LogicalNode, PhysicalNode } from './plan.ts';
import { satisfies, toOrdering, type Ordering } from './properties.ts';

export interface Enforced {
  plan: PhysicalNode;
  delivered: Ordering;
}

/** Insert a Sort enforcer iff `delivered` does not satisfy `required`. */
function enforceSort(plan: PhysicalNode, delivered: Ordering, required: Ordering): PhysicalNode {
  return satisfies(delivered, required)
    ? plan
    : { kind: 'Sort', keys: required, child: plan };
}

export class Optimizer {
  private catalog: Catalog;

  constructor(catalog: Catalog) {
    this.catalog = catalog;
  }

  optimize(logical: LogicalNode): PhysicalNode {
    return this.enforce(logical, []).plan;
  }

  private enforce(node: LogicalNode, required: Ordering): Enforced {
    switch (node.kind) {
      case 'Scan':
        return this.enforceScan(node, required);

      case 'Filter': {
        // Filters preserve order: push the requirement straight down.
        const child = this.enforce(node.child, required);
        return {
          plan: { kind: 'Filter', predicate: node.predicate, child: child.plan },
          delivered: child.delivered,
        };
      }

      case 'Project': {
        // Projection preserves row order.
        const child = this.enforce(node.child, required);
        return {
          plan: { kind: 'Project', columns: node.columns, child: child.plan },
          delivered: child.delivered,
        };
      }

      case 'OrderBy': {
        // The Sort enforcer IS the physical implementation of ORDER BY;
        // if the child already delivers the order, nothing is added.
        const child = this.enforce(node.child, node.keys);
        return { plan: child.plan, delivered: node.keys };
      }

      case 'GroupBy': {
        // StreamAggregate requires input sorted by the grouping keys.
        const groupOrder = toOrdering(node.keys);
        const child = this.enforce(node.child, groupOrder);
        const plan: PhysicalNode = {
          kind: 'StreamAggregate',
          keys: node.keys,
          aggregates: node.aggregates,
          child: child.plan,
        };
        // Output of a sorted-group-by is ordered by the grouping keys.
        return { plan: enforceSort(plan, groupOrder, required), delivered: required.length ? required : groupOrder };
      }

      case 'Join':
        return this.enforceJoin(node, required);
    }
  }

  private enforceScan(
    node: Extract<LogicalNode, { kind: 'Scan' }>,
    required: Ordering,
  ): Enforced {
    const table = this.catalog.getTable(node.table);
    const qualifies = (col: string) =>
      col.startsWith(`${node.alias}.`) &&
      col.slice(node.alias.length + 1) !== '';

    // Find an index whose key prefix covers the required ordering.
    const index = table.indexes.find((idx) => {
      if (required.length === 0 || required.length > idx.columns.length) return false;
      return required.every(
        (req, i) =>
          qualifies(req.column) &&
          req.column.slice(node.alias.length + 1) === idx.columns[i] &&
          req.direction === 'asc',
      );
    });

    if (index) {
      // IndexScan delivers the FULL index key order (a prefix satisfies the requirement).
      const delivered = toOrdering(
        index.columns.map((c) => `${node.alias}.${c}`),
      );
      return {
        plan: { kind: 'IndexScan', table: node.table, alias: node.alias, index: index.name },
        delivered,
      };
    }

    const plan: PhysicalNode = { kind: 'SeqScan', table: node.table, alias: node.alias };
    return { plan: enforceSort(plan, [], required), delivered: required.length ? required : [] };
  }

  private enforceJoin(
    node: Extract<LogicalNode, { kind: 'Join' }>,
    required: Ordering,
  ): Enforced {
    const leftReq = toOrdering([node.leftKey]);
    const rightReq = toOrdering([node.rightKey]);

    // Ask both sides for merge-join order; check what they deliver natively.
    const leftForMerge = this.enforce(node.left, leftReq);
    const rightForMerge = this.enforce(node.right, rightReq);
    const leftNative = satisfies(leftForMerge.delivered, leftReq) &&
      leftForMerge.plan.kind !== 'Sort';
    const rightNative = satisfies(rightForMerge.delivered, rightReq) &&
      rightForMerge.plan.kind !== 'Sort';

    // Heuristic: pick MergeJoin when at least one side is natively ordered
    // (at most one Sort needed); otherwise HashJoin is cheaper.
    if (leftNative || rightNative) {
      const plan: PhysicalNode = {
        kind: 'MergeJoin',
        leftKey: node.leftKey,
        rightKey: node.rightKey,
        left: leftForMerge.plan,
        right: rightForMerge.plan,
      };
      // MergeJoin output keeps the left input's order on the join key.
      const delivered = leftNative ? leftReq : [];
      return {
        plan: enforceSort(plan, delivered, required),
        delivered: required.length && satisfies(delivered, required) ? required : delivered,
      };
    }

    const left = this.enforce(node.left, []);
    const right = this.enforce(node.right, []);
    const plan: PhysicalNode = {
      kind: 'HashJoin',
      leftKey: node.leftKey,
      rightKey: node.rightKey,
      left: left.plan,
      right: right.plan,
    };
    return { plan: enforceSort(plan, [], required), delivered: required.length ? required : [] };
  }
}

export function optimize(logical: LogicalNode, catalog: Catalog): PhysicalNode {
  return new Optimizer(catalog).optimize(logical);
}
