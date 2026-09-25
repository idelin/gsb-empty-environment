import type { MoveSpec, NodeId, Row, TreeNode, TreeState } from './types.ts';

export function cloneTree(tree: TreeState): TreeState {
  const nodes: Record<NodeId, TreeNode> = {};
  for (const [id, node] of Object.entries(tree.nodes)) {
    nodes[id] = { ...node, childIds: [...node.childIds] };
  }
  return { nodes, rootIds: [...tree.rootIds], version: tree.version };
}

/** id 是否位于 ancestorId 的子树内（含自身） */
export function subtreeContains(tree: TreeState, ancestorId: NodeId, id: NodeId): boolean {
  let cur: TreeNode | undefined = tree.nodes[id];
  while (cur) {
    if (cur.id === ancestorId) return true;
    cur = cur.parentId !== null ? tree.nodes[cur.parentId] : undefined;
  }
  return false;
}

/** 移动合法性：双方存在、非自身、目标不在源子树内（禁止移入自身后代） */
export function isValidMove(tree: TreeState, spec: MoveSpec): boolean {
  const source = tree.nodes[spec.sourceId];
  const target = tree.nodes[spec.targetId];
  if (!source || !target) return false;
  if (spec.sourceId === spec.targetId) return false;
  if (subtreeContains(tree, spec.sourceId, spec.targetId)) return false;
  return true;
}

function detach(tree: TreeState, id: NodeId): void {
  const node = tree.nodes[id];
  const list = node.parentId === null ? tree.rootIds : tree.nodes[node.parentId].childIds;
  const index = list.indexOf(id);
  if (index >= 0) list.splice(index, 1);
}

/** 应用移动，返回新树；原树不被修改 */
export function applyMove(tree: TreeState, spec: MoveSpec): TreeState {
  if (!isValidMove(tree, spec)) {
    throw new Error(`invalid move: ${JSON.stringify(spec)}`);
  }
  const next = cloneTree(tree);
  const source = next.nodes[spec.sourceId];
  const target = next.nodes[spec.targetId];
  const oldParentId = source.parentId;
  detach(next, source.id);
  if (spec.placement === 'inside') {
    target.childIds.push(source.id);
    source.parentId = target.id;
  } else {
    const siblings = target.parentId === null ? next.rootIds : next.nodes[target.parentId].childIds;
    const index = siblings.indexOf(target.id);
    siblings.splice(spec.placement === 'before' ? index : index + 1, 0, source.id);
    source.parentId = target.parentId;
  }
  next.version += 1;
  const bumped = new Set<NodeId>([source.id, target.id]);
  if (oldParentId !== null) bumped.add(oldParentId);
  if (source.parentId !== null) bumped.add(source.parentId);
  for (const id of bumped) {
    const node = next.nodes[id];
    if (node) node.revision += 1;
  }
  return next;
}

/** 删除整棵子树，返回新树 */
export function removeSubtree(tree: TreeState, id: NodeId): TreeState {
  if (!tree.nodes[id]) return tree;
  const next = cloneTree(tree);
  const parentId = next.nodes[id].parentId;
  detach(next, id);
  const stack = [id];
  while (stack.length > 0) {
    const cur = stack.pop() as NodeId;
    const node = next.nodes[cur];
    if (!node) continue;
    stack.push(...node.childIds);
    delete next.nodes[cur];
  }
  next.version += 1;
  if (parentId !== null && next.nodes[parentId]) next.nodes[parentId].revision += 1;
  return next;
}

/** 内容编辑（独立编辑）：改标签，全树版本自增，结构 revision 不变 */
export function renameNode(tree: TreeState, id: NodeId, label: string): TreeState {
  if (!tree.nodes[id]) return tree;
  const next = cloneTree(tree);
  next.nodes[id].label = label;
  next.version += 1;
  return next;
}

/** 服务器端重排：同一父节点下子顺序的整体替换 */
export function reorderChildren(tree: TreeState, parentId: NodeId | null, orderedChildIds: NodeId[]): TreeState {
  const next = cloneTree(tree);
  const current = parentId === null ? next.rootIds : next.nodes[parentId]?.childIds;
  if (!current) throw new Error(`unknown parent: ${parentId}`);
  const sameMembers = current.length === orderedChildIds.length
    && current.every((id) => orderedChildIds.includes(id));
  if (!sameMembers) throw new Error('reorder must keep the same children');
  if (parentId === null) next.rootIds = [...orderedChildIds];
  else next.nodes[parentId].childIds = [...orderedChildIds];
  next.version += 1;
  if (parentId !== null) next.nodes[parentId].revision += 1;
  for (const id of orderedChildIds) next.nodes[id].revision += 1;
  return next;
}

/** 深度优先展开为可见行（核心布局数据，纯计算，不依赖浏览器） */
export function listRows(tree: TreeState): Row[] {
  const rows: Row[] = [];
  const walk = (id: NodeId, depth: number): void => {
    rows.push({ id, depth });
    for (const childId of tree.nodes[id].childIds) walk(childId, depth + 1);
  };
  for (const id of tree.rootIds) walk(id, 0);
  return rows;
}
