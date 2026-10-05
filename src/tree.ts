import type { Move, NodeSpec, TreeState } from "./types.ts";

export function buildState(specs: NodeSpec[]): TreeState {
  const parent = new Map<string, string | null>();
  const name = new Map<string, string>();
  const pinned = new Set<string>();
  const localAcl = new Map<string, readonly string[]>();
  const refs = new Map<string, readonly string[]>();
  let root: string | null = null;
  for (const spec of specs) {
    if (parent.has(spec.id)) {
      throw new Error(`重复节点 id: ${spec.id}（禁止通过复制节点虚增身份）`);
    }
    parent.set(spec.id, spec.parent);
    name.set(spec.id, spec.name);
    if (spec.pinned) pinned.add(spec.id);
    localAcl.set(spec.id, spec.acl ?? []);
    refs.set(spec.id, spec.refs ?? []);
    if (spec.parent === null) {
      if (root !== null) throw new Error("存在多个根节点");
      root = spec.id;
    }
  }
  if (root === null) throw new Error("缺少根节点");
  for (const spec of specs) {
    if (spec.parent !== null && !parent.has(spec.parent)) {
      throw new Error(`节点 ${spec.id} 的父节点 ${spec.parent} 不存在`);
    }
  }
  const state: TreeState = { root, parent, name, pinned, localAcl, refs };
  for (const id of parent.keys()) {
    const seen = new Set<string>();
    let cur: string | null = id;
    while (cur !== null) {
      if (seen.has(cur)) throw new Error(`层级中存在环，涉及节点: ${cur}`);
      seen.add(cur);
      cur = parent.get(cur) ?? null;
    }
  }
  return state;
}

export function childrenOf(state: TreeState, id: string): string[] {
  const out: string[] = [];
  for (const [child, p] of state.parent) {
    if (p === id) out.push(child);
  }
  return out;
}

export function isAncestor(state: TreeState, anc: string, id: string): boolean {
  let cur = state.parent.get(id) ?? null;
  while (cur !== null) {
    if (cur === anc) return true;
    cur = state.parent.get(cur) ?? null;
  }
  return false;
}

export function descendantsOf(state: TreeState, id: string): string[] {
  const out: string[] = [];
  for (const nid of state.parent.keys()) {
    if (isAncestor(state, id, nid)) out.push(nid);
  }
  return out.sort();
}

export function pathFromRoot(state: TreeState, id: string): string[] {
  const path: string[] = [];
  let cur: string | null = id;
  while (cur !== null) {
    path.push(cur);
    cur = state.parent.get(cur) ?? null;
  }
  return path.reverse();
}

export function applyMove(state: TreeState, move: Move): TreeState {
  const { nodeId, newParentId } = move;
  if (!state.parent.has(nodeId)) throw new Error(`节点不存在: ${nodeId}`);
  if (!state.parent.has(newParentId)) throw new Error(`目标父节点不存在: ${newParentId}`);
  if (nodeId === state.root) throw new Error("根节点不可移动");
  if (state.pinned.has(nodeId)) throw new Error(`节点 ${nodeId} 已固定，不可移动`);
  if (nodeId === newParentId) throw new Error("不能将节点移动到自身之下");
  if (isAncestor(state, nodeId, newParentId)) {
    throw new Error(`不能将 ${nodeId} 移动到其子树内（会形成环）`);
  }
  if (state.parent.get(nodeId) === newParentId) {
    throw new Error("无变化的移动");
  }
  const parent = new Map(state.parent);
  parent.set(nodeId, newParentId);
  return { ...state, parent };
}

export function effectiveAcl(state: TreeState, id: string): Set<string> {
  const acl = new Set<string>();
  for (const nid of pathFromRoot(state, id)) {
    for (const grant of state.localAcl.get(nid) ?? []) acl.add(grant);
  }
  return acl;
}

export function resolveRef(state: TreeState, fromId: string, path: string): string | null {
  let cur: string | null = fromId;
  for (const seg of path.split("/")) {
    if (seg === "" || seg === ".") continue;
    if (cur === null) return null;
    if (seg === "..") {
      cur = state.parent.get(cur) ?? null;
      continue;
    }
    const next = childrenOf(state, cur).find((c) => state.name.get(c) === seg);
    cur = next ?? null;
  }
  return cur;
}

export function stateKey(state: TreeState): string {
  return [...state.parent.entries()]
    .map(([child, p]) => `${child}<${p ?? ""}`)
    .sort()
    .join("|");
}
