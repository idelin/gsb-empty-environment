import type { Grant, OrgNode, OrgTree, Snapshot, User, UserException } from '../src/types.ts';

export function treeOf(nodes: OrgNode[], rootId: string): OrgTree {
  return { rootId, nodes: new Map(nodes.map((n) => [n.id, n])) };
}

export interface SnapshotSpec {
  nodes: OrgNode[];
  rootId: string;
  users: User[];
  grants?: Grant[];
  exceptions?: UserException[];
  catalog: Record<string, string[]>;
}

export function snapshot(spec: SnapshotSpec): Snapshot {
  return {
    tree: treeOf(spec.nodes, spec.rootId),
    users: spec.users,
    grants: spec.grants ?? [],
    exceptions: spec.exceptions ?? [],
    catalog: spec.catalog,
  };
}

/** 标准组织树：root -> eng -> backend；root -> sales */
export const STD_NODES: OrgNode[] = [
  { id: 'root', name: '总部', parentId: null },
  { id: 'eng', name: '工程部', parentId: 'root' },
  { id: 'backend', name: '后端组', parentId: 'eng' },
  { id: 'sales', name: '销售部', parentId: 'root' },
];

export const STD_CATALOG: Record<string, string[]> = {
  folder: ['/docs', '/docs/api', '/docs/guide', '/code', '/code/src', '/code/tests'],
};
