import type { ColumnDef, TreeNode } from '../src/types.ts';

export function node(
  id: string,
  children?: TreeNode[],
  extra: Partial<TreeNode> = {},
): TreeNode {
  return { id, children, expanded: children !== undefined, ...extra };
}

export function cols(...keys: string[]): ColumnDef[] {
  return keys.map((key) => ({ key }));
}

export function rowIds(grid: { rows: { id: string }[] }): string[] {
  return grid.rows.map((r) => r.id);
}
