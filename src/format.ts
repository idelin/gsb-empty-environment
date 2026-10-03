import type { PhysicalNode } from './plan.ts';
import { formatOrdering } from './properties.ts';

function label(node: PhysicalNode): string {
  switch (node.kind) {
    case 'SeqScan':
      return `SeqScan(${node.table} ${node.alias})`;
    case 'IndexScan':
      return `IndexScan(${node.table} ${node.alias}, index=${node.index})`;
    case 'Filter':
      return `Filter(${node.predicate})`;
    case 'Project':
      return `Project(${node.columns.join(', ')})`;
    case 'Sort':
      return `Sort(${formatOrdering(node.keys)})  <-- enforcer inserted`;
    case 'StreamAggregate':
      return `StreamAggregate(keys=[${node.keys.join(', ')}], aggs=[${node.aggregates.join(', ')}])`;
    case 'MergeJoin':
      return `MergeJoin(${node.leftKey} = ${node.rightKey})`;
    case 'HashJoin':
      return `HashJoin(${node.leftKey} = ${node.rightKey})`;
  }
}

export function formatPlan(node: PhysicalNode): string {
  const lines: string[] = [];
  const render = (n: PhysicalNode, prefix: string, isLast: boolean, isRoot: boolean) => {
    lines.push(
      (isRoot ? '' : prefix + (isLast ? '└─ ' : '├─ ')) + label(n),
    );
    const children =
      n.kind === 'MergeJoin' || n.kind === 'HashJoin'
        ? [n.left, n.right]
        : 'child' in n
          ? [n.child]
          : [];
    const childPrefix = isRoot ? '' : prefix + (isLast ? '   ' : '│  ');
    children.forEach((c, i) =>
      render(c, childPrefix, i === children.length - 1, false),
    );
  };
  render(node, '', true, true);
  return lines.join('\n');
}
