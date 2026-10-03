import type { Ordering } from './properties.ts';

/** Logical plan: what the query means, no physical decisions yet. */
export type LogicalNode =
  | { kind: 'Scan'; table: string; alias: string }
  | { kind: 'Filter'; predicate: string; child: LogicalNode }
  | { kind: 'Project'; columns: string[]; child: LogicalNode }
  | { kind: 'GroupBy'; keys: string[]; aggregates: string[]; child: LogicalNode }
  | {
      kind: 'Join';
      left: LogicalNode;
      right: LogicalNode;
      leftKey: string;
      rightKey: string;
    }
  | { kind: 'OrderBy'; keys: Ordering; child: LogicalNode };

/** Physical plan: concrete operators with known delivered orderings. */
export type PhysicalNode =
  | { kind: 'SeqScan'; table: string; alias: string }
  | { kind: 'IndexScan'; table: string; alias: string; index: string }
  | { kind: 'Filter'; predicate: string; child: PhysicalNode }
  | { kind: 'Project'; columns: string[]; child: PhysicalNode }
  | { kind: 'Sort'; keys: Ordering; child: PhysicalNode }
  | { kind: 'StreamAggregate'; keys: string[]; aggregates: string[]; child: PhysicalNode }
  | {
      kind: 'MergeJoin';
      leftKey: string;
      rightKey: string;
      left: PhysicalNode;
      right: PhysicalNode;
    }
  | {
      kind: 'HashJoin';
      leftKey: string;
      rightKey: string;
      left: PhysicalNode;
      right: PhysicalNode;
    };

export function childrenOf(node: PhysicalNode): PhysicalNode[] {
  switch (node.kind) {
    case 'Filter':
    case 'Project':
    case 'Sort':
    case 'StreamAggregate':
      return [node.child];
    case 'MergeJoin':
    case 'HashJoin':
      return [node.left, node.right];
    default:
      return [];
  }
}

export function* walk(node: PhysicalNode): Generator<PhysicalNode> {
  yield node;
  for (const child of childrenOf(node)) yield* walk(child);
}

export function countNodes(node: PhysicalNode, kind: PhysicalNode['kind']): number {
  let count = 0;
  for (const n of walk(node)) if (n.kind === kind) count++;
  return count;
}
